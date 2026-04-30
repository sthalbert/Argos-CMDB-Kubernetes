// Command argos-vm-collector polls a cloud-provider API and pushes the
// resulting VM inventory to argosd over HTTPS. See ADR-0015.
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
	"strings"
	"syscall"
	"time"

	"github.com/sthalbert/longue-vue/internal/vmcollector"
	"github.com/sthalbert/longue-vue/internal/vmcollector/apiclient"
	"github.com/sthalbert/longue-vue/internal/vmcollector/provider"
)

// version is set at build time via -ldflags.
var version = "dev"

// Sentinel errors for required env vars.
var (
	errServerURLRequired   = errors.New("LONGUE_VUE_SERVER_URL is required")
	errAPITokenRequired    = errors.New("LONGUE_VUE_API_TOKEN is required")
	errAccountNameRequired = errors.New("LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME is required")
	errRegionRequired      = errors.New("LONGUE_VUE_VM_COLLECTOR_REGION is required")
	errUnknownProvider     = errors.New("unknown provider")
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("argos-vm-collector exited with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

type runConfig struct {
	serverURL         string
	token             string
	providerName      string
	accountName       string
	region            string
	providerEndpoint  string
	interval          time.Duration
	fetchTimeout      time.Duration
	reconcile         bool
	credentialRefresh time.Duration
	caCert            string
	clientCert        string
	clientKey         string
	extraHeaders      map[string]string
	metricsAddr       string
}

func loadConfig() (runConfig, error) {
	cfg := runConfig{
		serverURL:    os.Getenv("LONGUE_VUE_SERVER_URL"),
		token:        os.Getenv("LONGUE_VUE_API_TOKEN"),
		providerName: envOr("LONGUE_VUE_VM_COLLECTOR_PROVIDER", "outscale"),
		accountName:  os.Getenv("LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME"),
		region:       os.Getenv("LONGUE_VUE_VM_COLLECTOR_REGION"),
		caCert:       os.Getenv("LONGUE_VUE_CA_CERT"),
		clientCert:   os.Getenv("LONGUE_VUE_CLIENT_CERT"),
		clientKey:    os.Getenv("LONGUE_VUE_CLIENT_KEY"),
		extraHeaders: parseExtraHeaders(os.Getenv("LONGUE_VUE_EXTRA_HEADERS")),
		// Default 0.0.0.0 so the Kubernetes liveness probe (which runs
		// from outside the pod, against the pod IP) can reach it. The
		// pod IP itself is the network boundary — no Service exposes
		// this port externally unless the operator explicitly creates
		// one. Operators running the binary on bare VMs can override
		// to 127.0.0.1:9090 if they want strict localhost binding.
		metricsAddr:      envOr("LONGUE_VUE_VM_COLLECTOR_METRICS_ADDR", "0.0.0.0:9090"),
		providerEndpoint: os.Getenv("LONGUE_VUE_VM_COLLECTOR_PROVIDER_ENDPOINT_URL"),
	}
	if cfg.serverURL == "" {
		return runConfig{}, errServerURLRequired
	}
	if cfg.token == "" {
		return runConfig{}, errAPITokenRequired
	}
	if cfg.accountName == "" {
		return runConfig{}, errAccountNameRequired
	}
	if cfg.region == "" {
		return runConfig{}, errRegionRequired
	}
	var err error
	cfg.interval, err = parseDurationEnv("LONGUE_VUE_VM_COLLECTOR_INTERVAL", 5*time.Minute)
	if err != nil {
		return runConfig{}, err
	}
	cfg.fetchTimeout, err = parseDurationEnv("LONGUE_VUE_VM_COLLECTOR_FETCH_TIMEOUT", 30*time.Second)
	if err != nil {
		return runConfig{}, err
	}
	cfg.reconcile, err = parseBoolEnv("LONGUE_VUE_VM_COLLECTOR_RECONCILE", true)
	if err != nil {
		return runConfig{}, err
	}
	cfg.credentialRefresh, err = parseDurationEnv("LONGUE_VUE_VM_COLLECTOR_CREDENTIAL_REFRESH", time.Hour)
	if err != nil {
		return runConfig{}, err
	}
	return cfg, nil
}

func run() error {
	slog.Info("argos-vm-collector starting", slog.String("version", version))
	vmcollector.SetBuildInfo(version)

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	store, err := apiclient.NewStore(apiclient.Config{
		ServerURL:    cfg.serverURL,
		Token:        cfg.token,
		CACert:       cfg.caCert,
		ClientCert:   cfg.clientCert,
		ClientKey:    cfg.clientKey,
		ExtraHeaders: cfg.extraHeaders,
	})
	if err != nil {
		return fmt.Errorf("init api client: %w", err)
	}

	factory, err := pickProviderFactory(cfg.providerName, cfg.providerEndpoint)
	if err != nil {
		return err
	}

	coll := vmcollector.New(vmcollector.Config{
		Provider:          cfg.providerName,
		AccountName:       cfg.accountName,
		Region:            cfg.region,
		Interval:          cfg.interval,
		FetchTimeout:      cfg.fetchTimeout,
		Reconcile:         cfg.reconcile,
		CredentialRefresh: cfg.credentialRefresh,
	}, store, factory)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Optional /metrics listener — localhost-only by default to avoid
	// exposing collector internals on a public interface. Set
	// LONGUE_VUE_VM_COLLECTOR_METRICS_ADDR="" to disable.
	if cfg.metricsAddr != "" {
		startMetricsServer(ctx, cfg.metricsAddr)
	}

	if err := coll.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("collector: %w", err)
	}
	slog.Info("argos-vm-collector stopped cleanly")
	return nil
}

// startMetricsServer launches the /metrics listener in a goroutine and
// shuts it down when ctx is cancelled. Best-effort: a startup error is
// logged but does not prevent the collector from running.
func startMetricsServer(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", vmcollector.MetricsHandler())
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		slog.Info("argos-vm-collector: metrics listening", slog.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("argos-vm-collector: metrics listener error", slog.Any("error", err))
		}
	}()
	go func() {
		<-ctx.Done()
		// Use a fresh context: the parent ctx is already cancelled, so we need
		// an independent timeout for the graceful shutdown window.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx) //nolint:contextcheck // shutdown timeout is intentionally separated from the cancelled parent
	}()
}

// pickProviderFactory returns a ProviderFactory for the named provider.
// Only "outscale" is supported in v1. endpointURL is optional — empty
// falls back to the SDK default of api.{region}.outscale.com.
func pickProviderFactory(name, endpointURL string) (vmcollector.ProviderFactory, error) {
	if name == "outscale" {
		return func(creds apiclient.Credentials) (provider.Provider, error) {
			return provider.NewOutscale(creds.AccessKey, creds.SecretKey, creds.Region, endpointURL)
		}, nil
	}
	return nil, fmt.Errorf("%w: %q", errUnknownProvider, name)
}

func parseExtraHeaders(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	out := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok && k != "" {
			out[k] = v
		}
	}
	return out
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
