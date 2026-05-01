// Package metrics owns the Prometheus registry exposed on /metrics.
//
// All metrics live in one package so operators have a single place to read
// what's exported. The /metrics endpoint is mounted unauthenticated to match
// Prometheus's scrape convention; deployments that need access control should
// either put longue-vue behind a proxy that gates /metrics or run the scraper
// on a network path that's already trusted.
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

// Registry is longue-vue's private Prometheus registry. We don't reuse the
// default one — a per-process registry keeps scrape output stable across
// tests and makes it obvious which metrics are longue-vue-specific.
var Registry = prometheus.NewRegistry()

var (
	httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "HTTP requests handled, labelled by method, route pattern, and status class.",
	}, []string{"method", "route", "status"})

	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "longue_vue",
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "HTTP request handling duration in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "route"})

	collectorUpserts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "collector",
		Name:      "upserted_total",
		Help:      "Cumulative count of entities upserted by the collector, per cluster and resource kind.",
	}, []string{"cluster", "resource"})

	collectorReconciled = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "collector",
		Name:      "reconciled_total",
		Help:      "Cumulative count of entities removed by reconciliation, per cluster and resource kind.",
	}, []string{"cluster", "resource"})

	collectorErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "collector",
		Name:      "errors_total",
		Help:      "Collector errors, per cluster, resource kind, and phase (list, upsert, reconcile, lookup).",
	}, []string{"cluster", "resource", "phase"})

	collectorLastPoll = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Subsystem: "collector",
		Name:      "last_poll_timestamp_seconds",
		Help:      "Unix timestamp of the last successful poll for each (cluster, resource).",
	}, []string{"cluster", "resource"})

	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Name:      "build_info",
		Help:      "Set to 1 for the running longue-vue build; labels carry version and Go toolchain info.",
	}, []string{"version", "go_version"})

	eolEnrichments = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "eol",
		Name:      "enrichments_total",
		Help:      "EOL annotations written, per cluster, resource kind, and status.",
	}, []string{"cluster", "resource", "status"})

	eolErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "eol",
		Name:      "errors_total",
		Help:      "EOL enrichment errors, per cluster, resource kind, and phase.",
	}, []string{"cluster", "resource", "phase"})

	eolLastRun = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Subsystem: "eol",
		Name:      "last_run_timestamp_seconds",
		Help:      "Unix timestamp of the last completed EOL enrichment run.",
	})

	impactQueries = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "impact",
		Name:      "queries_total",
		Help:      "Impact graph queries, per entity type.",
	}, []string{"entity_type"})

	impactDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "longue_vue",
		Subsystem: "impact",
		Name:      "query_duration_seconds",
		Help:      "Impact graph query duration in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"entity_type"})

	mcpToolCalls = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "mcp",
		Name:      "tool_calls_total",
		Help:      "MCP tool calls, per tool name.",
	}, []string{"tool"})

	mcpToolDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "longue_vue",
		Subsystem: "mcp",
		Name:      "tool_duration_seconds",
		Help:      "MCP tool call duration in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"tool"})

	// VM-collector metrics on the longue-vue side (ADR-0015).
	cloudAccountsTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Name:      "cloud_accounts_total",
		Help:      "Number of registered cloud accounts, labelled by status.",
	}, []string{"status"})

	cloudAccountsPending = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Name:      "cloud_accounts_pending_credentials",
		Help:      "Number of cloud accounts in status=pending_credentials. A non-zero value means a collector is registered but admin has not yet supplied AK/SK.",
	})

	virtualMachinesTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Name:      "virtual_machines_total",
		Help:      "Number of virtual machines, labelled by cloud account name and tombstone state.",
	}, []string{"cloud_account", "terminated"})

	credentialsReads = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "cloud_accounts",
		Name:      "credentials_reads_total",
		Help:      "Cumulative successful credential fetches via GET /v1/cloud-accounts/.../credentials.",
	}, []string{"cloud_account"})

	// DMZ ingest gateway metrics on the longue-vue side (ADR-0016).
	ingestVerifyTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "auth",
		Name:      "verify_total",
		Help:      "POST /v1/auth/verify calls, per outcome (valid / invalid / rate_limited).",
	}, []string{"result"})

	ingestListenerClientCertFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_listener",
		Name:      "client_cert_failures_total",
		Help:      "Failed mTLS client-cert validations on the ingest listener, per reason (bad_ca / expired / cn_not_allowed / none_provided).",
	}, []string{"reason"})
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
		eolEnrichments,
		eolErrors,
		eolLastRun,
		impactQueries,
		impactDuration,
		mcpToolCalls,
		mcpToolDuration,
		cloudAccountsTotal,
		cloudAccountsPending,
		virtualMachinesTotal,
		credentialsReads,
		ingestVerifyTotal,
		ingestListenerClientCertFailures,
	)
}

// IngestVerifyTotal increments the POST /v1/auth/verify outcome counter.
// `result` is one of "valid", "invalid", "rate_limited" — the cardinality
// stays bounded regardless of how many tokens or callers exist.
func IngestVerifyTotal(result string) {
	ingestVerifyTotal.WithLabelValues(result).Inc()
}

// IngestListenerClientCertFailure increments the mTLS handshake failure
// counter on the longue-vue ingest listener. `reason` is one of "bad_ca",
// "expired", "cn_not_allowed", "none_provided" so a misconfigured gateway
// is diagnosable from a single Prometheus query.
func IngestListenerClientCertFailure(reason string) {
	ingestListenerClientCertFailures.WithLabelValues(reason).Inc()
}

// SetCloudAccountsTotal sets the per-status cloud-accounts gauge. Called
// from a periodic refresh loop in longue-vue that recomputes the totals from
// the store.
func SetCloudAccountsTotal(status string, n int) {
	cloudAccountsTotal.WithLabelValues(status).Set(float64(n))
}

// SetCloudAccountsPending sets the pending_credentials shorthand gauge.
func SetCloudAccountsPending(n int) {
	cloudAccountsPending.Set(float64(n))
}

// SetVirtualMachinesTotal sets the per-account VM count gauge.
// terminated is "true" / "false" string for label stability.
func SetVirtualMachinesTotal(cloudAccount, terminated string, n int) {
	virtualMachinesTotal.WithLabelValues(cloudAccount, terminated).Set(float64(n))
}

// ObserveCredentialsRead increments the per-account credentials-fetch
// counter. Called from HandleCollectorGetCredentialsBy{Name,ID} on a
// successful 200 response.
func ObserveCredentialsRead(cloudAccount string) {
	credentialsReads.WithLabelValues(cloudAccount).Inc()
}

// Handler returns the /metrics HTTP handler.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}

// SetBuildInfo sets the single longue_vue_build_info time-series to 1. Call once
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

// ObserveEOLEnrichment increments the per-(cluster, resource, status) enrichment counter.
func ObserveEOLEnrichment(cluster, resource, status string) {
	eolEnrichments.WithLabelValues(cluster, resource, status).Inc()
}

// ObserveEOLError increments the per-(cluster, resource, phase) EOL error counter.
func ObserveEOLError(cluster, resource, phase string) {
	eolErrors.WithLabelValues(cluster, resource, phase).Inc()
}

// MarkEOLRun stamps the last-completed-run gauge with the current time.
func MarkEOLRun() {
	eolLastRun.Set(float64(time.Now().Unix()))
}

// ObserveImpactQuery records an impact graph query.
func ObserveImpactQuery(entityType string, duration time.Duration) {
	impactQueries.WithLabelValues(entityType).Inc()
	impactDuration.WithLabelValues(entityType).Observe(duration.Seconds())
}

// ObserveMCPToolCall records an MCP tool call.
func ObserveMCPToolCall(tool string, duration time.Duration) {
	mcpToolCalls.WithLabelValues(tool).Inc()
	mcpToolDuration.WithLabelValues(tool).Observe(duration.Seconds())
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
