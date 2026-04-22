// Package metrics owns the Prometheus registry exposed on /metrics.
//
// All metrics live in one package so operators have a single place to read
// what's exported. The /metrics endpoint is mounted unauthenticated to match
// Prometheus's scrape convention; deployments that need access control should
// either put argosd behind a proxy that gates /metrics or run the scraper on
// a network path that's already trusted.
package metrics

import (
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is argosd's private Prometheus registry. We don't reuse the
// default one — a per-process registry keeps scrape output stable across
// tests and makes it obvious which metrics are argos-specific.
var Registry = prometheus.NewRegistry()

var (
	httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "argos",
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "HTTP requests handled, labelled by method, route pattern, and status class.",
	}, []string{"method", "route", "status"})

	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "argos",
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "HTTP request handling duration in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "route"})

	collectorUpserts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "argos",
		Subsystem: "collector",
		Name:      "upserted_total",
		Help:      "Cumulative count of entities upserted by the collector, per cluster and resource kind.",
	}, []string{"cluster", "resource"})

	collectorReconciled = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "argos",
		Subsystem: "collector",
		Name:      "reconciled_total",
		Help:      "Cumulative count of entities removed by reconciliation, per cluster and resource kind.",
	}, []string{"cluster", "resource"})

	collectorErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "argos",
		Subsystem: "collector",
		Name:      "errors_total",
		Help:      "Collector errors, per cluster, resource kind, and phase (list, upsert, reconcile, lookup).",
	}, []string{"cluster", "resource", "phase"})

	collectorLastPoll = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "argos",
		Subsystem: "collector",
		Name:      "last_poll_timestamp_seconds",
		Help:      "Unix timestamp of the last successful poll for each (cluster, resource).",
	}, []string{"cluster", "resource"})

	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "argos",
		Name:      "build_info",
		Help:      "Set to 1 for the running argosd build; labels carry version and Go toolchain info.",
	}, []string{"version", "go_version"})
)

func init() {
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		httpRequestsTotal,
		httpRequestDuration,
		collectorUpserts,
		collectorReconciled,
		collectorErrors,
		collectorLastPoll,
		buildInfo,
	)
}

// Handler returns the /metrics HTTP handler.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}

// SetBuildInfo sets the single argos_build_info time-series to 1. Call once
// at startup with the version injected via -ldflags.
func SetBuildInfo(version string) {
	goVersion := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		goVersion = info.GoVersion
	}
	buildInfo.WithLabelValues(version, goVersion).Set(1)
}

// ObserveUpserts increments the per-(cluster, resource) upsert counter by n.
// A zero n is a no-op — handy when a tick's list was successful but returned
// no items and we don't want to stamp anything.
func ObserveUpserts(cluster, resource string, n int) {
	if n <= 0 {
		return
	}
	collectorUpserts.WithLabelValues(cluster, resource).Add(float64(n))
}

// ObserveReconciled increments the per-(cluster, resource) reconcile counter by n.
func ObserveReconciled(cluster, resource string, n int64) {
	if n <= 0 {
		return
	}
	collectorReconciled.WithLabelValues(cluster, resource).Add(float64(n))
}

// ObserveError increments the per-(cluster, resource, phase) error counter.
// phase is one of "list", "upsert", "reconcile", "lookup".
func ObserveError(cluster, resource, phase string) {
	collectorErrors.WithLabelValues(cluster, resource, phase).Inc()
}

// MarkPoll stamps the last-successful-poll gauge with the current time.
// Called once per ingest* function after a successful list+upsert+reconcile
// cycle. Reconcile failures don't block the stamp — the list succeeded and
// the upserts reflect live state, which is what the freshness signal tracks.
func MarkPoll(cluster, resource string) {
	collectorLastPoll.WithLabelValues(cluster, resource).Set(float64(time.Now().Unix()))
}

// InstrumentHandler wraps an http.Handler with request counting + duration
// recording. Route label is taken from the request's pattern (stdlib mux,
// Go 1.22+); falls back to the raw path when Pattern is empty (e.g.,
// unmatched routes that produce a 404 before routing).
func InstrumentHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		httpRequestsTotal.WithLabelValues(r.Method, route, statusClass(rec.status)).Inc()
		httpRequestDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

// statusClass folds raw HTTP status codes into Prometheus-friendly classes
// ("2xx", "4xx", …) so label cardinality stays bounded. Keeps the common
// outliers ("401", "404") as their full code — useful for alerts.
func statusClass(code int) string {
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict:
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

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader captures the status code for metrics before delegating to the
// wrapped ResponseWriter.
func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}
