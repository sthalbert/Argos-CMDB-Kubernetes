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

	// Time-travel history capture metrics (ADR-0021 Phase 2).
	timeTravelWritesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "time_travel",
		Name:      "writes_total",
		Help:      "History rows written (or suppressed as noop), per entity kind and change_type. change_type='noop' counts suppressed updates where no watched field changed.",
	}, []string{"kind", "change_type"})

	// DMZ ingest gateway metrics on the longue-vue side (ADR-0016).
	ingestVerifyTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "auth",
		Name:      "verify_total",
		Help:      "POST /v1/auth/verify calls, per outcome (valid / invalid / rate_limited).",
	}, []string{"result"})

	nodeImageBackfillTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Name:      "node_image_backfill_total",
		Help:      "Node OS-image backfill requests, per result (matched / nomatch).",
	}, []string{"result"})

	ingestListenerClientCertFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "ingest_listener",
		Name:      "client_cert_failures_total",
		Help:      "Failed mTLS client-cert validations on the ingest listener, per reason (bad_ca / expired / cn_not_allowed / none_provided).",
	}, []string{"reason"})

	auditEventsSkipped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "audit",
		Name:      "events_skipped_total",
		Help: "audit_events INSERTs suppressed by the no-op filter, labelled " +
			"by actor kind, resource type, and reason " +
			"(no_change|reconcile_empty). See ADR-0024.",
	}, []string{"actor_kind", "resource_type", "reason"})

	extractsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Name:      "extracts_total",
		Help:      "Bulk extracts requested via /v1/search/extract*, /v1/eol/extract, or /v1/applications/extract.*, per page, format, and outcome.",
	}, []string{"page", "format", "outcome"})

	extractRowsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Name:      "extract_rows_total",
		Help:      "Cumulative count of rows emitted by extract endpoints, per page.",
	}, []string{"page"})

	// dictCoverage counts workloads by where their effective DICT
	// classification comes from (ADR-0029 §6). Refreshed by a periodic
	// goroutine in longue-vue from a single SQL query over
	// workloads ⋈ applications.
	dictCoverage = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Name:      "dict_coverage",
		Help:      "Count of workloads by effective-DICT source (ADR-0029): application | workload | none.",
	}, []string{"source"})

	// applicationsTotal counts applications split by whether they belong to a
	// block and whether any DICT axis is set (ADR-0029 §9). Refreshed by the
	// same periodic goroutine as dictCoverage.
	applicationsTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Name:      "applications_total",
		Help:      "Count of applications, labelled by whether they belong to a block and whether any DICT axis is set (ADR-0029 §9).",
	}, []string{"has_block", "has_dict"})

	// applicationBlocksTotal counts application blocks (ADR-0029 §9).
	applicationBlocksTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Name:      "application_blocks_total",
		Help:      "Count of application blocks (ADR-0029 §9).",
	})

	// flowStates counts synthesized flows per cluster, layer (perimeter |
	// internal), and state (conforme | non_declare | manquant | large_ouvert).
	// Computed at refresh time from the read-time flow-matrix synthesis, only
	// for clusters with >=1 reference row and only when flow_matrix_enabled.
	flowStates = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "longue_vue_flow_states",
		Help: "Count of synthesized flows per cluster, layer, and state.",
	}, []string{"cluster", "layer", "state"})

	// flowReferenceRows counts declared reference flow rows per cluster.
	flowReferenceRows = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "longue_vue_flow_reference_rows",
		Help: "Declared reference flow rows per cluster.",
	}, []string{"cluster"})

	// flowDanglingRefs counts reference rows whose endpoint no longer resolves,
	// per cluster (surfaced as synthesis warnings).
	flowDanglingRefs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "longue_vue_flow_dangling_references",
		Help: "Reference rows whose endpoint no longer resolves, per cluster.",
	}, []string{"cluster"})

	// clusterLastSeen exports each cluster's collector heartbeat. This is
	// the server-side authority: the collector-side
	// longue_vue_collector_last_poll_timestamp_seconds lives in the
	// collector's own registry in push mode and cannot drive server-side
	// alerting.
	clusterLastSeen = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "longue_vue_cluster_last_seen_timestamp_seconds",
		Help: "Unix timestamp of the last collector heartbeat per cluster.",
	}, []string{"cluster"})

	// clustersStale counts clusters whose heartbeat is older than the
	// cluster_stale_after_days setting (0 while the feature is disabled).
	clustersStale = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "longue_vue_clusters_stale",
		Help: "Clusters whose collector heartbeat exceeds the staleness threshold.",
	})
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
		nodeImageBackfillTotal,
		ingestListenerClientCertFailures,
		timeTravelWritesTotal,
		auditEventsSkipped,
		extractsTotal,
		extractRowsTotal,
		dictCoverage,
		applicationsTotal,
		applicationBlocksTotal,
		flowStates,
		flowReferenceRows,
		flowDanglingRefs,
		clusterLastSeen,
		clustersStale,
	)
}

// SetFlowStates sets the per-(cluster, layer) flow-state counts. Called from
// the periodic metrics-refresh loop after recomputing the read-time flow-matrix
// synthesis. Callers should ResetFlowGauges() once per refresh first so a state
// that drops to zero is cleared rather than left stale.
func SetFlowStates(cluster, layer string, counts map[string]int) {
	for state, n := range counts {
		flowStates.WithLabelValues(cluster, layer, state).Set(float64(n))
	}
}

// SetFlowReferenceRows sets the declared-reference-rows gauge for a cluster.
func SetFlowReferenceRows(cluster string, n int) {
	flowReferenceRows.WithLabelValues(cluster).Set(float64(n))
}

// SetFlowDanglingRefs sets the dangling-reference gauge for a cluster.
func SetFlowDanglingRefs(cluster string, n int) {
	flowDanglingRefs.WithLabelValues(cluster).Set(float64(n))
}

// ResetFlowGauges clears all flow-matrix gauge series. Called at the start of
// each refresh pass so clusters that lose their reference rows (or whose state
// counts drop to zero) don't leave stale series behind.
func ResetFlowGauges() {
	flowStates.Reset()
	flowReferenceRows.Reset()
	flowDanglingRefs.Reset()
}

// ClusterHeartbeat is one live cluster's heartbeat, fed to
// SetClusterHeartbeats by the periodic metrics-refresh loop.
type ClusterHeartbeat struct {
	Name       string
	LastSeenAt time.Time
}

// SetClusterHeartbeats replaces the per-cluster heartbeat series.
// Reset-then-set so renamed or deleted clusters drop their series.
func SetClusterHeartbeats(rows []ClusterHeartbeat) {
	clusterLastSeen.Reset()
	for _, r := range rows {
		clusterLastSeen.WithLabelValues(r.Name).Set(float64(r.LastSeenAt.Unix()))
	}
}

// SetClustersStale sets the stale-cluster count gauge.
func SetClustersStale(n int) {
	clustersStale.Set(float64(n))
}

// SetDICTCoverage sets the per-source workload effective-DICT coverage gauge
// (ADR-0029 §6). Called from the periodic metrics-refresh loop.
func SetDICTCoverage(application, workload, none int) {
	dictCoverage.WithLabelValues("application").Set(float64(application))
	dictCoverage.WithLabelValues("workload").Set(float64(workload))
	dictCoverage.WithLabelValues("none").Set(float64(none))
}

// ApplicationCount is one (has_block, has_dict) bucket of the applications
// population, used to set the longue_vue_applications_total gauge series
// (ADR-0029 §9).
type ApplicationCount struct {
	HasBlock bool
	HasDict  bool
	Count    int
}

// SetApplicationCounts replaces all longue_vue_applications_total series from
// the supplied buckets. Resets first so a combination that drops to zero
// between refreshes is cleared rather than left stale. Called from the
// periodic metrics-refresh loop (ADR-0029 §9).
func SetApplicationCounts(buckets []ApplicationCount) {
	applicationsTotal.Reset()
	for _, b := range buckets {
		applicationsTotal.WithLabelValues(boolLabel(b.HasBlock), boolLabel(b.HasDict)).Set(float64(b.Count))
	}
}

// SetApplicationBlocksTotal sets the longue_vue_application_blocks_total gauge
// (ADR-0029 §9).
func SetApplicationBlocksTotal(n int) {
	applicationBlocksTotal.Set(float64(n))
}

func boolLabel(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// IngestVerifyTotal increments the POST /v1/auth/verify outcome counter.
// `result` is one of "valid", "invalid", "rate_limited" — the cardinality
// stays bounded regardless of how many tokens or callers exist.
func IngestVerifyTotal(result string) {
	ingestVerifyTotal.WithLabelValues(result).Inc()
}

// ObserveNodeImageBackfill records one node-image backfill request.
// result is "matched" (≥1 node matched) or "nomatch".
func ObserveNodeImageBackfill(result string) {
	nodeImageBackfillTotal.WithLabelValues(result).Inc()
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

// ObserveTimeTravelWrite increments the time_travel_writes_total counter for a
// history row that was actually written. kind is e.g. "clusters"; changeType is
// one of "create", "update", "soft_delete", "restore".
func ObserveTimeTravelWrite(kind, changeType string) {
	timeTravelWritesTotal.WithLabelValues(kind, changeType).Inc()
}

// ObserveTimeTravelNoop increments the time_travel_writes_total counter with
// change_type="noop" to track suppressed update events (RISK-003).
func ObserveTimeTravelNoop(kind string) {
	timeTravelWritesTotal.WithLabelValues(kind, "noop").Inc()
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

// ObserveAuditSkipped increments the audit-skipped counter. actor_kind is
// "user" | "token" | "anonymous"; resource_type is the singular resource
// label (e.g., "pod"); reason is "no_change" or "reconcile_empty".
func ObserveAuditSkipped(actorKind, resourceType, reason string) {
	auditEventsSkipped.WithLabelValues(actorKind, resourceType, reason).Inc()
}

// AuditEventsSkippedFor returns the per-labelset Counter for tests. Not
// for production code paths — production should call ObserveAuditSkipped.
func AuditEventsSkippedFor(actorKind, resourceType, reason string) prometheus.Counter {
	return auditEventsSkipped.WithLabelValues(actorKind, resourceType, reason)
}

// ObserveExtract records one completed extract: bumps the per-(page, format,
// outcome) counter and adds `rows` to the per-page row total. `outcome` is
// one of "ok", "truncated", "error", "denied".
func ObserveExtract(page, format, outcome string, rows int) {
	extractsTotal.WithLabelValues(page, format, outcome).Inc()
	if rows > 0 {
		extractRowsTotal.WithLabelValues(page).Add(float64(rows))
	}
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
