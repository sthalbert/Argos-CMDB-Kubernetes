// Command longue-vue is the longue-vue CMDB daemon entry point.
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

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/api/swagger"
	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/collector"
	"github.com/sthalbert/longue-vue/internal/eol"
	"github.com/sthalbert/longue-vue/internal/httputil"
	"github.com/sthalbert/longue-vue/internal/imageversions"
	"github.com/sthalbert/longue-vue/internal/imageversions/mirrorresolve"
	"github.com/sthalbert/longue-vue/internal/imageversions/registry"
	"github.com/sthalbert/longue-vue/internal/impact"
	argmcp "github.com/sthalbert/longue-vue/internal/mcp"
	"github.com/sthalbert/longue-vue/internal/metrics"
	"github.com/sthalbert/longue-vue/internal/metricsrefresh"
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
	errNoCollectorClusters    = errors.New(
		"LONGUE_VUE_COLLECTOR_CLUSTERS or LONGUE_VUE_CLUSTER_NAME must be set when LONGUE_VUE_COLLECTOR_ENABLED=true",
	)
	errInvalidCookiePolicy    = errors.New("LONGUE_VUE_SESSION_SECURE_COOKIE must be auto / always / never")
	errEncryptedCredentials   = errors.New("secrets master key missing but cloud_accounts rows carry encrypted credentials")
	errIngestMissingTLSConfig = errors.New("LONGUE_VUE_INGEST_LISTEN_ADDR is set but LONGUE_VUE_INGEST_LISTEN_TLS_CERT, " +
		"LONGUE_VUE_INGEST_LISTEN_TLS_KEY, or LONGUE_VUE_INGEST_LISTEN_CLIENT_CA_FILE is missing — see ADR-0016 §4")
	// errMCPAuthFailed is the generic sentinel returned to MCP clients when
	// authentication fails for any reason. Internal details are logged at
	// Warn level and never forwarded to the client (MED-04).
	errMCPAuthFailed           = errors.New("authentication failed")
	errTransportPostureRefused = errors.New("LONGUE_VUE_REQUIRE_HTTPS=true but neither native TLS " +
		"(LONGUE_VUE_PUBLIC_LISTEN_TLS_CERT + _KEY) nor a trusted-proxy + always-secure-cookie posture " +
		"(LONGUE_VUE_TRUSTED_PROXIES non-empty AND LONGUE_VUE_SESSION_SECURE_COOKIE=always) is configured — see ADR-0017 §3")
	errExtractMaxRowsInvalid       = errors.New("LONGUE_VUE_EXTRACT_MAX_ROWS is not a positive integer")
	errVerifyRateLimitRPSInvalid   = errors.New("LONGUE_VUE_VERIFY_RATE_LIMIT_RPS is not a positive number")
	errVerifyRateLimitBurstInvalid = errors.New("LONGUE_VUE_VERIFY_RATE_LIMIT_BURST is not a positive integer")
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	metrics.SetBuildInfo(version)

	if err := run(); err != nil {
		slog.Error("longue-vue exited with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// runConfig holds parsed configuration for the longue-vue daemon.
type runConfig struct {
	addr            string
	dsn             string
	cookiePolicy    auth.SecureCookiePolicy
	oidcCfg         auth.OIDCConfig
	shutdownTimeout time.Duration
	autoMigrate     bool
	// ingest configures the optional mTLS-only ingest listener used by
	// the DMZ ingest gateway (ADR-0016). When ingest.addr is empty the
	// listener is not started and longue-vue behaves identically to today.
	ingest ingestListenerConfig
	// Public-listener TLS posture and proxy trust (ADR-0017). All four
	// fields default to "off" so existing deployments are unchanged.
	// publicTLSCert + publicTLSKey: opt longue-vue into native TLS on the
	// public listener; both must be set together.
	publicTLSCert string
	publicTLSKey  string
	// trustedProxies enumerates the immediate-peer CIDRs whose
	// X-Forwarded-For and X-Forwarded-Proto headers longue-vue will honor.
	// Empty (the default) means no peer is trusted — both headers are
	// ignored unconditionally, which is the secure default.
	trustedProxies []*net.IPNet
	// requireHTTPS turns the §3 startup guard on. When true, longue-vue
	// refuses to come up unless either native TLS is configured or a
	// trusted-proxy + always-secure-cookie posture is set.
	requireHTTPS bool
	// extractMaxRows caps the number of rows returned by the /v1/*/extract
	// endpoints. Defaults to 50 000; overridden via LONGUE_VUE_EXTRACT_MAX_ROWS.
	extractMaxRows int
	// verifyRateLimitRPS / Burst tune the per-IP /v1/auth/verify limiter
	// (ADR-0016 §5). Since every push-collector verify hits longue-vue from
	// the single ingest-gw pod IP, the defaults can become a bottleneck once
	// many agents are deployed. Override via
	// LONGUE_VUE_VERIFY_RATE_LIMIT_{RPS,BURST}.
	verifyRateLimitRPS   float64
	verifyRateLimitBurst int
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

	extractMaxRows := 50000
	if v := os.Getenv("LONGUE_VUE_EXTRACT_MAX_ROWS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return runConfig{}, fmt.Errorf("%w: LONGUE_VUE_EXTRACT_MAX_ROWS=%q", errExtractMaxRowsInvalid, v)
		}
		extractMaxRows = n
	}

	verifyRPS := float64(api.DefaultVerifyRateLimitRPS)
	if v := os.Getenv("LONGUE_VUE_VERIFY_RATE_LIMIT_RPS"); v != "" {
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || n <= 0 {
			return runConfig{}, fmt.Errorf("%w: LONGUE_VUE_VERIFY_RATE_LIMIT_RPS=%q", errVerifyRateLimitRPSInvalid, v)
		}
		verifyRPS = n
	}
	verifyBurst := api.DefaultVerifyRateLimitBurst
	if v := os.Getenv("LONGUE_VUE_VERIFY_RATE_LIMIT_BURST"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return runConfig{}, fmt.Errorf("%w: LONGUE_VUE_VERIFY_RATE_LIMIT_BURST=%q", errVerifyRateLimitBurstInvalid, v)
		}
		verifyBurst = n
	}

	cfg := runConfig{
		addr:                 envOr("LONGUE_VUE_ADDR", ":8080"),
		dsn:                  dsn,
		cookiePolicy:         cookiePolicy,
		oidcCfg:              loadOIDCConfig(),
		shutdownTimeout:      shutdownTimeout,
		autoMigrate:          autoMigrate,
		ingest:               ingest,
		publicTLSCert:        os.Getenv("LONGUE_VUE_PUBLIC_LISTEN_TLS_CERT"),
		publicTLSKey:         os.Getenv("LONGUE_VUE_PUBLIC_LISTEN_TLS_KEY"),
		trustedProxies:       trustedProxies,
		requireHTTPS:         requireHTTPS,
		extractMaxRows:       extractMaxRows,
		verifyRateLimitRPS:   verifyRPS,
		verifyRateLimitBurst: verifyBurst,
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

	if err := rescueLockedAdminIfNeeded(rootCtx, pg); err != nil {
		return fmt.Errorf("rescue admin: %w", err)
	}

	encrypter, err := initSecretsEncrypter(rootCtx, pg)
	if err != nil {
		return err
	}
	if encrypter != nil {
		pg.SetEncrypter(encrypter)
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

	drainMetricsRefresh, err := maybeStartMetricsRefresh(rootCtx, pg)
	if err != nil {
		return err
	}
	defer drainMetricsRefresh()

	imgVersionsEnricher, drainImageVersions, err := maybeStartImageVersionsEnricher(rootCtx, pg)
	if err != nil {
		return fmt.Errorf("image versions enricher: %w", err)
	}
	defer drainImageVersions()

	if err := seedFlowMatrixSetting(rootCtx, pg); err != nil {
		return fmt.Errorf("flow matrix setting: %w", err)
	}

	if err := seedClusterStaleSetting(rootCtx, pg); err != nil {
		return fmt.Errorf("cluster stale setting: %w", err)
	}

	if err := seedPoliciesSetting(rootCtx, pg); err != nil {
		return fmt.Errorf("policies setting: %w", err)
	}

	drainMCP, err := maybeStartMCPServer(rootCtx, pg)
	if err != nil {
		return err
	}
	defer drainMCP()

	srv, err := buildHTTPServer(&cfg, pg, oidcProvider, encrypter, imgVersionsEnricher)
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

	slog.Info("longue-vue config",
		slog.Int("extract_max_rows", cfg.extractMaxRows),
		slog.Float64("verify_rate_limit_rps", cfg.verifyRateLimitRPS),
		slog.Int("verify_rate_limit_burst", cfg.verifyRateLimitBurst),
	)
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
//
//nolint:maintidx // pre-existing complexity; this branch's additions are 6 line items for the flow-matrix read routes
func buildHTTPServer(
	cfg *runConfig,
	pg *store.PG,
	oidcProvider *auth.OIDCProvider,
	enc *secrets.Encrypter,
	imgVersionsEnricher api.EnricherTrigger,
) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler())
	// SPA served unauthenticated under /ui/; the bundle is static and the
	// API calls it makes from the browser carry their own bearer token.
	mux.Handle("/ui/", http.StripPrefix("/ui", ui.Handler()))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusFound)
	})
	// API documentation (ADR-0025).
	// Swagger UI shell is public — same precedent as /ui/*: the static
	// assets carry no secrets, and the actual sensitive surface (the spec)
	// is gated below.
	swaggerUI := swagger.UIHandler()
	mux.Handle("GET /docs", http.RedirectHandler("/docs/", http.StatusMovedPermanently))
	mux.Handle("GET /docs/", http.StripPrefix("/docs", swaggerUI))
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
	// /openapi.yaml — same auth posture as any /v1 read.
	swaggerSpecAuth := auth.Middleware(pg, cfg.cookiePolicy, cfg.trustedProxies)
	mux.Handle("GET /openapi.yaml", requireReadScope(swaggerSpecAuth(swagger.OpenAPISpecHandler())))
	impactAuth := auth.Middleware(pg, cfg.cookiePolicy, cfg.trustedProxies)
	mux.Handle("GET /v1/impact/{entity_type}/{id}", requireReadScope(impactAuth(impact.HandleImpact(pg))))

	// Time-travel history endpoints (ADR-0021 Phase 3) — hand-written routes.
	// GET /v1/{kind}/{id}/history — paginated history list (newest first).
	historyAuth := auth.Middleware(pg, cfg.cookiePolicy, cfg.trustedProxies)
	for _, kindPath := range []string{"clusters", "namespaces", "nodes", "workloads"} {
		kind := kindPath // capture loop variable
		mux.Handle(
			"GET /v1/"+kind+"/{id}/history",
			requireReadScope(historyAuth(api.HandleEntityHistory(pg, kind))),
		)
	}

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

	// Image-registry mirror credentials reveal (admin, audit-logged).
	mux.Handle(
		"GET /v1/admin/image-versions/registries/{hostname}/{path_prefix}/credentials",
		requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleGetImageRegistryCredentials(pg)))),
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
	mux.Handle("GET /v1/os-images", requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleListOSImages(pg)))))
	mux.Handle("GET /v1/container-freshness", requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleListContainerFreshness(pg)))))
	mux.Handle("GET /v1/container-freshness/extract",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleContainerFreshnessExtract(pg, cfg.extractMaxRows)))))
	mux.Handle("GET /v1/virtual-machines/{id}", requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleGetVirtualMachine(pg)))))
	mux.Handle("PATCH /v1/virtual-machines/{id}", requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandlePatchVirtualMachine(pg)))))
	mux.Handle("DELETE /v1/virtual-machines/{id}", requireScope(auth.ScopeDelete)(cloudAuth(auditWrap(api.HandleDeleteVirtualMachine(pg)))))
	mux.Handle(
		"POST /v1/ingest/cloud-accounts/{id}/security-groups/sweep",
		requireScope(auth.ScopeVMCollector)(cloudAuth(auditWrap(api.HandleSweepSecurityGroups(pg)))),
	)
	mux.Handle(
		"POST /v1/ingest/cloud-accounts/{id}/node-images",
		requireScope(auth.ScopeVMCollector)(cloudAuth(auditWrap(api.HandleBackfillNodeImages(pg)))),
	)

	// Security groups — read endpoints (flow-matrix P1, Task 19).
	mux.Handle("GET /v1/security-groups",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleListSecurityGroups(pg)))))
	mux.Handle("GET /v1/security-groups/{id}",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleGetSecurityGroup(pg)))))

	// Network policies — read endpoints (flow-matrix P1, Tasks 17 + 18).
	mux.Handle("GET /v1/network-policies",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleListNetworkPolicies(pg)))))
	mux.Handle("GET /v1/network-policies/{id}",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleGetNetworkPolicy(pg)))))
	// Network policies — push routes (ADR-0038): POST /v1/network-policies (write scope)
	// and POST /v1/network-policies/reconcile (delete scope) are served by the
	// codegen router (HandlerWithOptions above) which sets BearerAuthScopes in
	// context and applies the shared AuthMiddleware + AuditMiddleware chain.

	// Kyverno policies — read + write endpoints (ADR-0043). POST routes
	// accept externally-authored rows (source='api'); collector sweep
	// skips API-sourced rows via the source discriminator column.
	mux.Handle("POST /v1/cluster-policies",
		requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandleCreateClusterPolicy(pg)))))
	mux.Handle("GET /v1/cluster-policies",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleListClusterPolicies(pg)))))
	mux.Handle("GET /v1/cluster-policies/{id}",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleGetClusterPolicy(pg)))))
	mux.Handle("POST /v1/policy-reports",
		requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandleCreatePolicyReport(pg)))))
	mux.Handle("GET /v1/policy-reports",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleListPolicyReports(pg)))))
	mux.Handle("GET /v1/policy-reports/{id}",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleGetPolicyReport(pg)))))
	mux.Handle("DELETE /v1/cluster-policies/{id}",
		requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandleDeleteClusterPolicy(pg)))))
	mux.Handle("DELETE /v1/policy-reports/{id}",
		requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandleDeletePolicyReport(pg)))))

	// Per-asset derived network-rules (flow-matrix P1, Tasks 20 + 21).
	mux.Handle("GET /v1/workloads/{id}/network-rules",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleWorkloadNetworkRules(pg)))))
	mux.Handle("GET /v1/virtual-machines/{id}/network-rules",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleVMNetworkRules(pg)))))

	// Cluster flow matrix (ADR-0036, R2 Task 10) — hand-written handlers.
	// The synthesis endpoint is read-scope but gated inside the handler by
	// flow_matrix_enabled (409 when disabled). Flow references are read for
	// list/export and editor (write) scope for the mutating routes.
	mux.Handle("GET /v1/clusters/{id}/flow-matrix",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleClusterFlowMatrix(pg)))))
	// Flow-matrix extracts (R3 Task 4) — GET-but-audited via the shouldAudit
	// allowlist (SNC ch.8 exfiltration evidence), capped at extractMaxRows.
	mux.Handle("GET /v1/clusters/{id}/flow-matrix/extract",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleFlowMatrixExtract(pg, cfg.extractMaxRows)))))
	mux.Handle("GET /v1/clusters/{id}/flow-matrix/extract.zip",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleFlowMatrixExtractZip(pg, cfg.extractMaxRows)))))
	mux.Handle("GET /v1/clusters/{id}/flow-references",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleListFlowReferences(pg)))))
	mux.Handle("GET /v1/clusters/{id}/flow-references/export",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleExportFlowReferences(pg)))))
	mux.Handle("POST /v1/clusters/{id}/flow-references",
		requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandleCreateFlowReference(pg)))))
	mux.Handle("POST /v1/clusters/{id}/flow-references/import",
		requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandleImportFlowReferences(pg)))))
	mux.Handle("PATCH /v1/flow-references/{id}",
		requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandleUpdateFlowReference(pg)))))
	mux.Handle("DELETE /v1/flow-references/{id}",
		requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandleDeleteFlowReference(pg)))))

	// Endpoint groups (ADR-0036) — admin scope, hand-written. /v1/admin/*
	// GETs are audited automatically by AuditMiddleware (shouldAudit), so the
	// read routes omit the explicit auditWrap, matching /v1/admin/settings.
	mux.Handle("GET /v1/admin/endpoint-groups",
		requireScope(auth.ScopeAdmin)(cloudAuth(api.HandleListEndpointGroups(pg))))
	mux.Handle("GET /v1/admin/endpoint-groups/{id}",
		requireScope(auth.ScopeAdmin)(cloudAuth(api.HandleGetEndpointGroup(pg))))
	mux.Handle("POST /v1/admin/endpoint-groups",
		requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleCreateEndpointGroup(pg)))))
	mux.Handle("PATCH /v1/admin/endpoint-groups/{id}",
		requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleUpdateEndpointGroup(pg)))))
	mux.Handle("DELETE /v1/admin/endpoint-groups/{id}",
		requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleDeleteEndpointGroup(pg)))))

	// Application + ApplicationBlock routes (ADR-0029) — extracted to keep
	// buildHTTPServer within the maintainability-index ceiling.
	mountApplicationRoutes(mux, pg, requireScope, cloudAuth, auditWrap, cfg.extractMaxRows)

	// Extract endpoints — audited via the shouldAudit allowlist (ADR-0024 / SNC ch.8).
	extractAuth := auth.Middleware(pg, cfg.cookiePolicy, cfg.trustedProxies)
	mux.Handle("GET /v1/search/extract",
		requireScope(auth.ScopeRead)(extractAuth(auditWrap(api.HandleSearchExtract(pg, cfg.extractMaxRows)))))
	mux.Handle("GET /v1/search/extract.zip",
		requireScope(auth.ScopeRead)(extractAuth(auditWrap(api.HandleSearchExtractZip(pg, cfg.extractMaxRows)))))
	mux.Handle("GET /v1/eol/extract",
		requireScope(auth.ScopeRead)(extractAuth(auditWrap(api.HandleEolExtract(pg, cfg.extractMaxRows)))))

	loginLimiter := api.NewLoginRateLimiter()
	verifyLimiter := api.NewVerifyRateLimiterWithLimits(cfg.verifyRateLimitRPS, cfg.verifyRateLimitBurst)
	apiServer := api.NewServer(version, pg, cfg.cookiePolicy, oidcProvider, loginLimiter, verifyLimiter)
	apiServer.SetTrustedProxies(cfg.trustedProxies)
	apiServer.SetEnricher(imgVersionsEnricher)
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
		// it from the request context. The workload-unlink detector is
		// innermost (runs last, right before the codegen body decode) so it
		// peeks the body the audit layer has already buffered + restored
		// (ADR-0029 §2.3).
		Middlewares: []api.MiddlewareFunc{
			api.DetectWorkloadUnlinkMiddleware,
			api.AuditMiddleware(pg, "api", cfg.trustedProxies),
			api.AuthMiddleware(pg, cfg.cookiePolicy, cfg.trustedProxies),
		},
	})

	// /v1/auth/verify is reachable only on the mTLS-only ingest listener
	// (ADR-0016 §3). The codegen router registers it on every mux it
	// wires, so 404 it here on the public listener as defence in depth
	// in case an operator runs longue-vue without configuring the ingest
	// listener separately.
	// AsOfMiddleware intercepts GET /v1/{kind}/{id}?as_of= requests and serves
	// point-in-time snapshots from history tables (ADR-0021 Phase 3). It must
	// wrap the entire mux so it runs after auth (which is wired via the
	// HandlerWithOptions Middlewares list) and before the generated router's
	// route-dispatch. The auth middleware is re-applied via the requireReadScope
	// + historyAuth pattern on the /history routes; for the as_of interceptor
	// we rely on the generated router's AuthMiddleware that already ran at the
	// mux level (via HandlerWithOptions Middlewares).
	// asOfAuthMiddleware wraps a terminal handler with read-scope injection + auth,
	// mirroring the requireReadScope(historyAuth(...)) pattern used for /history routes.
	asOfAuthMW := func(h http.Handler) http.Handler {
		return requireReadScope(auth.Middleware(pg, cfg.cookiePolicy, cfg.trustedProxies)(h))
	}
	asOfHandler := api.AsOfMiddleware(pg, asOfAuthMW)(mux)
	publicHandler := blockIngestOnlyPaths(asOfHandler)

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

// mountApplicationRoutes wires the five /v1/application-blocks routes
// and the eight /v1/applications routes (ADR-0029) on the supplied mux.
// Extracted from buildHTTPServer to keep that function under the
// maintainability-index ceiling.
//
// Routing note: GET /v1/applications/by-name/{name} and
// GET /v1/applications/{id}/members are both three-segment patterns with
// a literal at the same position; net/http.ServeMux refuses to register
// both on the same mux (verified empirically — it panics at registration
// with "neither is more specific"). We sidestep that by mounting the
// by-name pattern on a dedicated child mux keyed on the longer literal
// prefix, so the parent dispatcher routes by-name requests through the
// child mux before they reach the {id}-rooted patterns. Mirrors the test
// scaffold in internal/api/application_handlers_test.go.
func mountApplicationRoutes(
	mux *http.ServeMux,
	pg *store.PG,
	requireScope func(scope string) func(http.Handler) http.Handler,
	cloudAuth func(http.Handler) http.Handler,
	auditWrap func(http.Handler) http.Handler,
	extractMaxRows int,
) {
	// Application blocks — no internal route conflicts; mount directly.
	mux.Handle("POST /v1/application-blocks",
		requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandleCreateApplicationBlock(pg)))))
	mux.Handle("GET /v1/application-blocks",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleListApplicationBlocks(pg)))))
	mux.Handle("GET /v1/application-blocks/{id}",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleGetApplicationBlock(pg)))))
	mux.Handle("PATCH /v1/application-blocks/{id}",
		requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandlePatchApplicationBlock(pg)))))
	mux.Handle("DELETE /v1/application-blocks/{id}",
		requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleDeleteApplicationBlock(pg)))))

	// Applications — sub-mux split to dodge the by-name vs {id}/members panic.
	appsByNameMux := http.NewServeMux()
	appsByNameMux.Handle("GET /v1/applications/by-name/{name}",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleGetApplicationByName(pg)))))

	appsRestMux := http.NewServeMux()
	appsRestMux.Handle("POST /v1/applications",
		requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandleCreateApplication(pg)))))
	appsRestMux.Handle("GET /v1/applications",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleListApplications(pg)))))
	// Bulk extracts (ADR-0029 §2.1). The ".csv" / ".json" literal segments
	// are more specific than the {id} wildcard, so net/http.ServeMux routes
	// them without colliding with GET /v1/applications/{id}.
	appsRestMux.Handle("GET /v1/applications/extract.csv",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleExtractApplicationsCSV(pg, extractMaxRows)))))
	appsRestMux.Handle("GET /v1/applications/extract.json",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleExtractApplicationsJSON(pg, extractMaxRows)))))
	appsRestMux.Handle("GET /v1/applications/{id}",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleGetApplication(pg)))))
	appsRestMux.Handle("PATCH /v1/applications/{id}",
		requireScope(auth.ScopeWrite)(cloudAuth(auditWrap(api.HandlePatchApplication(pg)))))
	appsRestMux.Handle("DELETE /v1/applications/{id}",
		requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleDeleteApplication(pg)))))
	appsRestMux.Handle("GET /v1/applications/{id}/members",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleListApplicationMembers(pg)))))
	appsRestMux.Handle("GET /v1/applications/{id}/eol",
		requireScope(auth.ScopeRead)(cloudAuth(auditWrap(api.HandleGetApplicationEOL(pg)))))

	// Parent-mux dispatch: longer literal prefix wins, so by-name/...
	// routes through appsByNameMux first; everything else falls to
	// appsRestMux. The trailing-slash subtree pattern covers nested paths,
	// the bare pattern covers the collection root.
	mux.Handle("/v1/applications/by-name/", appsByNameMux)
	mux.Handle("/v1/applications/", appsRestMux)
	mux.Handle("/v1/applications", appsRestMux)
}

// blockIngestOnlyPaths 404s requests to paths that should never appear on
// longue-vue's public listener. Today that's only POST /v1/auth/verify
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
	verifyLimiter := api.NewVerifyRateLimiterWithLimits(cfg.verifyRateLimitRPS, cfg.verifyRateLimitBurst)
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
	data, err := os.ReadFile(path) // #nosec G304 -- operator-supplied path; fail loud if missing
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
// changes — sufficient for longue-vue-side (cert rotation is infrequent).
// The gateway binary (cmd/longue-vue-ingest-gw) gets an fsnotify-driven
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
// Increments longue_vue_ingest_listener_client_cert_failures_total{reason="cn_not_allowed"}
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
// both gracefully. ingestSrv may be nil — longue-vue treats the ingest listener
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
		slog.Info("longue-vue listening",
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
			slog.Info("longue-vue ingest listener starting",
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
	slog.Info("longue-vue stopped cleanly")
	return nil
}

// collectorClusterConfig is one entry in LONGUE_VUE_COLLECTOR_CLUSTERS.
// Kubeconfig may be empty to mean "use in-cluster config" (typically when
// longue-vue runs inside one of the target clusters).
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
	// kubeQPS / kubeBurst override the client-go rate limiter. <=0 = use
	// collector package defaults. Override via
	// LONGUE_VUE_COLLECTOR_KUBE_QPS / _KUBE_BURST. Applies to every cluster
	// declared in LONGUE_VUE_COLLECTOR_CLUSTERS.
	kubeQPS   float32
	kubeBurst int
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
	kubeQPS, err := parseFloat32Env("LONGUE_VUE_COLLECTOR_KUBE_QPS", 0)
	if err != nil {
		return collectorEnvConfig{}, err
	}
	kubeBurst, err := parseIntEnv("LONGUE_VUE_COLLECTOR_KUBE_BURST", 0)
	if err != nil {
		return collectorEnvConfig{}, err
	}
	return collectorEnvConfig{
		interval:     interval,
		fetchTimeout: fetchTimeout,
		reconcile:    reconcile,
		kubeQPS:      kubeQPS,
		kubeBurst:    kubeBurst,
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
		source, err := collector.NewKubeClientWithLimits(cfg.Kubeconfig, envCfg.kubeQPS, envCfg.kubeBurst)
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
		"\n  LONGUE-VUE FIRST-RUN BOOTSTRAP" +
		"\n  A default admin user has been created:" +
		"\n    username: admin" +
		"\n    password: " + password +
		"\n    source:   " + source +
		"\n  This account MUST rotate its password on first login." +
		"\n" + banner)
	return nil
}

// rescueLockedAdminIfNeeded recovers from the situation where every
// admin is locked or disabled and no operator can log in. Reads the
// new password from LONGUE_VUE_ADMIN_RESCUE_PASSWORD; if unset, no-op.
//
// Triggers when COUNT(active+unlocked admins) == 0. Picks the most
// recently active admin, resets their password, clears lockout +
// disabled state, forces must_change_password=true, invalidates all
// sessions for the user, writes a system audit event, and prints a
// loud banner.
func rescueLockedAdminIfNeeded(ctx context.Context, s *store.PG) error {
	password := os.Getenv("LONGUE_VUE_ADMIN_RESCUE_PASSWORD")
	if password == "" {
		return nil
	}

	usable, err := s.CountActiveUnlockedAdmins(ctx)
	if err != nil {
		return fmt.Errorf("count active+unlocked admins: %w", err)
	}
	if usable > 0 {
		return nil
	}

	target, err := s.PickRescueTarget(ctx)
	if err != nil {
		return fmt.Errorf("pick rescue target: %w", err)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash rescue password: %w", err)
	}

	if err := s.RescueAdmin(ctx, *target.Id, hash); err != nil {
		return fmt.Errorf("rescue admin %s: %w", target.Username, err)
	}

	if err := s.InsertAuditEvent(ctx, api.AuditEventInsert{
		ID:           uuid.New(),
		OccurredAt:   time.Now().UTC(),
		ActorKind:    "system",
		Action:       "auth.admin_rescue",
		ResourceType: "user",
		ResourceID:   target.Id.String(),
		Source:       "system",
	}); err != nil {
		// Do not fail the boot if the audit insert fails -- recovery
		// already happened. Log loudly instead.
		slog.Error("audit insert for admin rescue failed", slog.Any("error", err))
	}

	banner := strings.Repeat("=", 72)
	slog.Error("\n"+banner+
		"\n  LONGUE-VUE ADMIN RESCUE TRIGGERED"+
		"\n  Username:           "+target.Username+
		"\n  Password reset to:  $LONGUE_VUE_ADMIN_RESCUE_PASSWORD"+
		"\n  must_change_password forced -- rotate immediately on first login."+
		"\n"+banner,
		slog.Any("user_id", target.Id),
		slog.String("username", target.Username),
	)
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

func parseFloat32Env(key string, fallback float32) (float32, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	f, err := strconv.ParseFloat(v, 32)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q: %w", key, v, err)
	}
	return float32(f), nil
}

// seedFlowMatrixSetting seeds the `flow_matrix_enabled` DB setting from the
// LONGUE_VUE_FLOW_MATRIX_ENABLED env var when explicitly set, mirroring the
// seed-once semantics of the EOL / image-versions toggles. Unlike those
// toggles there is no enricher goroutine to host the seed (the flow-matrix
// behavior is gated elsewhere), so the seed lives in its own boot hook.
func seedFlowMatrixSetting(ctx context.Context, s api.Store) error {
	envVal := os.Getenv("LONGUE_VUE_FLOW_MATRIX_ENABLED")
	if envVal == "" {
		return nil
	}
	enabled, err := strconv.ParseBool(envVal)
	if err != nil {
		return fmt.Errorf("parse LONGUE_VUE_FLOW_MATRIX_ENABLED=%q: %w", envVal, err)
	}
	if _, err := s.UpdateSettings(ctx, api.SettingsPatch{FlowMatrixEnabled: &enabled}); err != nil {
		slog.Warn("flow matrix: failed to seed settings from env", slog.Any("error", err))
	}
	return nil
}

// seedPoliciesSetting seeds the `policies_enabled` DB setting from the
// LONGUE_VUE_POLICIES_ENABLED env var when explicitly set, mirroring the
// seed-once semantics of the flow-matrix / EOL / MCP toggles (ADR-0043 IMP-002).
func seedPoliciesSetting(ctx context.Context, s api.Store) error {
	envVal := os.Getenv("LONGUE_VUE_POLICIES_ENABLED")
	if envVal == "" {
		return nil
	}
	enabled, err := strconv.ParseBool(envVal)
	if err != nil {
		return fmt.Errorf("parse LONGUE_VUE_POLICIES_ENABLED=%q: %w", envVal, err)
	}
	if _, err := s.UpdateSettings(ctx, api.SettingsPatch{PoliciesEnabled: &enabled}); err != nil {
		slog.Warn("policies: failed to seed settings from env", slog.Any("error", err))
	}
	return nil
}

// errClusterStaleDaysInvalid is the static sentinel wrapped into the
// boot-failure error returned by seedClusterStaleSetting (err113).
var errClusterStaleDaysInvalid = errors.New("must be an integer >= 0")

// seedClusterStaleSetting seeds `cluster_stale_after_days` from the
// LONGUE_VUE_CLUSTER_STALE_AFTER_DAYS env var when explicitly set,
// mirroring seedFlowMatrixSetting. 0 disables the derived stale status.
func seedClusterStaleSetting(ctx context.Context, s api.Store) error {
	envVal := os.Getenv("LONGUE_VUE_CLUSTER_STALE_AFTER_DAYS")
	if envVal == "" {
		return nil
	}
	days, err := strconv.Atoi(envVal)
	if err != nil || days < 0 {
		return fmt.Errorf("parse LONGUE_VUE_CLUSTER_STALE_AFTER_DAYS=%q: %w", envVal, errClusterStaleDaysInvalid)
	}
	if _, err := s.UpdateSettings(ctx, api.SettingsPatch{ClusterStaleAfterDays: &days}); err != nil {
		slog.Warn("cluster staleness: failed to seed settings from env", slog.Any("error", err))
	}
	return nil
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

// maybeStartMetricsRefresh spawns the periodic goroutine that recomputes
// store-derived Prometheus gauges — currently the longue_vue_dict_coverage
// gauge (ADR-0029 §6). Mirrors the EOL enricher's lifecycle: always starts,
// runs once immediately, then on each tick. Returns a drain function the
// caller defers. Interval via LONGUE_VUE_METRICS_REFRESH_INTERVAL (default
// 60s).
func maybeStartMetricsRefresh(ctx context.Context, s *store.PG) (func(), error) {
	interval, err := parseDurationEnv("LONGUE_VUE_METRICS_REFRESH_INTERVAL", 60*time.Second)
	if err != nil {
		return nil, err
	}
	refresher := metricsrefresh.New(s, interval)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := refresher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("metrics refresher exited with error", slog.String("error", err.Error()))
		}
	}()

	slog.Info("metrics refresher goroutine started", slog.String("interval", interval.String()))
	return wg.Wait, nil
}

// maybeStartImageVersionsEnricher spawns the image-versions enrichment goroutine.
// The goroutine always starts; actual enrichment is gated by the
// `image_versions_enabled` setting in the database (toggled by admins via the UI).
// LONGUE_VUE_IMAGE_VERSIONS_ENABLED seeds the DB setting on first boot when present.
// Returns the enricher (for the /refresh handler in Task 15) and a drain function the caller defers.
func maybeStartImageVersionsEnricher(ctx context.Context, s api.Store) (*imageversions.Enricher, func(), error) {
	// Seed the DB setting from env var when explicitly set.
	if envVal := os.Getenv("LONGUE_VUE_IMAGE_VERSIONS_ENABLED"); envVal != "" {
		enabled, err := strconv.ParseBool(envVal)
		if err != nil {
			return nil, nil, fmt.Errorf("parse LONGUE_VUE_IMAGE_VERSIONS_ENABLED=%q: %w", envVal, err)
		}
		if _, err := s.UpdateSettings(ctx, api.SettingsPatch{ImageVersionsEnabled: &enabled}); err != nil {
			slog.Warn("imageversions enricher: failed to seed settings from env", slog.Any("error", err))
		}
	}

	interval, err := parseDurationEnv("LONGUE_VUE_IMAGE_VERSIONS_INTERVAL", 24*time.Hour)
	if err != nil {
		return nil, nil, err
	}

	ivMetrics := imageversions.NewMetrics(metrics.Registry)
	client := registry.NewClientWithObserver(ivMetrics)
	resolver := &mirrorresolve.HTTPResolver{
		Lookup:  imageversions.NewStoreMirrorLookup(s),
		Origins: imageversions.NewStoreOriginLookup(s),
		Metrics: imageversions.NewObserver(ivMetrics),
	}
	enricher := imageversions.NewEnricherWithResolver(s, client, resolver, interval, ivMetrics)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := enricher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("imageversions enricher exited with error", slog.String("error", err.Error()))
		}
	}()

	slog.Info("imageversions enricher goroutine started (actual enrichment gated by DB setting)",
		slog.String("interval", interval.String()),
	)

	return enricher, wg.Wait, nil
}

// mcpTokenStore is the narrow store interface needed by buildMCPAuthFn.
// Extracted so the function can be unit-tested without a real *store.PG.
type mcpTokenStore interface {
	GetActiveTokenByPrefix(ctx context.Context, prefix string) (auth.APIToken, error)
}

// buildMCPAuthFn constructs the AuthFunc used by the MCP server to verify
// bearer tokens. It is extracted from maybeStartMCPServer to enable unit
// testing without a real database (MED-04).
func buildMCPAuthFn(tokenStore mcpTokenStore, cache *argmcp.AuthCache) argmcp.AuthFunc {
	return func(ctx context.Context, rawToken string) (*argmcp.Caller, error) {
		prefix, full, err := auth.ParseToken(rawToken)
		if err != nil {
			slog.Warn("mcp auth failed: invalid token format", slog.Any("error", err))
			return nil, errMCPAuthFailed
		}

		// Check bounded LRU cache — skip argon2id if recently verified.
		if caller, ok := cache.Get(prefix, full); ok {
			return caller, nil
		}

		// Full verification: DB lookup + argon2id.
		tok, err := tokenStore.GetActiveTokenByPrefix(ctx, prefix)
		if err != nil {
			slog.Warn("mcp auth failed: token lookup error", slog.Any("error", err))
			return nil, errMCPAuthFailed
		}
		if verr := auth.VerifyPassword(full, tok.Hash); verr != nil {
			slog.Warn("mcp auth failed: token verification error", slog.Any("error", verr))
			return nil, errMCPAuthFailed
		}
		// Per ADR-0015 §5, reject any token carrying vm-collector scope
		// (a mis-issued admin+vm-collector token must NOT access MCP).
		if !mcpScopeAllowed(tok.Scopes) {
			slog.Warn("mcp auth failed: token scope not permitted for MCP access")
			return nil, errMCPAuthFailed
		}

		tokenID := tok.ID
		userID := tok.CreatedByUserID
		caller := &argmcp.Caller{
			TokenID: &tokenID,
			Name:    tok.Name,
			UserID:  &userID,
			Scopes:  tok.Scopes,
		}

		// Cache the verified result.
		cache.Put(prefix, full, caller)
		return caller, nil
	}
}

// mcpScopeAllowed checks whether a token's scopes permit MCP access.
// Returns true only if the token has the read scope (or admin scope,
// which implies read per ADR-0007) AND does not carry the vm-collector
// scope. Per ADR-0015 §5, vm-collector tokens must never access CMDB
// data through MCP, even if mis-issued with additional scopes.
func mcpScopeAllowed(scopes []string) bool {
	caller := &auth.Caller{Scopes: scopes}
	// Check for vm-collector scope first — deny if present (ADR-0015 §5).
	if caller.HasScope(auth.ScopeVMCollector) {
		return false
	}
	// Require read scope (admin implies read per ADR-0007).
	return caller.HasScope(auth.ScopeRead)
}

// maybeStartMCPServer spawns the MCP server goroutine (ADR-0014).
// The goroutine always starts; tool calls are gated by the `mcp_enabled`
// setting in the database (toggled by admins via the UI).
// LONGUE_VUE_MCP_ENABLED seeds the DB setting on first boot when present.
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
	addr := envOr("LONGUE_VUE_MCP_ADDR", "127.0.0.1:8090")
	token := os.Getenv("LONGUE_VUE_MCP_TOKEN")

	// For SSE transport, validate bearer tokens on every tool call using
	// the same auth store that the REST API uses.
	// Argon2id verification is expensive (~100-500ms, 64 MiB), so we use a
	// bounded LRU cache (cap 1024, TTL 30 s) to amortise cost across a
	// typical AI conversation's tool calls. 30 s is deliberate: short enough
	// that a revoked token is unusable within a typical incident-response
	// window even without the revocation channel, long enough to cover a full
	// burst of MCP tool calls in one conversation turn (HIGH-01, HIGH-03,
	// MED-03 — audit 2026-05-04).
	//
	// cap=1024 and ttl=30s are intentionally not configurable in this pass.
	authCache := argmcp.NewAuthCache(1024, 30*time.Second)

	// Subscribe to token revocations: invalidate the MCP cache immediately so
	// a revoked token cannot continue making MCP calls (HIGH-01). The goroutine
	// exits when the channel is garbage-collected at shutdown.
	go func() {
		for prefix := range s.RevocationChan() {
			authCache.Invalidate(prefix)
		}
	}()

	// authFn is used for SSE per-call verification and for stdio one-shot
	// startup verification (MED-01). Always built so verifyStdioToken can
	// call it when LONGUE_VUE_MCP_TOKEN is set.
	authFn := buildMCPAuthFn(s, authCache)

	mcpCertFile := os.Getenv("LONGUE_VUE_MCP_TLS_CERT")
	mcpKeyFile := os.Getenv("LONGUE_VUE_MCP_TLS_KEY")
	allowPlain := os.Getenv("LONGUE_VUE_MCP_ALLOW_PLAINTEXT") == "true"

	var getCert func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	if mcpCertFile != "" && mcpKeyFile != "" {
		var err error
		getCert, err = newCertReloader(mcpCertFile, mcpKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load mcp listener cert: %w", err)
		}
		slog.Info("mcp sse listener tls enabled", slog.String("cert", mcpCertFile))
	}

	// 30 rps, burst 60 — generous for an interactive AI conversation,
	// tight enough to prevent pathological fanout (HIGH-02).
	limiter := argmcp.NewRateLimiter(30, 60)

	cfg := &argmcp.Config{
		Transport:         transport,
		Addr:              addr,
		Token:             token,
		Auth:              authFn,
		Recorder:          s,
		TLSGetCertificate: getCert,
		AllowPlaintext:    allowPlain,
		RateLimiter:       limiter,
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
