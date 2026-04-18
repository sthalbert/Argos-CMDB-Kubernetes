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
	token := os.Getenv("ARGOS_API_TOKEN")
	if token == "" {
		return errors.New("ARGOS_API_TOKEN is required")
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
		Addr:              addr,
		Handler:           api.BearerAuth(token)(api.Handler(api.NewServer(version, pg))),
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
	kubeconfig := os.Getenv("ARGOS_KUBECONFIG")

	fetcher, err := collector.NewKubeVersionFetcher(kubeconfig)
	if err != nil {
		return fmt.Errorf("init kube fetcher: %w", err)
	}
	coll := collector.New(s, fetcher, clusterName, interval, fetchTimeout)

	go func() {
		if err := coll.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("collector exited with error", "error", err)
		}
	}()
	return nil
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
