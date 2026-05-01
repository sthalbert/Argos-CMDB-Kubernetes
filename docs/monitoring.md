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

The `resource` label is one of: `version`, `cluster`, `nodes`, `namespaces`, `pods`, `workloads`, `services`, `ingresses`, `persistentvolumes`, `persistentvolumeclaims`, `replicasets`.

### Build

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_build_info` | gauge | `version`, `go_version` | Always 1. Carries version and Go toolchain info as labels. |

### Go runtime

The standard `go_*` and `process_*` collectors from `client_golang` are also registered (goroutine count, GC stats, memory stats, open file descriptors, etc.).

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
