// Package ingestgw implements longue-vue-ingest-gw, the DMZ ingest gateway
// fronting the K8s push collector → longue-vue write path (ADR-0016).
//
// The package is split into small, single-purpose files:
//
//	allowlist.go     — the hardcoded (method, path) write allowlist
//	cache.go         — bounded LRU verify cache with positive/negative TTLs
//	verify_client.go — calls longue-vue's POST /v1/auth/verify over mTLS
//	tls_reload.go    — fsnotify-driven cert hot reload
//	proxy.go         — request forwarding with header strip and body cap
//	server.go        — wires all the pieces into a single http.Handler
//	metrics.go       — Prometheus registry + counters/histograms
package ingestgw

import (
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the gateway's private Prometheus registry. Separate from
// longue-vue's so the two binaries can run in the same pod (sidecar mode)
// without metric-name collisions; matches the pattern used by
// internal/vmcollector.
var Registry = prometheus.NewRegistry() //nolint:gochecknoglobals // standard Prometheus registry pattern

// Outcome labels for longue_vue_ingest_gw_requests_total. Kept as a small
// closed set so cardinality stays bounded regardless of traffic shape.
const (
	OutcomeAllowed         = "allowed"
	OutcomeDeniedPath      = "denied_path"      // path or method not in allowlist
	OutcomeDeniedToken     = "denied_token"     // verify says invalid
	OutcomeDeniedScope     = "denied_scope"     // valid token but missing required scope
	OutcomeDeniedBody      = "denied_body"      // body exceeded MaxBodyBytes
	OutcomeUpstreamError   = "upstream_error"   // mTLS / connection / 5xx
	OutcomeUpstreamTimeout = "upstream_timeout" // client timeout exceeded
)

var (
	requestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_gw",
		Name:      "requests_total",
		Help:      "Ingest gateway requests, labelled by HTTP method, route pattern, status class, and outcome.",
	}, []string{"method", "route", "status_class", "outcome"})

	requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_gw",
		Name:      "request_duration_seconds",
		Help:      "End-to-end ingest gateway request latency in seconds (includes upstream RTT).",
		Buckets:   prometheus.DefBuckets,
	}, []string{"route", "outcome"})

	upstreamDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_gw",
		Name:      "upstream_duration_seconds",
		Help:      "Upstream-only request latency in seconds (excludes gateway-side overhead).",
		Buckets:   prometheus.DefBuckets,
	}, []string{"route"})

	tokenVerifyTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_gw",
		Name:      "token_verify_total",
		Help:      "Calls to longue-vue's /v1/auth/verify, by result (valid / invalid / error).",
	}, []string{"result"})

	tokenCacheTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_gw",
		Name:      "token_cache_total",
		Help:      "Verify cache lifecycle events: hit / miss / negative_hit / evict / inflight_dedupe.",
	}, []string{"event"})

	tokenCacheSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_gw",
		Name:      "token_cache_size",
		Help:      "Current number of entries in the verify cache (positive + negative combined).",
	})

	certNotAfterSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_gw",
		Name:      "cert_not_after_seconds",
		Help:      "Unix timestamp of the current mTLS client cert's NotAfter. Alerting: cert_not_after - time() < 3600.",
	})

	certReloadTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_gw",
		Name:      "cert_reload_total",
		Help:      "Hot-reload events from the cert watcher, by result (success / failure).",
	}, []string{"result"})

	bodyBytes = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_gw",
		Name:      "body_bytes",
		Help:      "Distribution of forwarded request body sizes, by route. Power-of-two buckets from 1 KiB to 16 MiB.",
		Buckets:   prometheus.ExponentialBuckets(1024, 2, 15),
	}, []string{"route"})

	inflightRequests = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_gw",
		Name:      "inflight_requests",
		Help:      "Concurrent ingest gateway requests in flight.",
	})

	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_gw",
		Name:      "build_info",
		Help:      "Set to 1 for the running longue-vue-ingest-gw build; labels carry version and Go toolchain info.",
	}, []string{"version", "commit", "go_version"})
)

func init() {
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		requestsTotal,
		requestDuration,
		upstreamDuration,
		tokenVerifyTotal,
		tokenCacheTotal,
		tokenCacheSize,
		certNotAfterSeconds,
		certReloadTotal,
		bodyBytes,
		inflightRequests,
		buildInfo,
	)
}

// MetricsHandler returns the /metrics http.Handler for this registry.
func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}

// SetBuildInfo records the running build's version + commit. Called once
// at startup from main.
func SetBuildInfo(version, commit string) {
	bi, ok := debug.ReadBuildInfo()
	goVer := "unknown"
	if ok {
		goVer = bi.GoVersion
	}
	buildInfo.WithLabelValues(version, commit, goVer).Set(1)
}

// observeRequest records the per-request counter + duration. statusClass
// is computed via foldStatus to keep label cardinality bounded.
func observeRequest(method, route string, status int, outcome string, duration time.Duration) {
	requestsTotal.WithLabelValues(method, route, foldStatus(status), outcome).Inc()
	requestDuration.WithLabelValues(route, outcome).Observe(duration.Seconds())
}

// observeUpstream records upstream-only latency for a successfully-forwarded
// request. Skipped on allowlist denials and other gateway-local terminations.
func observeUpstream(route string, d time.Duration) {
	upstreamDuration.WithLabelValues(route).Observe(d.Seconds())
}

// observeBody records the request body size for sizing alerts.
func observeBody(route string, n int) {
	bodyBytes.WithLabelValues(route).Observe(float64(n))
}

// observeVerify records a verify-endpoint outcome.
func observeVerify(result string) {
	tokenVerifyTotal.WithLabelValues(result).Inc()
}

// observeCache records a cache lifecycle event.
func observeCache(event string) {
	tokenCacheTotal.WithLabelValues(event).Inc()
}

// setCacheSize updates the gauge after every put / evict.
func setCacheSize(n int) {
	tokenCacheSize.Set(float64(n))
}

// observeCertNotAfter sets the running cert's NotAfter as a gauge so
// Prometheus rules can alert before the cert actually expires.
func observeCertNotAfter(t time.Time) {
	certNotAfterSeconds.Set(float64(t.Unix()))
}

// observeCertReload counts cert hot-reload outcomes.
func observeCertReload(result string) {
	certReloadTotal.WithLabelValues(result).Inc()
}

// foldStatus collapses raw HTTP status codes into Prometheus-friendly
// classes ("2xx", "4xx", …) so cardinality stays bounded. Mirrors
// internal/metrics.statusClass; reproduced here so the gateway has no
// dependency on that longue-vue-internal package.
func foldStatus(code int) string {
	switch code {
	case http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusRequestEntityTooLarge,
		http.StatusServiceUnavailable:
		return strconv.Itoa(code)
	}
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return "1xx"
	}
}
