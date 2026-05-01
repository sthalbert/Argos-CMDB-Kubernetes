// Command longue-vue-ingest-gw is the DMZ ingest gateway for longue-vue
// (ADR-0016). It accepts inbound HTTPS from K8s push collectors, verifies
// their bearer tokens against the longue-vue server via mTLS, and forwards
// write requests into the trusted zone. It serves a strict-write-only
// allowlist of 18 routes and never exposes any read or admin endpoint.
//
// Configuration is purely via environment variables — no config file, no
// CLI flags. See LONGUE_VUE_INGEST_GW_* vars below. Refusal to start on
// missing required vars is intentional; misconfiguration must surface at
// boot, not as cryptic 500s per request.
package main

import (
	"context"
	"crypto/tls"
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

	"github.com/sthalbert/longue-vue/internal/ingestgw"
)

// Build-time identity, set via -ldflags.
var (
	version = "dev"  //nolint:gochecknoglobals // -ldflags "-X main.version=..."
	commit  = "none" //nolint:gochecknoglobals // -ldflags "-X main.commit=..."
)

// Sentinel errors for env-var validation. Loud at boot beats subtle at
// request time.
var (
	errMissingUpstreamURL   = errors.New("LONGUE_VUE_INGEST_GW_UPSTREAM_URL is required")
	errMissingClientCert    = errors.New("LONGUE_VUE_INGEST_GW_CLIENT_CERT_FILE / _CLIENT_KEY_FILE are required")
	errMissingUpstreamCA    = errors.New("LONGUE_VUE_INGEST_GW_UPSTREAM_CA_FILE is required")
	errMissingListenerCerts = errors.New("LONGUE_VUE_INGEST_GW_LISTEN_TLS_CERT / _LISTEN_TLS_KEY are required")
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	ingestgw.SetBuildInfo(version, commit)

	if err := run(); err != nil {
		slog.Error("longue-vue-ingest-gw exited with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

type runConfig struct {
	listenAddr     string
	listenCertFile string
	listenKeyFile  string

	healthAddr string

	upstreamURL     string
	upstreamHost    string
	upstreamTimeout time.Duration
	upstreamCAFile  string

	clientCertFile string
	clientKeyFile  string

	cacheTTL         time.Duration
	cacheNegativeTTL time.Duration
	cacheMaxEntries  int

	maxBodyBytes    int64
	requiredScope   string
	shutdownTimeout time.Duration
}

func loadConfig() (runConfig, error) { //nolint:gocyclo // env-var validation; flat structure is clearer than factored helpers
	cfg := runConfig{
		listenAddr:     envOr("LONGUE_VUE_INGEST_GW_LISTEN_ADDR", ":8443"),
		listenCertFile: os.Getenv("LONGUE_VUE_INGEST_GW_LISTEN_TLS_CERT"),
		listenKeyFile:  os.Getenv("LONGUE_VUE_INGEST_GW_LISTEN_TLS_KEY"),
		healthAddr:     envOr("LONGUE_VUE_INGEST_GW_HEALTH_ADDR", ":9090"),
		upstreamURL:    os.Getenv("LONGUE_VUE_INGEST_GW_UPSTREAM_URL"),
		upstreamHost:   os.Getenv("LONGUE_VUE_INGEST_GW_UPSTREAM_HOST"),
		upstreamCAFile: os.Getenv("LONGUE_VUE_INGEST_GW_UPSTREAM_CA_FILE"),
		clientCertFile: envOr("LONGUE_VUE_INGEST_GW_CLIENT_CERT_FILE", "/etc/longue-vue-ingest-gw/tls/tls.crt"),
		clientKeyFile:  envOr("LONGUE_VUE_INGEST_GW_CLIENT_KEY_FILE", "/etc/longue-vue-ingest-gw/tls/tls.key"),
		requiredScope:  envOr("LONGUE_VUE_INGEST_GW_REQUIRED_SCOPE", "write"),
	}

	if cfg.upstreamURL == "" {
		return runConfig{}, errMissingUpstreamURL
	}
	if cfg.upstreamCAFile == "" {
		return runConfig{}, errMissingUpstreamCA
	}
	if cfg.listenCertFile == "" || cfg.listenKeyFile == "" {
		return runConfig{}, errMissingListenerCerts
	}
	if cfg.clientCertFile == "" || cfg.clientKeyFile == "" {
		return runConfig{}, errMissingClientCert
	}

	var err error
	cfg.upstreamTimeout, err = parseDurationEnv("LONGUE_VUE_INGEST_GW_UPSTREAM_TIMEOUT", 30*time.Second)
	if err != nil {
		return runConfig{}, err
	}
	cfg.cacheTTL, err = parseDurationEnv("LONGUE_VUE_INGEST_GW_CACHE_TTL", 60*time.Second)
	if err != nil {
		return runConfig{}, err
	}
	cfg.cacheNegativeTTL, err = parseDurationEnv("LONGUE_VUE_INGEST_GW_CACHE_NEGATIVE_TTL", 10*time.Second)
	if err != nil {
		return runConfig{}, err
	}
	cfg.cacheMaxEntries, err = parseIntEnv("LONGUE_VUE_INGEST_GW_CACHE_MAX_ENTRIES", 10000)
	if err != nil {
		return runConfig{}, err
	}
	cfg.maxBodyBytes, err = parseInt64Env("LONGUE_VUE_INGEST_GW_MAX_BODY_BYTES", 10*1024*1024) // 10 MiB
	if err != nil {
		return runConfig{}, err
	}
	cfg.shutdownTimeout, err = parseDurationEnv("LONGUE_VUE_INGEST_GW_SHUTDOWN_TIMEOUT", 30*time.Second)
	if err != nil {
		return runConfig{}, err
	}
	return cfg, nil
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// mTLS client identity — hot-reloading from disk via fsnotify.
	reloader, err := ingestgw.NewCertReloader(cfg.clientCertFile, cfg.clientKeyFile, slog.Default())
	if err != nil {
		return fmt.Errorf("init cert reloader: %w", err)
	}

	// Upstream CA (longue-vue's server cert chain).
	caBytes, err := os.ReadFile(cfg.upstreamCAFile) //nolint:gosec // operator-supplied path
	if err != nil {
		return fmt.Errorf("read upstream CA: %w", err)
	}
	caPool, err := ingestgw.AppendCertPoolFromPEM(caBytes)
	if err != nil {
		return fmt.Errorf("parse upstream CA: %w", err)
	}

	upstreamTLS := &tls.Config{
		MinVersion:             tls.VersionTLS13,
		RootCAs:                caPool,
		GetClientCertificate:   reloader.GetClientCertificate,
		SessionTicketsDisabled: true,
	}
	upstreamClient := &http.Client{
		Timeout: cfg.upstreamTimeout,
		Transport: &http.Transport{
			TLSClientConfig:     upstreamTLS,
			MaxIdleConnsPerHost: 32,
			IdleConnTimeout:     90 * time.Second,
			ForceAttemptHTTP2:   true,
		},
	}

	srv, err := ingestgw.NewServer(ingestgw.Config{
		UpstreamBaseURL: cfg.upstreamURL,
		UpstreamHost:    cfg.upstreamHost,
		UpstreamClient:  upstreamClient,
		MaxBodyBytes:    cfg.maxBodyBytes,
		CacheConfig: ingestgw.CacheConfig{
			MaxEntries:  cfg.cacheMaxEntries,
			PositiveTTL: cfg.cacheTTL,
			NegativeTTL: cfg.cacheNegativeTTL,
		},
		RequiredScope: cfg.requiredScope,
		ReadyzCheck: func(ctx context.Context) error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.upstreamURL, "/")+"/healthz", http.NoBody)
			if err != nil {
				return fmt.Errorf("build readyz probe: %w", err)
			}
			resp, err := upstreamClient.Do(req)
			if err != nil {
				return fmt.Errorf("upstream healthz: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("upstream healthz status %d", resp.StatusCode) //nolint:err113 // dynamic status code, not a comparable sentinel
			}
			return nil
		},
		Logger: slog.Default(),
	})
	if err != nil {
		return fmt.Errorf("init gateway server: %w", err)
	}

	// Inbound listener — TLS only, fronted by Envoy in production.
	listenerTLS, err := newListenerTLSConfig(cfg.listenCertFile, cfg.listenKeyFile)
	if err != nil {
		return fmt.Errorf("init listener TLS: %w", err)
	}

	mainSrv := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           srv.Handler(),
		TLSConfig:         listenerTLS,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Health/metrics on a separate (plaintext) listener, bound to the
	// pod IP only by convention. Prometheus + kubelet probes scrape it.
	healthMux := http.NewServeMux()
	healthMux.Handle("GET /metrics", ingestgw.MetricsHandler())
	// /healthz and /readyz also live on the main listener for Envoy probes,
	// but mounting them here too lets the kubelet probe directly without
	// going through Envoy.
	healthMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	healthSrv := &http.Server{
		Addr:              cfg.healthAddr,
		Handler:           healthMux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	return serveAndShutdown(rootCtx, mainSrv, healthSrv, cfg.shutdownTimeout)
}

func newListenerTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	// Validate at startup; the TLS server will load via GetCertificate
	// for hot-reload. For the inbound listener Vault Agent / cert-manager
	// can rotate without a restart by updating the file on disk.
	reloader, err := ingestgw.NewCertReloader(certFile, keyFile, slog.Default())
	if err != nil {
		return nil, fmt.Errorf("listener cert reloader: %w", err)
	}
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		GetCertificate:         reloader.GetCertificate,
		SessionTicketsDisabled: true,
	}, nil
}

//nolint:gocyclo // central shutdown dispatcher; flat select is clearer than nested helpers
func serveAndShutdown(
	ctx context.Context,
	main, health *http.Server,
	shutdownTimeout time.Duration,
) error {
	errCh := make(chan error, 2)
	go func() {
		slog.Info("longue-vue-ingest-gw listening",
			slog.String("addr", main.Addr),
			slog.String("version", version),
		)
		if err := main.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("main listener: %w", err)
		}
	}()
	go func() {
		slog.Info("longue-vue-ingest-gw health/metrics listening",
			slog.String("addr", health.Addr))
		if err := health.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("health listener: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received, draining",
			slog.String("timeout", shutdownTimeout.String()))
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	var firstErr error
	if err := main.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck // detached context — parent is already cancelled by shutdown signal
		firstErr = fmt.Errorf("main listener shutdown: %w", err)
	}
	if err := health.Shutdown(shutdownCtx); err != nil && firstErr == nil { //nolint:contextcheck // see above — same detached shutdown context
		firstErr = fmt.Errorf("health listener shutdown: %w", err)
	}
	if firstErr != nil {
		return firstErr
	}
	slog.Info("longue-vue-ingest-gw stopped cleanly")
	return nil
}

// envOr reads an env var, falling back to def when unset/empty.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func parseDurationEnv(name string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return d, nil
}

func parseIntEnv(name string, def int) (int, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return n, nil
}

func parseInt64Env(name string, def int64) (int64, error) {
	v := os.Getenv(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return n, nil
}
