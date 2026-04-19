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
	"github.com/sthalbert/argos/internal/metrics"
	"github.com/sthalbert/argos/internal/store"
	"github.com/sthalbert/argos/ui"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	metrics.SetBuildInfo(version)

	if err := run(); err != nil {
		slog.Error("argosd exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	addr := envOr("ARGOS_ADDR", ":8080")
	dsn := os.Getenv("ARGOS_DATABASE_URL")
	if dsn == "" {
		return errors.New("ARGOS_DATABASE_URL is required")
	}
	// Per ADR-0007: env-var token bootstrap is removed. Fail loudly so
	// operators migrating from v0 know to read the admin password from
	// the startup log instead.
	if os.Getenv("ARGOS_API_TOKEN") != "" || os.Getenv("ARGOS_API_TOKENS") != "" {
		return errors.New("ARGOS_API_TOKEN / ARGOS_API_TOKENS are no longer supported; " +
			"the bootstrap admin password is printed in the startup log on first run, " +
			"and machine tokens are issued in the admin panel — see ADR-0007")
	}
	cookiePolicy, err := parseCookiePolicy()
	if err != nil {
		return err
	}
	shutdownTimeout, err := parseDurationEnv("ARGOS_SHUTDOWN_TIMEOUT", 15*time.Second)
	if err != nil {
		return err
	}
	autoMigrate, err := parseBoolEnv("ARGOS_AUTO_MIGRATE", true)
	if err != nil {
		return err
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pg, err := store.Open(rootCtx, dsn)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer pg.Close()

	if autoMigrate {
		if err := pg.Migrate(rootCtx); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		slog.Info("migrations applied")
	}

	if err := bootstrapAdminIfNeeded(rootCtx, pg); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}

	drainCollectors, err := maybeStartCollectors(rootCtx, pg)
	if err != nil {
		return err
	}
	defer drainCollectors()

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler())
	// SPA served unauthenticated under /ui/; the bundle is static and the
	// API calls it makes from the browser carry their own bearer token.
	mux.Handle("/ui/", http.StripPrefix("/ui", ui.Handler()))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusFound)
	})
	api.HandlerWithOptions(api.NewServer(version, pg, cookiePolicy), api.StdHTTPServerOptions{
		BaseRouter:  mux,
		Middlewares: []api.MiddlewareFunc{api.AuthMiddleware(pg, cookiePolicy)},
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           metrics.InstrumentHandler(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("argosd listening", "addr", addr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-rootCtx.Done():
		slog.Info("shutdown signal received, draining", "timeout", shutdownTimeout)
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
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
			return nil, errors.New("ARGOS_COLLECTOR_CLUSTERS is empty")
		}
		seen := make(map[string]struct{}, len(parsed))
		for i, c := range parsed {
			if c.Name == "" {
				return nil, fmt.Errorf("ARGOS_COLLECTOR_CLUSTERS[%d]: name is required", i)
			}
			if _, dup := seen[c.Name]; dup {
				return nil, fmt.Errorf("ARGOS_COLLECTOR_CLUSTERS[%d] %q: duplicate name", i, c.Name)
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

	return nil, errors.New("ARGOS_COLLECTOR_CLUSTERS or ARGOS_CLUSTER_NAME must be set when ARGOS_COLLECTOR_ENABLED=true")
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
	interval, err := parseDurationEnv("ARGOS_COLLECTOR_INTERVAL", 5*time.Minute)
	if err != nil {
		return nil, err
	}
	fetchTimeout, err := parseDurationEnv("ARGOS_COLLECTOR_FETCH_TIMEOUT", 10*time.Second)
	if err != nil {
		return nil, err
	}
	reconcile, err := parseBoolEnv("ARGOS_COLLECTOR_RECONCILE", true)
	if err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	for _, cfg := range clusters {
		source, err := collector.NewKubeClient(cfg.Kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("init kube client for cluster %q: %w", cfg.Name, err)
		}
		coll := collector.New(s, source, cfg.Name, interval, fetchTimeout, reconcile)
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if err := coll.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("collector exited with error", "error", err, "cluster_name", name)
			}
		}(cfg.Name)
	}
	slog.Info("collectors started", "count", len(clusters))

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
		return err
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
		return err
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

func parseCookiePolicy() (auth.SecureCookiePolicy, error) {
	switch strings.ToLower(envOr("ARGOS_SESSION_SECURE_COOKIE", "auto")) {
	case "auto":
		return auth.SecureAuto, nil
	case "always", "true", "yes":
		return auth.SecureAlways, nil
	case "never", "false", "no":
		return auth.SecureNever, nil
	default:
		return 0, errors.New("ARGOS_SESSION_SECURE_COOKIE must be auto / always / never")
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
