// Command argosd is the Argos CMDB daemon entry point.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/collector"
	"github.com/sthalbert/longue-vue/internal/eol"
	"github.com/sthalbert/longue-vue/internal/httputil"
	"github.com/sthalbert/longue-vue/internal/impact"
	argmcp "github.com/sthalbert/longue-vue/internal/mcp"
	"github.com/sthalbert/longue-vue/internal/metrics"
	"github.com/sthalbert/longue-vue/internal/secrets"
	"github.com/sthalbert/longue-vue/internal/store"
	"github.com/sthalbert/longue-vue/ui"
)

// version is set at build time via -ldflags.
var version = "dev"

// Sentinel errors for configuration validation.
var (
	errDatabaseURLRequired     = errors.New("LONGUE_VUE_DATABASE_URL is required")
	errLegacyTokensUnsupported = errors.New("LONGUE_VUE_API_TOKEN / LONGUE_VUE_API_TOKENS are no longer supported; " +
		"the bootstrap admin password is printed in the startup log on first run, " +
		"and machine tokens are issued in the admin panel — see ADR-0007")
	errCollectorClustersEmpty = errors.New("LONGUE_VUE_COLLECTOR_CLUSTERS is empty")
	errClusterNameRequired    = errors.New("LONGUE_VUE_COLLECTOR_CLUSTERS entry: name is required")
	errDuplicateClusterName   = errors.New("LONGUE_VUE_COLLECTOR_CLUSTERS entry: duplicate name")
	errNoCollectorClusters    = errors.New("LONGUE_VUE_COLLECTOR_CLUSTERS or LONGUE_VUE_CLUSTER_NAME must be set when LONGUE_VUE_COLLECTOR_ENABLED=true")
	errInvalidCookiePolicy    = errors.New("LONGUE_VUE_SESSION_SECURE_COOKIE must be auto / always / never")
	errEncryptedCredentials   = errors.New("secrets master key missing but cloud_accounts rows carry encrypted credentials")
	errIngestMissingTLSConfig = errors.New("LONGUE_VUE_INGEST_LISTEN_ADDR is set but LONGUE_VUE_INGEST_LISTEN_TLS_CERT, " +
		"LONGUE_VUE_INGEST_LISTEN_TLS_KEY, or LONGUE_VUE_INGEST_LISTEN_CLIENT_CA_FILE is missing — see ADR-0016 §4")
	errTransportPostureRefused = errors.New("LONGUE_VUE_REQUIRE_HTTPS=true but neither native TLS " +
		"(LONGUE_VUE_PUBLIC_LISTEN_TLS_CERT + _KEY) nor a trusted-proxy + always-secure-cookie posture " +
		"(LONGUE_VUE_TRUSTED_PROXIES non-empty AND LONGUE_VUE_SESSION_SECURE_COOKIE=always) is configured — see ADR-0017 §3")
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	metrics.SetBuildInfo(version)

	if err := run(); err != nil {
		slog.Error("argosd exited with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// runConfig holds parsed configuration for the argosd daemon.
type runConfig struct {
	addr            string
	dsn             string
	cookiePolicy    auth.SecureCookiePolicy
	oidcCfg         auth.OIDCConfig
	shutdownTimeout time.Duration
	autoMigrate     bool
	// ingest configures the optional mTLS-only ingest listener used by
	// the DMZ ingest gateway (ADR-0016). When ingest.addr is empty the
	// listener is not started and argosd behaves identically to today.
	ingest ingestListenerConfig
	// Public-listener TLS posture and proxy trust (ADR-0017). All four
	// fields default to "off" so existing deployments are unchanged.
	// publicTLSCert + publicTLSKey: opt argosd into native TLS on the
	// public listener; both must be set together.
	publicTLSCert string
	publicTLSKey  string
	// trustedProxies enumerates the immediate-peer CIDRs whose
	// X-Forwarded-For and X-Forwarded-Proto headers argosd will honor.
	// Empty (the default) means no peer is trusted — both headers are
	// ignored unconditionally, which is the secure default.
	trustedProxies []*net.IPNet
	// requireHTTPS turns the §3 startup guard on. When true, argosd
	// refuses to come up unless either native TLS is configured or a
	// trusted-proxy + always-secure-cookie posture is set.
	requireHTTPS bool
}

// ingestListenerConfig captures the env-var surface for the ADR-0016
// mTLS ingest listener. Empty addr → disabled.
type ingestListenerConfig struct {
	addr         string
	tlsCertFile  string
	tlsKeyFile   string
	clientCAFile string
	clientCNs    []string // empty = any CN signed by the CA passes
}

// loadRunConfig reads and validates all configuration from the environment.
//
//nolint:gocyclo // complexity is structural: one branch per env var; refactoring adds indirection without clarity
func loadRunConfig() (runConfig, error) {
	dsn := os.Getenv("LONGUE_VUE_DATABASE_URL")
	if dsn == "" {
		return runConfig{}, errDatabaseURLRequired
	}
	// Per ADR-0007: env-var token bootstrap is removed. Fail loudly so
	// operators migrating from v0 know to read the admin password from
	// the startup log instead.
	if os.Getenv("LONGUE_VUE_API_TOKEN") != "" || os.Getenv("LONGUE_VUE_API_TOKENS") != "" {
		return runConfig{}, errLegacyTokensUnsupported
	}
	cookiePolicy, err := parseCookiePolicy()
	if err != nil {
		return runConfig{}, err
	}
	shutdownTimeout, err := parseDurationEnv("LONGUE_VUE_SHUTDOWN_TIMEOUT", 15*time.Second)
	if err != nil {
		return runConfig{}, err
	}
	autoMigrate, err := parseBoolEnv("LONGUE_VUE_AUTO_MIGRATE", true)
	if err != nil {
		return runConfig{}, err
	}
	ingest, err := loadIngestListenerConfig()
	if err != nil {
		return runConfig{}, err
	}
	trustedProxies, err := httputil.ParseTrustedProxies(os.Getenv("LONGUE_VUE_TRUSTED_PROXIES"))
	if err != nil {
		return runConfig{}, fmt.Errorf("parse LONGUE_VUE_TRUSTED_PROXIES: %w", err)
	}
	requireHTTPS, err := parseBoolEnv("LONGUE_VUE_REQUIRE_HTTPS", false)
	if err != nil {
		return runConfig{}, err
	}

	cfg := runConfig{
		addr:            envOr("LONGUE_VUE_ADDR", ":8080"),
		dsn:             dsn,
		cookiePolicy:    cookiePolicy,
		oidcCfg:         loadOIDCConfig(),
		shutdownTimeout: shutdownTimeout,
		autoMigrate:     autoMigrate,
		ingest:          ingest,
		publicTLSCert:   os.Getenv("LONGUE_VUE_PUBLIC_LISTEN_TLS_CERT"),
		publicTLSKey:    os.Getenv("LONGUE_VUE_PUBLIC_LISTEN_TLS_KEY"),
		trustedProxies:  trustedProxies,
		requireHTTPS:    requireHTTPS,
	}
	if err := checkTransportPosture(&cfg); err != nil {
		return runConfig{}, err
	}
	return cfg, nil
}

// checkTransportPosture enforces the ADR-0017 §3 startup guard. Returns
// nil when LONGUE_VUE_REQUIRE_HTTPS is off (legacy posture, allowed by default
// for backwards compatibility and dev workflows), and otherwise refuses to
// start unless one of the two safe deployment shapes is configured:
//
//   - native TLS on the public listener (publicTLSCert + publicTLSKey
//     both set), or
//   - trusted-proxy + always-secure-cookie (trustedProxies non-empty AND
//     cookiePolicy = SecureAlways).
//
// This catches the pentest topology — direct-exposed plaintext :8080 with
// no trust list — at boot rather than per-request.
func checkTransportPosture(cfg *runConfig) error {
	if !cfg.requireHTTPS {
		return nil
	}
	nativeTLS := cfg.publicTLSCert != "" && cfg.publicTLSKey != ""
	proxyShape := len(cfg.trustedProxies) > 0 && cfg.cookiePolicy == auth.SecureAlways
	if nativeTLS || proxyShape {
		return nil
	}
	return errTransportPostureRefused
}

// loadIngestListenerConfig reads the LONGUE_VUE_INGEST_LISTEN_* env vars. When
// LONGUE_VUE_INGEST_LISTEN_ADDR is empty the listener is disabled; otherwise
// the cert + key + CA paths are required so misconfiguration fails at boot.
func loadIngestListenerConfig() (ingestListenerConfig, error) {
	addr := os.Getenv("LONGUE_VUE_INGEST_LISTEN_ADDR")
	if addr == "" {
		return ingestListenerConfig{}, nil
	}
	cert := os.Getenv("LONGUE_VUE_INGEST_LISTEN_TLS_CERT")
	key := os.Getenv("LONGUE_VUE_INGEST_LISTEN_TLS_KEY")
	clientCA := os.Getenv("LONGUE_VUE_INGEST_LISTEN_CLIENT_CA_FILE")
	if cert == "" || key == "" || clientCA == "" {
		return ingestListenerConfig{}, errIngestMissingTLSConfig
	}
	cns := splitCSV(os.Getenv("LONGUE_VUE_INGEST_LISTEN_CLIENT_CN_ALLOW"))
	return ingestListenerConfig{
		addr:         addr,
		tlsCertFile:  cert,
		tlsKeyFile:   key,
		clientCAFile: clientCA,
		clientCNs:    cns,
	}, nil
}

// splitCSV trims and splits a comma-separated env var, dropping empties.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func run() error { //nolint:gocyclo // daemon bootstrap; flat structure is clearer than factored helpers
	cfg, err := loadRunConfig()
	if err != nil {
		return err
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pg, err := store.Open(rootCtx, cfg.dsn)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer pg.Close()

	if err := maybeAutoMigrate(rootCtx, pg, cfg.autoMigrate); err != nil {
		return err
	}

	if err := bootstrapAdminIfNeeded(rootCtx, pg); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}

	encrypter, err := initSecretsEncrypter(rootCtx, pg)
	if err != nil {
		return err
	}

	oidcProvider, err := maybeInitOIDC(rootCtx, &cfg.oidcCfg)
	if err != nil {
		return err
	}

	drainCollectors, err := maybeStartCollectors(rootCtx, pg)
	if err != nil {
		return err
	}
	defer drainCollectors()

	drainEOL, err := maybeStartEOLEnricher(rootCtx, pg)
	if err != nil {
		return err
	}
	defer drainEOL()

	drainMCP, err := maybeStartMCPServer(rootCtx, pg)
	if err != nil {
		return err
	}
	defer drainMCP()

	srv, err := buildHTTPServer(&cfg, pg, oidcProvider, encrypter)
	if err != nil {
		return fmt.Errorf("build public listener: %w", err)
	}

	// Optional mTLS-only ingest listener fronted by the DMZ gateway
	// (ADR-0016). Started in parallel with the public listener and
	// drained alongside it on shutdown. When cfg.ingest.addr is empty,
	// ingestSrv is nil and serveAndShutdown becomes a no-op for that
	// half.
	ingestSrv, err := buildIngestServer(&cfg, pg, oidcProvider, encrypter)
	if err != nil {
		return fmt.Errorf("build ingest listener: %w", err)
	}

	return serveAndShutdown(rootCtx, srv, ingestSrv, cfg.shutdownTimeout)
}

// initSecretsEncrypter constructs the AES-256-GCM encrypter (ADR-0015).
// Behaviour:
//   - master key set + valid → return encrypter, log fingerprint.
//   - master key absent + no rows with stored secrets → return nil with
//     a WARN; VM collector features are disabled until the operator
//     supplies a key.
//   - master key absent + at least one row with stored secrets → fatal.
func initSecretsEncrypter(ctx context.Context, pg *store.PG) (*secrets.Encrypter, error) {
	enc, err := secrets.NewEncrypterFromEnv()
	if err == nil {
		slog.Info("secrets encrypter initialised",
			slog.String("master_key_fingerprint", enc.Fingerprint()))
		return enc, nil
	}
	if !errors.Is(err, secrets.ErrMasterKeyMissing) {
		return nil, fmt.Errorf("secrets encrypter: %w", err)
	}
	count, cerr := pg.CountCloudAccountsWithSecrets(ctx)
	if cerr != nil {
		return nil, fmt.Errorf("secrets encrypter: count rows: %w", cerr)
	}
	if count > 0 {
		return nil, fmt.Errorf("%w: %d row(s) require %s", errEncryptedCredentials, count, secrets.MasterKeyEnvVar)
	}
	slog.Warn("secrets master key not configured; VM collector features disabled until LONGUE_VUE_SECRETS_MASTER_KEY is set")
	return nil, nil //nolint:nilnil // nil encrypter is the intentional "disabled" sentinel; callers check for nil before use
}

// maybeAutoMigrate runs embedded goose migrations when enabled.
func maybeAutoMigrate(ctx context.Context, pg *store.PG, enabled bool) error {
	if !enabled {
		return nil
	}
	if err := pg.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	slog.Info("migrations applied")
	return nil
}

// maybeInitOIDC resolves the OIDC provider when configured. Fatal on
// misconfig so operators see the error at boot, not per-request 500s.
func maybeInitOIDC(ctx context.Context, cfg *auth.OIDCConfig) (*auth.OIDCProvider, error) {
	provider, err := auth.NewOIDCProvider(ctx, cfg)
	if err != nil && !errors.Is(err, auth.ErrOIDCDisabled) {
		return nil, fmt.Errorf("oidc provider: %w", err)
	}
	if provider != nil {
		slog.Info("oidc configured",
			slog.String("issuer", provider.Config.Issuer),
			slog.String("redirect_url", provider.Config.RedirectURL),
			slog.String("label", provider.Config.Label),
		)
	}
	return provider, nil
}

// buildHTTPServer wires all HTTP routes, middleware, and the server struct.
//
// When cfg.publicTLSCert + cfg.publicTLSKey are both set (ADR-0017 §1),
// the returned server carries a TLS 1.3 config with hot certificate reload
// via newCertReloader; serveAndShutdown then starts it with
// ListenAndServeTLS. When either is unset, the listener stays plaintext —
// the legacy posture, allowed for backward compatibility but refused at
// boot when LONGUE_VUE_REQUIRE_HTTPS=true (see checkTransportPosture).
func buildHTTPServer(cfg *runConfig, pg *store.PG, oidcProvider *auth.OIDCProvider, enc *secrets.Encrypter) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler())
	// SPA served unauthenticated under /ui/; the bundle is static and the
	// API calls it makes from the browser carry their own bearer token.
	mux.Handle("/ui/", http.StripPrefix("/ui", ui.Handler()))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusFound)
	})
	// Settings endpoints — hand-written, gated on admin role internally.
	// Inject "admin" scope into context so the auth middleware resolves
	// the caller (it skips public routes that lack scope declarations).
	settingsAuth := auth.Middleware(pg, cfg.cookiePolicy, cfg.trustedProxies)
	requireAdminScope := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			//nolint:staticcheck // matches oapi-codegen context key convention
			ctx := context.WithValue(
				r.Context(), "BearerAuth.Scopes", []string{"admin"},
			)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	mux.Handle("GET /v1/admin/settings", requireAdminScope(settingsAuth(api.HandleGetSettings(pg))))
	mux.Handle("PATCH /v1/admin/settings", requireAdminScope(settingsAuth(api.HandleUpdateSettings(pg))))
	// Impact analysis endpoint — requires read scope (any authenticated user).
	requireReadScope := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			//nolint:staticcheck // matches oapi-codegen context key convention
			ctx := context.WithValue(
				r.Context(), "BearerAuth.Scopes", []string{"read"},
			)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	impactAuth := auth.Middleware(pg, cfg.cookiePolicy, cfg.trustedProxies)
	mux.Handle("GET /v1/impact/{entity_type}/{id}", requireReadScope(impactAuth(impact.HandleImpact(pg))))

	// Cloud-accounts + virtual-machines (ADR-0015) — hand-written
	// handlers. Each route mounts the auth middleware after a scope
	// declaration, mirroring the settings + impact pattern.
	requireScope := func(scope string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				//nolint:staticcheck // matches oapi-codegen context key convention
				ctx := context.WithValue(r.Context(), "BearerAuth.Scopes", []string{scope})
				next.ServeHTTP(w, r.WithContext(ctx))
			})
		}
	}
	cloudAuth := auth.Middleware(pg, cfg.cookiePolicy, cfg.trustedProxies)
	// Audit middleware for the hand-written routes — reads the caller
	// from the request context (set by cloudAuth) and inserts a row
	// into audit_events. Wrapping order: requireScope → cloudAuth →
	// auditWrap → handler, so the audit layer always sees an
	// authenticated caller. Mirrors the strict-server router below
	// where AuditMiddleware sits inside AuthMiddleware in the chain.
	auditWrap := api.AuditMiddleware(pg, "api", cfg.trustedProxies)

	// Admin-side cloud-accounts.
	mux.Handle("GET /v1/admin/cloud-accounts", requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleListCloudAccounts(pg)))))
	mux.Handle("POST /v1/admin/cloud-accounts", requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleCreateCloudAccount(pg, enc)))))
	mux.Handle("GET /v1/admin/cloud-accounts/{id}", requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleGetCloudAccount(pg)))))
	mux.Handle("PATCH /v1/admin/cloud-accounts/{id}", requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandlePatchCloudAccount(pg)))))
	mux.Handle(
		"PATCH /v1/admin/cloud-accounts/{id}/credentials",
		requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandlePatchCloudAccountCredentials(pg, enc)))),
	)
	mux.Handle("POST /v1/admin/cloud-accounts/{id}/disable", requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleDisableCloudAccount(pg)))))
	mux.Handle("POST /v1/admin/cloud-accounts/{id}/enable", requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleEnableCloudAccount(pg)))))
	mux.Handle("DELETE /v1/admin/cloud-accounts/{id}", requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleDeleteCloudAccount(pg)))))
	mux.Handle(
		"POST /v1/admin/cloud-accounts/{id}/tokens",
		requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleCreateCloudAccountToken(pg)))),
	)

	// Collector-side cloud-accounts (vm-collector scope).
	mux.Handle("POST /v1/cloud-accounts", requireScope(auth.ScopeVMCollector)(cloudAuth(auditWrap(api.HandleCollectorRegisterCloudAccount(pg)))))
	mux.Handle(
		"PATCH /v1/cloud-accounts/{id}/status",
		requireScope(auth.ScopeVMCollector)(cloudAuth(auditWrap(api.HandleCollectorPatchCloudAccountStatus(pg)))),
	)
	mux.Handle(
		"GET /v1/cloud-accounts/by-name/{name}/credentials",
		requireScope(auth.ScopeVMCollector)(cloudAuth(auditWrap(api.HandleCollectorGetCredentialsByName(pg, enc)))),
	)
	mux.Handle(
		"GET /v1/cloud-accounts/{id}/credentials",
		requireScope(auth.ScopeVMCollector)(cloudAuth(auditWrap(api.HandleCollectorGetCredentialsByID(pg, enc)))),
	)

	// Virtual-machines.
	mux.Handle("POST /v1/virtual-machines", requireScope(auth.ScopeVMCollector)(cloudAuth(auditWrap(api.HandleUpsertVirtualMachine(pg)))))
	mux.Handle(
		"POST /v1/virtual-machines/reconcile",
		requireScope(auth.ScopeVMCollector)(cloudAuth(auditWrap(api.HandleReconcileVirtualMachines(pg)))),
	)
	mux.Handle("GET /v1/virtual-machines", requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleListVirtualMachines(pg)))))
	mux.Handle(
		"GET /v1/virtual-machines/applications/distinct",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleListDistinctVMApplications(pg)))),
	)
	mux.Handle("GET /v1/virtual-machines/{id}", requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleGetVirtualMachine(pg)))))
	mux.Handle("PATCH /v1/virtual-machines/{id}", requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandlePatchVirtualMachine(pg)))))
	mux.Handle("DELETE /v1/virtual-machines/{id}", requireScope(auth.ScopeDelete)(cloudAuth(auditWrap(api.HandleDeleteVirtualMachine(pg)))))

	loginLimiter := api.NewLoginRateLimiter()
	verifyLimiter := api.NewVerifyRateLimiter()
	apiServer := api.NewServer(version, pg, cfg.cookiePolicy, oidcProvider, loginLimiter, verifyLimiter)
	apiServer.SetTrustedProxies(cfg.trustedProxies)
	strict := api.NewStrictHandlerWithOptions(
		apiServer,
		[]api.StrictMiddlewareFunc{api.InjectRequestMiddleware},
		api.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				slog.Warn("request parse error", slog.Any("error", err))
				http.Error(w, "invalid request", http.StatusBadRequest)
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				slog.Error("unhandled handler error", slog.Any("error", err))
				http.Error(w, "internal server error", http.StatusInternalServerError)
			},
		},
	)
	api.HandlerWithOptions(strict, api.StdHTTPServerOptions{
		BaseRouter: mux,
		// Order matters: oapi-codegen wraps in list order, so the last
		// entry becomes the outermost handler (runs first). Auth must be
		// outermost so it resolves the caller before the audit layer reads
		// it from the request context.
		Middlewares: []api.MiddlewareFunc{
			api.AuditMiddleware(pg, "api", cfg.trustedProxies),
			api.AuthMiddleware(pg, cfg.cookiePolicy, cfg.trustedProxies),
		},
	})

	// /v1/auth/verify is reachable only on the mTLS-only ingest listener
	// (ADR-0016 §3). The codegen router registers it on every mux it
	// wires, so 404 it here on the public listener as defence in depth
	// in case an operator runs argosd without configuring the ingest
	// listener separately.
	publicHandler := blockIngestOnlyPaths(mux)

	secureHandler := api.SecurityHeadersMiddleware(cfg.trustedProxies, cfg.requireHTTPS)(
		http.MaxBytesHandler(publicHandler, 1<<20),
	)
	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           metrics.InstrumentHandler(secureHandler),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if cfg.publicTLSCert != "" && cfg.publicTLSKey != "" {
		getCert, err := newCertReloader(cfg.publicTLSCert, cfg.publicTLSKey)
		if err != nil {
			return nil, fmt.Errorf("load public listener cert: %w", err)
		}
		srv.TLSConfig = &tls.Config{
			MinVersion:     tls.VersionTLS13,
			GetCertificate: getCert,
		}
	}
	return srv, nil
}

// blockIngestOnlyPaths 404s requests to paths that should never appear on
// argosd's public listener. Today that's only POST /v1/auth/verify
// (ADR-0016 §3): the ingest listener serves it; the public listener must
// not. Belt-and-braces — the spec doesn't declare auth on the verify
// endpoint, so a misconfigured deployment that mounts only the public
// listener could otherwise expose it to anonymous callers.
func blockIngestOnlyPaths(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/verify" {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// buildIngestServer constructs the optional mTLS-only ingest listener
// (ADR-0016). Returns (nil, nil) when cfg.ingest.addr is empty —
// "ingest disabled" is fully supported. Failure to load the cert/key/CA
// is fatal so misconfiguration shows up at boot, not as cryptic 500s
// per request.
func buildIngestServer(
	cfg *runConfig,
	pg *store.PG,
	oidcProvider *auth.OIDCProvider,
	enc *secrets.Encrypter,
) (*http.Server, error) {
	if cfg.ingest.addr == "" {
		return nil, nil //nolint:nilnil // nil server is the supported "disabled" sentinel
	}
	_ = oidcProvider // not used by the ingest listener; kept in the signature for symmetry
	_ = enc          // same — encrypter is for cloud-account credentials, untouched here

	clientCAs, err := loadPEMCertPool(cfg.ingest.clientCAFile)
	if err != nil {
		return nil, fmt.Errorf("load ingest client CA: %w", err)
	}
	getCert, err := newCertReloader(cfg.ingest.tlsCertFile, cfg.ingest.tlsKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load ingest server cert: %w", err)
	}

	loginLimiter := api.NewLoginRateLimiter() // unused on ingest, but Server requires non-nil
	verifyLimiter := api.NewVerifyRateLimiter()
	ingestServer := api.NewServer(version, pg, cfg.cookiePolicy, oidcProvider, loginLimiter, verifyLimiter)
	ingestServer.SetTrustedProxies(cfg.trustedProxies)
	strict := api.NewStrictHandlerWithOptions(
		ingestServer,
		[]api.StrictMiddlewareFunc{api.InjectRequestMiddleware},
		api.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				slog.Warn("ingest request parse error", slog.Any("error", err))
				http.Error(w, "invalid request", http.StatusBadRequest)
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				slog.Error("ingest unhandled handler error", slog.Any("error", err))
				http.Error(w, "internal server error", http.StatusInternalServerError)
			},
		},
	)
	mux := api.NewIngestMux(api.IngestMuxConfig{
		Server:          strict,
		AuthMiddleware:  api.AuthMiddleware(pg, cfg.cookiePolicy, nil),
		AuditMiddleware: api.AuditMiddleware(pg, "ingest_gw", cfg.trustedProxies),
		CookiePolicy:    cfg.cookiePolicy,
	})

	cnAllow := cfg.ingest.clientCNs
	tlsCfg := &tls.Config{
		MinVersion:             tls.VersionTLS13,
		ClientAuth:             tls.RequireAndVerifyClientCert,
		ClientCAs:              clientCAs,
		SessionTicketsDisabled: true,
		GetCertificate:         getCert,
		VerifyPeerCertificate:  enforceCNAllowlist(cnAllow),
	}

	return &http.Server{
		Addr:              cfg.ingest.addr,
		Handler:           metrics.InstrumentHandler(api.SecurityHeadersMiddleware(nil, true)(http.MaxBytesHandler(mux, 1<<20))),
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}, nil
}

// loadPEMCertPool reads a PEM bundle from disk into an x509.CertPool.
// Empty / missing files produce an explicit error so the operator sees
// the misconfiguration at boot.
func loadPEMCertPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path; fail loud if missing
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("no PEM certificates in %q", path) //nolint:err113 // local sentinel, not compared by callers
	}
	return pool, nil
}

// newCertReloader returns a TLSConfig.GetCertificate callback backed by
// an atomic pointer to the loaded keypair. The caller can swap the file
// contents on disk at any time; the next handshake reloads on a stat
// change. Used for both server-side and client-side cert hot-reload.
//
// This minimal version reloads on every handshake when the file's mtime
// changes — sufficient for argosd-side (cert rotation is infrequent).
// The gateway binary (cmd/argos-ingest-gw) gets an fsnotify-driven
// equivalent because it sees more frequent rotations from Vault Agent.
func newCertReloader(certFile, keyFile string) (func(*tls.ClientHelloInfo) (*tls.Certificate, error), error) {
	// Validate at startup so a missing / malformed cert fails the boot.
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		return nil, fmt.Errorf("load keypair: %w", err)
	}
	var (
		mu     sync.Mutex
		cached tls.Certificate
		mtime  time.Time
	)
	return func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
		st, err := os.Stat(certFile)
		if err != nil {
			return nil, fmt.Errorf("stat cert: %w", err)
		}
		mu.Lock()
		defer mu.Unlock()
		if !st.ModTime().Equal(mtime) {
			fresh, err := tls.LoadX509KeyPair(certFile, keyFile)
			if err != nil {
				return nil, fmt.Errorf("reload keypair: %w", err)
			}
			cached = fresh
			mtime = st.ModTime()
		}
		return &cached, nil
	}, nil
}

// enforceCNAllowlist returns a TLSConfig.VerifyPeerCertificate callback
// that fails the handshake if the leaf cert's Subject CN is not in the
// allow list. Empty list = any CN signed by the trusted CA passes.
//
// Increments argos_ingest_listener_client_cert_failures_total{reason="cn_not_allowed"}
// on rejection so a misconfigured gateway is diagnosable from a single
// Prometheus query.
func enforceCNAllowlist(allow []string) func([][]byte, [][]*x509.Certificate) error {
	if len(allow) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(allow))
	for _, cn := range allow {
		allowed[cn] = struct{}{}
	}
	return func(_ [][]byte, verifiedChains [][]*x509.Certificate) error {
		if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
			metrics.IngestListenerClientCertFailure("none_provided")
			return fmt.Errorf("no verified peer certificate") //nolint:err113 // local sentinel returned to TLS stack, never compared
		}
		leaf := verifiedChains[0][0]
		if _, ok := allowed[leaf.Subject.CommonName]; !ok {
			metrics.IngestListenerClientCertFailure("cn_not_allowed")
			return fmt.Errorf("client cert CN %q not in allow list", leaf.Subject.CommonName) //nolint:err113 // dynamic CN, not a comparable sentinel
		}
		return nil
	}
}

// serveAndShutdown starts the public HTTP server (and, when configured,
// the mTLS-only ingest listener), waits for a shutdown signal, and drains
// both gracefully. ingestSrv may be nil — argosd treats the ingest listener
// as opt-in and the absence of one is fully supported.
func serveAndShutdown( //nolint:gocyclo // central shutdown dispatcher; flat select is clearer than nested helpers
	rootCtx context.Context,
	srv *http.Server,
	ingestSrv *http.Server,
	shutdownTimeout time.Duration,
) error {
	errCh := make(chan error, 2)
	go func() {
		mode := "plaintext"
		if srv.TLSConfig != nil {
			mode = "tls"
		}
		slog.Info("argosd listening",
			slog.String("addr", srv.Addr),
			slog.String("version", version),
			slog.String("public_listener_mode", mode),
		)
		// When TLSConfig is set the cert/key are sourced via
		// GetCertificate, so the file paths passed here are ignored.
		var err error
		if srv.TLSConfig != nil {
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("public listener: %w", err)
		}
	}()
	if ingestSrv != nil {
		go func() {
			slog.Info("argosd ingest listener starting",
				slog.String("addr", ingestSrv.Addr),
				slog.String("version", version),
			)
			// TLS+mTLS — cert/key paths are baked into TLSConfig.GetCertificate.
			if err := ingestSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("ingest listener: %w", err)
			}
		}()
	}

	select {
	case <-rootCtx.Done():
		slog.Info("shutdown signal received, draining",
			slog.String("timeout", shutdownTimeout.String()),
		)
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	var firstErr error
	if err := srv.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck // detached context — parent is already cancelled by shutdown signal
		firstErr = fmt.Errorf("public listener shutdown: %w", err)
	}
	if ingestSrv != nil {
		if err := ingestSrv.Shutdown(shutdownCtx); err != nil && firstErr == nil { //nolint:contextcheck // see above — same detached shutdown context
			firstErr = fmt.Errorf("ingest listener shutdown: %w", err)
		}
	}
	if firstErr != nil {
		return firstErr
	}
	slog.Info("argosd stopped cleanly")
	return nil
}

// collectorClusterConfig is one entry in LONGUE_VUE_COLLECTOR_CLUSTERS.
// Kubeconfig may be empty to mean "use in-cluster config" (typically when
// argosd runs inside one of the target clusters).
type collectorClusterConfig struct {
	Name       string `json:"name"`
	Kubeconfig string `json:"kubeconfig"`
}

// loadCollectorClusters resolves the list of target clusters from env per
// ADR-0005. Precedence:
//   - LONGUE_VUE_COLLECTOR_CLUSTERS (JSON array of {name, kubeconfig}): primary.
//   - LONGUE_VUE_CLUSTER_NAME + LONGUE_VUE_KUBECONFIG: legacy single-cluster shortcut.
//
// Returns an error if neither form is set or if the JSON is malformed / has
// empty or duplicate names.
func loadCollectorClusters() ([]collectorClusterConfig, error) {
	if raw := os.Getenv("LONGUE_VUE_COLLECTOR_CLUSTERS"); raw != "" {
		var parsed []collectorClusterConfig
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return nil, fmt.Errorf("parse LONGUE_VUE_COLLECTOR_CLUSTERS: %w", err)
		}
		if len(parsed) == 0 {
			return nil, errCollectorClustersEmpty
		}
		seen := make(map[string]struct{}, len(parsed))
		for i, c := range parsed {
			if c.Name == "" {
				return nil, fmt.Errorf("LONGUE_VUE_COLLECTOR_CLUSTERS[%d]: %w", i, errClusterNameRequired)
			}
			if _, dup := seen[c.Name]; dup {
				return nil, fmt.Errorf("LONGUE_VUE_COLLECTOR_CLUSTERS[%d] %q: %w", i, c.Name, errDuplicateClusterName)
			}
			seen[c.Name] = struct{}{}
		}
		return parsed, nil
	}

	if name := os.Getenv("LONGUE_VUE_CLUSTER_NAME"); name != "" {
		return []collectorClusterConfig{{
			Name:       name,
			Kubeconfig: os.Getenv("LONGUE_VUE_KUBECONFIG"),
		}}, nil
	}

	return nil, errNoCollectorClusters
}

// collectorEnvConfig holds parsed environment configuration for the collector.
type collectorEnvConfig struct {
	interval     time.Duration
	fetchTimeout time.Duration
	reconcile    bool
}

// loadCollectorEnvConfig reads collector-specific env vars.
func loadCollectorEnvConfig() (collectorEnvConfig, error) {
	interval, err := parseDurationEnv("LONGUE_VUE_COLLECTOR_INTERVAL", 5*time.Minute)
	if err != nil {
		return collectorEnvConfig{}, err
	}
	fetchTimeout, err := parseDurationEnv("LONGUE_VUE_COLLECTOR_FETCH_TIMEOUT", 10*time.Second)
	if err != nil {
		return collectorEnvConfig{}, err
	}
	reconcile, err := parseBoolEnv("LONGUE_VUE_COLLECTOR_RECONCILE", true)
	if err != nil {
		return collectorEnvConfig{}, err
	}
	return collectorEnvConfig{
		interval:     interval,
		fetchTimeout: fetchTimeout,
		reconcile:    reconcile,
	}, nil
}

// maybeStartCollectors spawns one Kubernetes collector goroutine per entry
// in LONGUE_VUE_COLLECTOR_CLUSTERS (or per the legacy single-cluster env vars)
// when LONGUE_VUE_COLLECTOR_ENABLED is truthy. Returns a drain function the
// caller defers so main.run blocks on collector shutdown before returning.
// When the collector is disabled the drain is a no-op.
func maybeStartCollectors(ctx context.Context, s api.Store) (func(), error) {
	enabled, err := parseBoolEnv("LONGUE_VUE_COLLECTOR_ENABLED", false)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return func() {}, nil
	}

	clusters, err := loadCollectorClusters()
	if err != nil {
		return nil, err
	}
	envCfg, err := loadCollectorEnvConfig()
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	for _, cfg := range clusters {
		source, err := collector.NewKubeClient(cfg.Kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("init kube client for cluster %q: %w", cfg.Name, err)
		}
		coll := collector.New(s, source, cfg.Name, envCfg.interval, envCfg.fetchTimeout, envCfg.reconcile)
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if err := coll.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("collector exited with error",
					slog.String("error", err.Error()),
					slog.String("cluster_name", name),
				)
			}
		}(cfg.Name)
	}
	slog.Info("collectors started", slog.Int("count", len(clusters)))

	return wg.Wait, nil
}

// bootstrapAdminIfNeeded ensures at least one active admin user exists in
// the database. Runs on every start; idempotent once an admin is present.
//
// Password sources, in order:
//   - LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD env var — operators who want a
//     predictable password they control;
//   - otherwise, a fresh 16-char random printed once at WARN level with
//     a loud banner so it can't be missed in kubectl logs.
//
// Either way the user is flagged must_change_password so the first UI
// login is forced into rotation.
func bootstrapAdminIfNeeded(ctx context.Context, s *store.PG) error {
	n, err := s.CountActiveAdmins(ctx)
	if err != nil {
		return fmt.Errorf("count active admins: %w", err)
	}
	if n > 0 {
		return nil
	}

	password := os.Getenv("LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD")
	fromEnv := password != ""
	if !fromEnv {
		password, err = auth.RandomSecret(12)
		if err != nil {
			return fmt.Errorf("generate bootstrap password: %w", err)
		}
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash bootstrap password: %w", err)
	}
	if _, err := s.CreateUser(ctx, api.UserInsert{
		Username:           "admin",
		PasswordHash:       hash,
		Role:               auth.RoleAdmin,
		MustChangePassword: true,
	}); err != nil {
		return fmt.Errorf("create bootstrap admin: %w", err)
	}

	banner := strings.Repeat("=", 72)
	source := "LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD"
	if !fromEnv {
		source = "generated randomly; capture now — it won't be printed again"
	}
	slog.Warn("\n" + banner +
		"\n  ARGOS FIRST-RUN BOOTSTRAP" +
		"\n  A default admin user has been created:" +
		"\n    username: admin" +
		"\n    password: " + password +
		"\n    source:   " + source +
		"\n  This account MUST rotate its password on first login." +
		"\n" + banner)
	return nil
}

// loadOIDCConfig reads the LONGUE_VUE_OIDC_* env vars. Returns a zero-value
// config (Issuer == "") when OIDC is not configured; NewOIDCProvider
// treats that as "disabled". Validation happens in NewOIDCProvider.
func loadOIDCConfig() auth.OIDCConfig {
	cfg := auth.OIDCConfig{
		Issuer:       os.Getenv("LONGUE_VUE_OIDC_ISSUER"),
		ClientID:     os.Getenv("LONGUE_VUE_OIDC_CLIENT_ID"),
		ClientSecret: os.Getenv("LONGUE_VUE_OIDC_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("LONGUE_VUE_OIDC_REDIRECT_URL"),
		Label:        os.Getenv("LONGUE_VUE_OIDC_LABEL"),
	}
	if raw := os.Getenv("LONGUE_VUE_OIDC_SCOPES"); raw != "" {
		parts := strings.Split(raw, ",")
		cfg.Scopes = make([]string, 0, len(parts))
		for _, p := range parts {
			if p = strings.TrimSpace(p); p != "" {
				cfg.Scopes = append(cfg.Scopes, p)
			}
		}
	}
	return cfg
}

func parseCookiePolicy() (auth.SecureCookiePolicy, error) {
	switch strings.ToLower(envOr("LONGUE_VUE_SESSION_SECURE_COOKIE", "auto")) {
	case "auto":
		return auth.SecureAuto, nil
	case "always", "true", "yes":
		return auth.SecureAlways, nil
	case "never", "false", "no":
		return auth.SecureNever, nil
	default:
		return 0, errInvalidCookiePolicy
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseDurationEnv(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q: %w", key, v, err)
	}
	return d, nil
}

func parseBoolEnv(key string, fallback bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("parse %s=%q: %w", key, v, err)
	}
	return b, nil
}

func parseIntEnv(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q: %w", key, v, err)
	}
	return n, nil
}

// maybeStartEOLEnricher spawns the EOL enrichment goroutine (ADR-0012).
// The goroutine always starts; actual enrichment is gated by the
// `eol_enabled` setting in the database (toggled by admins via the UI).
// LONGUE_VUE_EOL_ENABLED seeds the DB setting on first boot when present.
// Returns a drain function the caller defers.
func maybeStartEOLEnricher(ctx context.Context, s api.Store) (func(), error) {
	// Seed the DB setting from env var when explicitly set.
	if envVal := os.Getenv("LONGUE_VUE_EOL_ENABLED"); envVal != "" {
		enabled, err := strconv.ParseBool(envVal)
		if err != nil {
			return nil, fmt.Errorf("parse LONGUE_VUE_EOL_ENABLED=%q: %w", envVal, err)
		}
		if _, err := s.UpdateSettings(ctx, api.SettingsPatch{EOLEnabled: &enabled}); err != nil {
			slog.Warn("eol enricher: failed to seed settings from env", slog.Any("error", err))
		}
	}

	interval, err := parseDurationEnv("LONGUE_VUE_EOL_INTERVAL", 2*time.Minute)
	if err != nil {
		return nil, err
	}
	approachingDays, err := parseIntEnv("LONGUE_VUE_EOL_APPROACHING_DAYS", 90)
	if err != nil {
		return nil, err
	}
	baseURL := envOr("LONGUE_VUE_EOL_BASE_URL", "https://endoflife.date")

	client := eol.NewClient(baseURL, interval, nil)
	enricher := eol.NewEnricher(s, client, interval, approachingDays)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := enricher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("eol enricher exited with error", slog.String("error", err.Error()))
		}
	}()

	slog.Info("eol enricher goroutine started (actual enrichment gated by DB setting)",
		slog.String("interval", interval.String()),
		slog.Int("approaching_days", approachingDays),
		slog.String("base_url", baseURL),
	)

	return wg.Wait, nil
}

// maybeStartMCPServer spawns the MCP server goroutine (ADR-0014).
// The goroutine always starts; tool calls are gated by the `mcp_enabled`
// setting in the database (toggled by admins via the UI).
// LONGUE_VUE_MCP_ENABLED seeds the DB setting on first boot when present.
//
//nolint:gocyclo,gocognit // auth setup + env parsing + goroutine lifecycle is inherently branchy.
func maybeStartMCPServer(ctx context.Context, s *store.PG) (func(), error) {
	if envVal := os.Getenv("LONGUE_VUE_MCP_ENABLED"); envVal != "" {
		enabled, err := strconv.ParseBool(envVal)
		if err != nil {
			return nil, fmt.Errorf("parse LONGUE_VUE_MCP_ENABLED=%q: %w", envVal, err)
		}
		if _, err := s.UpdateSettings(ctx, api.SettingsPatch{MCPEnabled: &enabled}); err != nil {
			slog.Warn("mcp server: failed to seed settings from env", slog.Any("error", err))
		}
	}

	transport := envOr("LONGUE_VUE_MCP_TRANSPORT", "sse")
	addr := envOr("LONGUE_VUE_MCP_ADDR", ":8090")
	token := os.Getenv("LONGUE_VUE_MCP_TOKEN")

	// For SSE transport, validate bearer tokens on every tool call using
	// the same auth store that the REST API uses.
	// For SSE transport, validate bearer tokens on every tool call.
	// Argon2id verification is expensive (~100-500ms, 64 MiB), so we
	// cache verified prefixes for 5 minutes to avoid re-hashing on
	// every tool call in a conversation.
	var authFn argmcp.AuthFunc
	if transport == "sse" {
		type cachedAuth struct {
			validUntil time.Time
			fullToken  string // last verified full token for this prefix
		}
		var (
			cacheMu sync.Mutex
			cache   = make(map[string]cachedAuth)
		)
		const cacheTTL = 5 * time.Minute

		authFn = func(ctx context.Context, rawToken string) error {
			prefix, full, err := auth.ParseToken(rawToken)
			if err != nil {
				return fmt.Errorf("invalid token: %w", err)
			}

			// Check cache — skip argon2id if recently verified.
			cacheMu.Lock()
			if entry, ok := cache[prefix]; ok && time.Now().Before(entry.validUntil) && entry.fullToken == full {
				cacheMu.Unlock()
				return nil
			}
			cacheMu.Unlock()

			// Full verification: DB lookup + argon2id.
			tok, err := s.GetActiveTokenByPrefix(ctx, prefix)
			if err != nil {
				return fmt.Errorf("token lookup failed: %w", err)
			}
			if verr := auth.VerifyPassword(full, tok.Hash); verr != nil {
				return fmt.Errorf("token verification failed: %w", verr)
			}
			hasRead := false
			for _, scope := range tok.Scopes {
				if scope == "read" || scope == "admin" {
					hasRead = true
					break
				}
			}
			if !hasRead {
				return errors.New("token lacks read scope") //nolint:err113 // one-off auth error
			}

			// Cache the verified result.
			cacheMu.Lock()
			cache[prefix] = cachedAuth{validUntil: time.Now().Add(cacheTTL), fullToken: full}
			cacheMu.Unlock()
			return nil
		}
	}

	cfg := argmcp.Config{
		Transport: transport,
		Addr:      addr,
		Token:     token,
		Auth:      authFn,
	}

	traverser := impact.NewTraverser(s)
	mcpSrv := argmcp.NewServer(s, traverser, cfg)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := mcpSrv.Run(ctx); !errors.Is(err, context.Canceled) {
			slog.Error("mcp server exited with error", slog.String("error", err.Error()))
		}
	}()

	slog.Info("mcp server goroutine started (tool calls gated by DB setting)",
		slog.String("transport", transport),
		slog.String("addr", addr),
	)

	return wg.Wait, nil
}
