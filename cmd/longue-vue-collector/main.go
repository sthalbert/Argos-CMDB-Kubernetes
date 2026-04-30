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

	"github.com/sthalbert/longue-vue/internal/collector"
	"github.com/sthalbert/longue-vue/internal/collector/apiclient"
)

// version is set at build time via -ldflags.
var version = "dev"

// Sentinel errors for required environment variables.
var (
	errServerURLRequired   = errors.New("LONGUE_VUE_SERVER_URL is required")
	errAPITokenRequired    = errors.New("LONGUE_VUE_API_TOKEN is required")
	errClusterNameRequired = errors.New("LONGUE_VUE_CLUSTER_NAME is required")
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("argos-collector exited with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// collectorConfig holds the validated configuration for the push-mode collector.
type collectorConfig struct {
	serverURL    string
	token        string
	clusterName  string
	kubeconfig   string
	interval     time.Duration
	fetchTimeout time.Duration
	reconcile    bool
}

// loadCollectorConfig reads and validates all environment variables needed
// by the push-mode collector.
func loadCollectorConfig() (collectorConfig, error) {
	serverURL := os.Getenv("LONGUE_VUE_SERVER_URL")
	if serverURL == "" {
		return collectorConfig{}, errServerURLRequired
	}
	token := os.Getenv("LONGUE_VUE_API_TOKEN")
	if token == "" {
		return collectorConfig{}, errAPITokenRequired
	}
	clusterName := os.Getenv("LONGUE_VUE_CLUSTER_NAME")
	if clusterName == "" {
		return collectorConfig{}, errClusterNameRequired
	}

	interval, err := parseDurationEnv("LONGUE_VUE_COLLECTOR_INTERVAL", 5*time.Minute)
	if err != nil {
		return collectorConfig{}, err
	}
	fetchTimeout, err := parseDurationEnv("LONGUE_VUE_COLLECTOR_FETCH_TIMEOUT", 30*time.Second)
	if err != nil {
		return collectorConfig{}, err
	}
	reconcile, err := parseBoolEnv("LONGUE_VUE_COLLECTOR_RECONCILE", true)
	if err != nil {
		return collectorConfig{}, err
	}

	return collectorConfig{
		serverURL:    serverURL,
		token:        token,
		clusterName:  clusterName,
		kubeconfig:   os.Getenv("LONGUE_VUE_KUBECONFIG"),
		interval:     interval,
		fetchTimeout: fetchTimeout,
		reconcile:    reconcile,
	}, nil
}

func run() error {
	slog.Info("argos-collector starting", slog.String("version", version))

	cfg, err := loadCollectorConfig()
	if err != nil {
		return err
	}

	// Build the HTTP-backed store.
	apiStore, err := apiclient.NewStore(apiclient.Config{
		ServerURL:    cfg.serverURL,
		Token:        cfg.token,
		CACert:       os.Getenv("LONGUE_VUE_CA_CERT"),
		ClientCert:   os.Getenv("LONGUE_VUE_CLIENT_CERT"),
		ClientKey:    os.Getenv("LONGUE_VUE_CLIENT_KEY"),
		ExtraHeaders: parseExtraHeaders(os.Getenv("LONGUE_VUE_EXTRA_HEADERS")),
	})
	if err != nil {
		return fmt.Errorf("init api client: %w", err)
	}

	// Build the Kubernetes source (in-cluster or kubeconfig).
	source, err := collector.NewKubeClient(cfg.kubeconfig)
	if err != nil {
		return fmt.Errorf("init kube client: %w", err)
	}

	// Wire the collector.
	coll := collector.New(apiStore, source, cfg.clusterName, cfg.interval, cfg.fetchTimeout, cfg.reconcile)

	// Signal handling: SIGINT/SIGTERM → context cancel → collector stops.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("argos-collector running",
		slog.String("cluster_name", cfg.clusterName),
		slog.String("server_url", cfg.serverURL),
		slog.String("interval", cfg.interval.String()),
		slog.Bool("reconcile", cfg.reconcile),
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
