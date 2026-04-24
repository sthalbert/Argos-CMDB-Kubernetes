// Command argosd is the Argos CMDB daemon entry point.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sthalbert/argos/internal/api"
	"github.com/sthalbert/argos/internal/auth"
	"github.com/sthalbert/argos/internal/collector"
	"github.com/sthalbert/argos/internal/eol"
	"github.com/sthalbert/argos/internal/impact"
	"github.com/sthalbert/argos/internal/metrics"
	"github.com/sthalbert/argos/internal/store"
	"github.com/sthalbert/argos/ui"
)

// version is set at build time via -ldflags.
var version = "dev"

// Sentinel errors for configuration validation.
var (
	errDatabaseURLRequired     = errors.New("ARGOS_DATABASE_URL is required")
	errLegacyTokensUnsupported = errors.New("ARGOS_API_TOKEN / ARGOS_API_TOKENS are no longer supported; " +
		"the bootstrap admin password is printed in the startup log on first run, " +
		"and machine tokens are issued in the admin panel — see ADR-0007")
	errCollectorClustersEmpty = errors.New("ARGOS_COLLECTOR_CLUSTERS is empty")
	errClusterNameRequired    = errors.New("ARGOS_COLLECTOR_CLUSTERS entry: name is required")
	errDuplicateClusterName   = errors.New("ARGOS_COLLECTOR_CLUSTERS entry: duplicate name")
	errNoCollectorClusters    = errors.New("ARGOS_COLLECTOR_CLUSTERS or ARGOS_CLUSTER_NAME must be set when ARGOS_COLLECTOR_ENABLED=true")
	errInvalidCookiePolicy    = errors.New("ARGOS_SESSION_SECURE_COOKIE must be auto / always / never")
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
}

// loadRunConfig reads and validates all configuration from the environment.
func loadRunConfig() (runConfig, error) {
	dsn := os.Getenv("ARGOS_DATABASE_URL")
	if dsn == "" {
		return runConfig{}, errDatabaseURLRequired
	}
	// Per ADR-0007: env-var token bootstrap is removed. Fail loudly so
	// operators migrating from v0 know to read the admin password from
	// the startup log instead.
	if os.Getenv("ARGOS_API_TOKEN") != "" || os.Getenv("ARGOS_API_TOKENS") != "" {
		return runConfig{}, errLegacyTokensUnsupported
	}
	cookiePolicy, err := parseCookiePolicy()
	if err != nil {
		return runConfig{}, err
	}
	shutdownTimeout, err := parseDurationEnv("ARGOS_SHUTDOWN_TIMEOUT", 15*time.Second)
	if err != nil {
		return runConfig{}, err
	}
	autoMigrate, err := parseBoolEnv("ARGOS_AUTO_MIGRATE", true)
	if err != nil {
		return runConfig{}, err
	}

	return runConfig{
		addr:            envOr("ARGOS_ADDR", ":8080"),
		dsn:             dsn,
		cookiePolicy:    cookiePolicy,
		oidcCfg:         loadOIDCConfig(),
		shutdownTimeout: shutdownTimeout,
		autoMigrate:     autoMigrate,
	}, nil
}

func run() error {
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

	srv := buildHTTPServer(&cfg, pg, oidcProvider)

	return serveAndShutdown(rootCtx, srv, cfg.shutdownTimeout)
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
func buildHTTPServer(cfg *runConfig, pg *store.PG, oidcProvider *auth.OIDCProvider) *http.Server {
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
	settingsAuth := auth.Middleware(pg, cfg.cookiePolicy)
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
	impactAuth := auth.Middleware(pg, cfg.cookiePolicy)
	mux.Handle("GET /v1/impact/{entity_type}/{id}", requireReadScope(impactAuth(impact.HandleImpact(pg))))

	strict := api.NewStrictHandlerWithOptions(
		api.NewServer(version, pg, cfg.cookiePolicy, oidcProvider),
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
			api.AuditMiddleware(pg),
			api.AuthMiddleware(pg, cfg.cookiePolicy),
		},
	})

	return &http.Server{
		Addr:              cfg.addr,
		Handler:           metrics.InstrumentHandler(api.SecurityHeadersMiddleware(http.MaxBytesHandler(mux, 1<<20))),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// serveAndShutdown starts the HTTP server, waits for a shutdown signal, and
// drains gracefully.
func serveAndShutdown(rootCtx context.Context, srv *http.Server, shutdownTimeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() {
		slog.Info("argosd listening",
			slog.String("addr", srv.Addr),
			slog.String("version", version),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-rootCtx.Done():
		slog.Info("shutdown signal received, draining",
			slog.String("timeout", shutdownTimeout.String()),
		)
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	//nolint:contextcheck // detached context — parent is already cancelled by shutdown signal
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	slog.Info("argosd stopped cleanly")
	return nil
}

// collectorClusterConfig is one entry in ARGOS_COLLECTOR_CLUSTERS.
// Kubeconfig may be empty to mean "use in-cluster config" (typically when
// argosd runs inside one of the target clusters).
type collectorClusterConfig struct {
	Name       string `json:"name"`
	Kubeconfig string `json:"kubeconfig"`
}

// loadCollectorClusters resolves the list of target clusters from env per
// ADR-0005. Precedence:
//   - ARGOS_COLLECTOR_CLUSTERS (JSON array of {name, kubeconfig}): primary.
//   - ARGOS_CLUSTER_NAME + ARGOS_KUBECONFIG: legacy single-cluster shortcut.
//
// Returns an error if neither form is set or if the JSON is malformed / has
// empty or duplicate names.
func loadCollectorClusters() ([]collectorClusterConfig, error) {
	if raw := os.Getenv("ARGOS_COLLECTOR_CLUSTERS"); raw != "" {
		var parsed []collectorClusterConfig
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return nil, fmt.Errorf("parse ARGOS_COLLECTOR_CLUSTERS: %w", err)
		}
		if len(parsed) == 0 {
			return nil, errCollectorClustersEmpty
		}
		seen := make(map[string]struct{}, len(parsed))
		for i, c := range parsed {
			if c.Name == "" {
				return nil, fmt.Errorf("ARGOS_COLLECTOR_CLUSTERS[%d]: %w", i, errClusterNameRequired)
			}
			if _, dup := seen[c.Name]; dup {
				return nil, fmt.Errorf("ARGOS_COLLECTOR_CLUSTERS[%d] %q: %w", i, c.Name, errDuplicateClusterName)
			}
			seen[c.Name] = struct{}{}
		}
		return parsed, nil
	}

	if name := os.Getenv("ARGOS_CLUSTER_NAME"); name != "" {
		return []collectorClusterConfig{{
			Name:       name,
			Kubeconfig: os.Getenv("ARGOS_KUBECONFIG"),
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
	interval, err := parseDurationEnv("ARGOS_COLLECTOR_INTERVAL", 5*time.Minute)
	if err != nil {
		return collectorEnvConfig{}, err
	}
	fetchTimeout, err := parseDurationEnv("ARGOS_COLLECTOR_FETCH_TIMEOUT", 10*time.Second)
	if err != nil {
		return collectorEnvConfig{}, err
	}
	reconcile, err := parseBoolEnv("ARGOS_COLLECTOR_RECONCILE", true)
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
// in ARGOS_COLLECTOR_CLUSTERS (or per the legacy single-cluster env vars)
// when ARGOS_COLLECTOR_ENABLED is truthy. Returns a drain function the
// caller defers so main.run blocks on collector shutdown before returning.
// When the collector is disabled the drain is a no-op.
func maybeStartCollectors(ctx context.Context, s api.Store) (func(), error) {
	enabled, err := parseBoolEnv("ARGOS_COLLECTOR_ENABLED", false)
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
//   - ARGOS_BOOTSTRAP_ADMIN_PASSWORD env var — operators who want a
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

	password := os.Getenv("ARGOS_BOOTSTRAP_ADMIN_PASSWORD")
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
	source := "ARGOS_BOOTSTRAP_ADMIN_PASSWORD"
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

// loadOIDCConfig reads the ARGOS_OIDC_* env vars. Returns a zero-value
// config (Issuer == "") when OIDC is not configured; NewOIDCProvider
// treats that as "disabled". Validation happens in NewOIDCProvider.
func loadOIDCConfig() auth.OIDCConfig {
	cfg := auth.OIDCConfig{
		Issuer:       os.Getenv("ARGOS_OIDC_ISSUER"),
		ClientID:     os.Getenv("ARGOS_OIDC_CLIENT_ID"),
		ClientSecret: os.Getenv("ARGOS_OIDC_CLIENT_SECRET"),
		RedirectURL:  os.Getenv("ARGOS_OIDC_REDIRECT_URL"),
		Label:        os.Getenv("ARGOS_OIDC_LABEL"),
	}
	if raw := os.Getenv("ARGOS_OIDC_SCOPES"); raw != "" {
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
	switch strings.ToLower(envOr("ARGOS_SESSION_SECURE_COOKIE", "auto")) {
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
// ARGOS_EOL_ENABLED seeds the DB setting on first boot when present.
// Returns a drain function the caller defers.
func maybeStartEOLEnricher(ctx context.Context, s api.Store) (func(), error) {
	// Seed the DB setting from env var when explicitly set.
	if envVal := os.Getenv("ARGOS_EOL_ENABLED"); envVal != "" {
		enabled, err := strconv.ParseBool(envVal)
		if err != nil {
			return nil, fmt.Errorf("parse ARGOS_EOL_ENABLED=%q: %w", envVal, err)
		}
		if _, err := s.UpdateSettings(ctx, api.SettingsPatch{EOLEnabled: &enabled}); err != nil {
			slog.Warn("eol enricher: failed to seed settings from env", slog.Any("error", err))
		}
	}

	interval, err := parseDurationEnv("ARGOS_EOL_INTERVAL", 2*time.Minute)
	if err != nil {
		return nil, err
	}
	approachingDays, err := parseIntEnv("ARGOS_EOL_APPROACHING_DAYS", 90)
	if err != nil {
		return nil, err
	}
	baseURL := envOr("ARGOS_EOL_BASE_URL", "https://endoflife.date")

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
