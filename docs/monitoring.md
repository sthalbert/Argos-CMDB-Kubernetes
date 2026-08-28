<div align="center"><img src="logo.svg" alt="longue-vue" height="38" /></div>

---

# Monitoring

longue-vue exposes Prometheus metrics for HTTP request tracking, collector health, and build information.

## Metrics endpoint

| Property | Value |
|----------|-------|
| Path | `/metrics` |
| Port | Same as the API (default `8080`) |
| Authentication | None (Prometheus scrape convention) |
| Format | Prometheus text exposition |

The endpoint is unauthenticated to match standard Prometheus scraping. If your threat model requires access control, restrict it with a NetworkPolicy, a reverse proxy, or a separate listener.

## Exported metrics

### HTTP

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_http_requests_total` | counter | `method`, `route`, `status` | Total HTTP requests handled. `status` is the HTTP status class (e.g., `2xx`, `4xx`). |
| `longue_vue_http_request_duration_seconds` | histogram | `method`, `route` | Request handling duration in seconds. Uses default Prometheus buckets. |

### Collector

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_collector_upserted_total` | counter | `cluster`, `resource` | Cumulative count of entities upserted per collector tick. |
| `longue_vue_collector_reconciled_total` | counter | `cluster`, `resource` | Cumulative count of entities removed by reconciliation. |
| `longue_vue_collector_errors_total` | counter | `cluster`, `resource`, `phase` | Collector errors. `phase` is `list`, `upsert`, `reconcile`, or `lookup`. |
| `longue_vue_collector_last_poll_timestamp_seconds` | gauge | `cluster`, `resource` | Unix timestamp of the last successful poll. |
| `longue_vue_cluster_last_seen_timestamp_seconds` | gauge | `cluster` | Unix timestamp of the last collector heartbeat per cluster. Server-side authority (ADR-0044); unlike the collector-registry gauge above, this covers push mode too. |
| `longue_vue_clusters_stale` | gauge | -- | Clusters whose collector heartbeat exceeds the `cluster_stale_after_days` staleness threshold (0 while the feature is disabled). |

The `resource` label is one of: `version`, `cluster`, `nodes`, `namespaces`, `pods`, `workloads`, `services`, `ingresses`, `persistentvolumes`, `persistentvolumeclaims`, `replicasets`.

### EOL enricher

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_eol_enrichments_total` | counter | `cluster`, `resource`, `status` | EOL annotations written, per cluster, resource kind, and resulting EOL status. |
| `longue_vue_eol_errors_total` | counter | `cluster`, `resource`, `phase` | Enrichment errors. `phase` is `list`, `resolve`, or `update`. |
| `longue_vue_eol_last_run_timestamp_seconds` | gauge | -- | Unix timestamp of the last completed enrichment run. Use for freshness alerts. |

### Impact analysis

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_impact_queries_total` | counter | `entity_type` | Impact graph queries, per entity type. |
| `longue_vue_impact_query_duration_seconds` | histogram | `entity_type` | Impact graph query duration in seconds. |

### MCP server

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_mcp_tool_calls_total` | counter | `tool` | MCP tool calls, per tool name. |
| `longue_vue_mcp_tool_duration_seconds` | histogram | `tool` | MCP tool call duration in seconds, per tool name. |

### Cloud accounts and virtual machines (ADR-0015)

These metrics are exposed by longue-vue (not the vm-collector). For vm-collector metrics, see [vm-collector — Observability](vm-collector.md#observability).

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_cloud_accounts_total` | gauge | `status` | Number of registered cloud accounts, per status (`pending_credentials`, `active`, `error`, `disabled`). |
| `longue_vue_cloud_accounts_pending_credentials` | gauge | -- | Count of accounts in `status=pending_credentials`. A non-zero value means a collector is registered but admin has not supplied AK/SK. |
| `longue_vue_virtual_machines_total` | gauge | `cloud_account`, `terminated` | Number of virtual machines, per cloud account name and tombstone state. |
| `longue_vue_cloud_accounts_credentials_reads_total` | counter | `cloud_account` | Successful credential fetches via `GET /v1/cloud-accounts/.../credentials`, per account. |

### Auth verify (ingest gateway)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_auth_verify_total` | counter | `result` | `POST /v1/auth/verify` calls, per outcome (`valid`, `invalid`, `rate_limited`). |
| `longue_vue_ingest_listener_client_cert_failures_total` | counter | `reason` | Failed mTLS client-cert validations on the ingest listener. `reason` is `bad_ca`, `expired`, `cn_not_allowed`, or `none_provided`. |

### Time-travel history (ADR-0021, Phase 1)

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_time_travel_writes_total` | counter | `kind`, `change_type` | History rows written (or suppressed as noop), per entity kind and change type. `change_type=noop` counts suppressed updates where no watched field changed. |

### Build

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_build_info` | gauge | `version`, `go_version` | Always 1. Carries version and Go toolchain info as labels. |

### Go runtime

The standard `go_*` and `process_*` collectors from `client_golang` are also registered (goroutine count, GC stats, memory stats, open file descriptors, etc.).

---

## vm-collector metrics

The `longue-vue-vm-collector` binary exposes its own Prometheus metrics on a separate endpoint (default `127.0.0.1:9090`, configurable via `LONGUE_VUE_VM_COLLECTOR_METRICS_ADDR`). These are not scraped by the main longue-vue `/metrics` endpoint. See [vm-collector — Observability](vm-collector.md#observability) for the full metric list and alert examples.

## longue-vue-ingest-gw metrics

The `longue-vue-ingest-gw` binary exposes its own Prometheus metrics on its health listener (default `:9090`, configurable via `LONGUE_VUE_INGEST_GW_HEALTH_ADDR`). Key metrics include request counters, upstream latency, token-cache hit/miss rates, body-bytes histograms, and cert expiry. See [How to deploy the DMZ ingest gateway](how-to-deploy-dmz-ingest-gateway.md) for scrape configuration.

## Scrape configuration

### Annotation-based (kube-prometheus)

The longue-vue Deployment carries the standard annotations:

```yaml
metadata:
  annotations:
    prometheus.io/scrape: "true"
    prometheus.io/port: "8080"
    prometheus.io/path: "/metrics"
```

Most Prometheus deployments with annotation-based service discovery will pick this up automatically.

### ServiceMonitor (Prometheus Operator)

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: longue-vue
  namespace: longue-vue-system
  labels:
    app.kubernetes.io/name: longue-vue
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: longue-vue
  endpoints:
    - port: http
      path: /metrics
      interval: 30s
```

### PodMonitor

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: longue-vue
  namespace: longue-vue-system
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: longue-vue
  podMetricsEndpoints:
    - port: http
      path: /metrics
      interval: 30s
```

## Example alerts

### Collector freshness

Fire if any resource kind has not been polled in 10 minutes:

```yaml
- alert: LongueVueCollectorStale
  expr: time() - longue_vue_collector_last_poll_timestamp_seconds > 600
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "longue-vue collector stale for {{ $labels.cluster }}/{{ $labels.resource }}"
    description: "No successful poll in the last 10 minutes."
```

### Stale clusters (server-side)

Fire when any cluster exceeds the `cluster_stale_after_days` threshold.
Unlike `longue_vue_collector_last_poll_timestamp_seconds` (which lives in
the collector's own registry in push mode), this gauge is computed by the
server and covers pull and push collectors alike:

```yaml
- alert: LongueVueClusterStale
  expr: longue_vue_clusters_stale > 0
  for: 30m
  labels:
    severity: warning
  annotations:
    summary: "{{ $value }} longue-vue cluster(s) with no collector heartbeat"
    description: "Check /clusters with the 'Stale only' filter, or longue_vue_cluster_last_seen_timestamp_seconds for the culprit."
```

### Collector errors

Fire if the collector encounters persistent errors:

```yaml
- alert: LongueVueCollectorErrors
  expr: rate(longue_vue_collector_errors_total[5m]) > 0
  for: 10m
  labels:
    severity: warning
  annotations:
    summary: "longue-vue collector errors for {{ $labels.cluster }}/{{ $labels.resource }} ({{ $labels.phase }})"
    description: "Sustained collector errors over the last 10 minutes."
```

### HTTP error rate

Fire if more than 5% of requests return 5xx:

```yaml
- alert: LongueVueHighErrorRate
  expr: |
    sum(rate(longue_vue_http_requests_total{status=~"5.."}[5m]))
    /
    sum(rate(longue_vue_http_requests_total[5m]))
    > 0.05
  for: 5m
  labels:
    severity: critical
  annotations:
    summary: "longue-vue HTTP 5xx rate above 5%"
```

### HTTP latency

Fire if p95 request duration exceeds 2 seconds:

```yaml
- alert: LongueVueHighLatency
  expr: |
    histogram_quantile(0.95, sum(rate(longue_vue_http_request_duration_seconds_bucket[5m])) by (le))
    > 2
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "longue-vue p95 latency above 2s"
```

## Grafana dashboard tips

### Useful panels

- **Request rate** by route: `sum(rate(longue_vue_http_requests_total[5m])) by (route)`
- **Error rate** by route: `sum(rate(longue_vue_http_requests_total{status=~"[45].."}[5m])) by (route, status)`
- **Latency heatmap**: use `longue_vue_http_request_duration_seconds_bucket` as a heatmap source.
- **Collector freshness**: `time() - longue_vue_collector_last_poll_timestamp_seconds` per `(cluster, resource)` -- show as a stat panel with thresholds at 120s (green) / 300s (yellow) / 600s (red).
- **Upserts per tick**: `increase(longue_vue_collector_upserted_total[5m])` per `(cluster, resource)` as a stacked bar chart.
- **Reconciled per tick**: `increase(longue_vue_collector_reconciled_total[5m])` -- a sudden spike may indicate a cluster-wide event.
- **Build info**: use `longue_vue_build_info` as a stat panel to show the running version.

### Variables

Define Grafana template variables for:

- `cluster` sourced from `label_values(longue_vue_collector_last_poll_timestamp_seconds, cluster)`
- `resource` sourced from `label_values(longue_vue_collector_last_poll_timestamp_seconds, resource)`

This lets operators drill into a specific cluster or resource kind.

## References

- [ADR-0001](adr/adr-0001-cmdb-for-snc-using-kube.md) — foundational CMDB design (Prometheus metrics are part of the operational posture)
- [ADR-0005](adr/adr-0005-multi-cluster-collector.md) — multi-cluster topology reflected in collector metric labels
