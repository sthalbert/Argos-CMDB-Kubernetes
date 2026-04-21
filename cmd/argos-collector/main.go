// Command argos-collector is the push-mode Kubernetes inventory collector.
// It runs inside an air-gapped cluster, polls the local Kubernetes API,
// and pushes observations to a remote argosd instance over HTTPS. See
// ADR-0009 for the design rationale.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sthalbert/argos/internal/collector"
	"github.com/sthalbert/argos/internal/collector/apiclient"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("argos-collector exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	slog.Info("argos-collector starting", "version", version)

	// Required env vars.
	serverURL := os.Getenv("ARGOS_SERVER_URL")
	if serverURL == "" {
		return errors.New("ARGOS_SERVER_URL is required")
	}
	token := os.Getenv("ARGOS_API_TOKEN")
	if token == "" {
		return errors.New("ARGOS_API_TOKEN is required")
	}
	clusterName := os.Getenv("ARGOS_CLUSTER_NAME")
	if clusterName == "" {
		return errors.New("ARGOS_CLUSTER_NAME is required")
	}

	// Optional env vars.
	kubeconfig := os.Getenv("ARGOS_KUBECONFIG")
	interval, err := parseDurationEnv("ARGOS_COLLECTOR_INTERVAL", 5*time.Minute)
	if err != nil {
		return err
	}
	fetchTimeout, err := parseDurationEnv("ARGOS_COLLECTOR_FETCH_TIMEOUT", 30*time.Second)
	if err != nil {
		return err
	}
	reconcile, err := parseBoolEnv("ARGOS_COLLECTOR_RECONCILE", true)
	if err != nil {
		return err
	}

	// Build the HTTP-backed store.
	store, err := apiclient.NewStore(apiclient.Config{
		ServerURL:    serverURL,
		Token:        token,
		CACert:       os.Getenv("ARGOS_CA_CERT"),
		ClientCert:   os.Getenv("ARGOS_CLIENT_CERT"),
		ClientKey:    os.Getenv("ARGOS_CLIENT_KEY"),
		ExtraHeaders: parseExtraHeaders(os.Getenv("ARGOS_EXTRA_HEADERS")),
	})
	if err != nil {
		return fmt.Errorf("init api client: %w", err)
	}

	// Build the Kubernetes source (in-cluster or kubeconfig).
	source, err := collector.NewKubeClient(kubeconfig)
	if err != nil {
		return fmt.Errorf("init kube client: %w", err)
	}

	// Wire the collector.
	coll := collector.New(store, source, clusterName, interval, fetchTimeout, reconcile)

	// Signal handling: SIGINT/SIGTERM → context cancel → collector stops.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("argos-collector running",
		"cluster_name", clusterName,
		"server_url", serverURL,
		"interval", interval,
		"reconcile", reconcile,
	)

	if err := coll.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("collector: %w", err)
	}

	slog.Info("argos-collector stopped cleanly")
	return nil
}

// parseExtraHeaders parses "key=value,key=value" into a map.
func parseExtraHeaders(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	headers := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok && k != "" {
			headers[k] = v
		}
	}
	return headers
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
