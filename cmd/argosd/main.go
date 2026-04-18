// Command argosd is the Argos CMDB daemon entry point.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/sthalbert/argos/internal/api"
	"github.com/sthalbert/argos/internal/collector"
	"github.com/sthalbert/argos/internal/store"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

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
	tokenStore, err := loadTokenStore()
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

	if err := maybeStartCollector(rootCtx, pg); err != nil {
		return err
	}

	srv := &http.Server{
		Addr: addr,
		Handler: api.HandlerWithOptions(api.NewServer(version, pg), api.StdHTTPServerOptions{
			Middlewares: []api.MiddlewareFunc{api.BearerAuth(tokenStore)},
		}),
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

// maybeStartCollector spawns the Kubernetes collector goroutine when
// ARGOS_COLLECTOR_ENABLED is truthy. Disabled by default.
func maybeStartCollector(ctx context.Context, s api.Store) error {
	enabled, err := parseBoolEnv("ARGOS_COLLECTOR_ENABLED", false)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}

	clusterName := os.Getenv("ARGOS_CLUSTER_NAME")
	if clusterName == "" {
		return errors.New("ARGOS_CLUSTER_NAME is required when ARGOS_COLLECTOR_ENABLED=true")
	}
	interval, err := parseDurationEnv("ARGOS_COLLECTOR_INTERVAL", 5*time.Minute)
	if err != nil {
		return err
	}
	fetchTimeout, err := parseDurationEnv("ARGOS_COLLECTOR_FETCH_TIMEOUT", 10*time.Second)
	if err != nil {
		return err
	}
	reconcile, err := parseBoolEnv("ARGOS_COLLECTOR_RECONCILE", true)
	if err != nil {
		return err
	}
	kubeconfig := os.Getenv("ARGOS_KUBECONFIG")

	source, err := collector.NewKubeClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("init kube client: %w", err)
	}
	coll := collector.New(s, source, clusterName, interval, fetchTimeout, reconcile)

	go func() {
		if err := coll.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("collector exited with error", "error", err)
		}
	}()
	return nil
}

// loadTokenStore merges ARGOS_API_TOKEN (a convenience single admin token)
// and ARGOS_API_TOKENS (a JSON array of full scoped tokens) into a validated
// TokenStore. Returns an error if neither env var is set.
func loadTokenStore() (*api.TokenStore, error) {
	var tokens []api.ScopedToken

	if adminToken := os.Getenv("ARGOS_API_TOKEN"); adminToken != "" {
		tokens = append(tokens, api.ScopedToken{
			Name:   "env:ARGOS_API_TOKEN",
			Token:  adminToken,
			Scopes: []string{api.ScopeAdmin},
		})
	}

	parsed, err := api.ParseTokensJSON(os.Getenv("ARGOS_API_TOKENS"))
	if err != nil {
		return nil, fmt.Errorf("ARGOS_API_TOKENS: %w", err)
	}
	for i, p := range parsed {
		if p.Name == "" {
			p.Name = fmt.Sprintf("env:ARGOS_API_TOKENS[%d]", i)
		}
		tokens = append(tokens, p)
	}

	if len(tokens) == 0 {
		return nil, api.ErrNoTokensConfigured
	}

	store, err := api.NewTokenStore(tokens)
	if err != nil {
		return nil, fmt.Errorf("build token store: %w", err)
	}
	slog.Info("auth tokens loaded", "count", store.Len())
	return store, nil
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
