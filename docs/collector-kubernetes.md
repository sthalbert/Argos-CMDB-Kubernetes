# Kubernetes Pull Collector

The in-process Kubernetes pull collector is a goroutine that runs inside the `longue-vue` daemon. On every tick it calls the Kubernetes API server with `client-go`, upserts everything it finds into the CMDB, and deletes rows that have disappeared from the live cluster (reconciliation). No sidecar or separate binary is required: enable it with `LONGUE_VUE_COLLECTOR_ENABLED=true` and it starts alongside the API server.

The collector auto-creates a minimal cluster record on first contact if the cluster name is not already registered (ADR-0011). You do not need to call `POST /v1/clusters` before starting the collector.

For clusters that cannot reach longue-vue directly (air-gapped networks, zero-trust perimeters), use the **push collector** instead — see [Push Collector](deployment/push-collector.md).

---

## What it ingests

Each tick collects the following resource types in order:

| Resource | Notes |
|---|---|
| **Cluster** | API-server version refreshed each tick. |
| **Nodes** | Full enriched field set: role, cloud identity (`provider_id`, `instance_type`, `zone`), networking (`internal_ip`, `external_ip`, `pod_cidr`), OS stack (`kernel_version`, `operating_system`, `container_runtime_version`, `kube_proxy_version`), capacity + allocatable quadruples (cpu / memory / pods / ephemeral_storage), `conditions` + `taints` JSONB arrays, `ready` / `unschedulable` flags. |
| **PersistentVolumes** | Cluster-scoped; upserted by `(cluster_id, name)`. Builds a name → id map used to resolve PVC bindings. |
| **Namespaces** | Upserted by `(cluster_id, name)`. Builds a name → id map used by all namespaced resources. |
| **Workloads** | Deployments, StatefulSets, and DaemonSets (three `AppsV1` list calls). Polymorphic on a `kind` discriminator; kind-specific detail in the `spec` JSONB column. Ingested before pods so each pod's `workload_id` FK can be resolved immediately. Carry a `containers` JSONB column (`{name, image, image_id?, init}` per entry) for SBOM / CVE workflows. |
| **Pods** | Upserted by `(namespace_id, name)`. The controlling `ownerReference` is walked — `ReplicaSet` → `Deployment` via a side `ListReplicaSetOwners` call, or direct for `StatefulSet` / `DaemonSet` — to set `workload_id` (null for unmodelled owners such as `Job`). Carry a `containers` JSONB column. |
| **Services** | Load-balancer ingress addresses (`status.loadBalancer.ingress[]`) flattened into a `load_balancer` JSONB column, so on-prem VIPs (MetalLB, Kube-VIP, hardware LBs) surface alongside cloud-provisioned ones. |
| **Ingresses** | Rules, TLS, and load-balancer info flattened into JSONB. |
| **PersistentVolumeClaims** | Each PVC's `spec.volumeName` is resolved against the PV map to set `bound_volume_id`; null when pending or the PV was not listed this tick. |

---

## Pull vs push

| | Pull (this document) | Push (`longue-vue-collector`) |
|---|---|---|
| **Binary** | In-process goroutine in `longue-vue` | Standalone `longue-vue-collector` |
| **Network** | longue-vue must reach the kube API | Collector must reach longue-vue (HTTPS) |
| **Use when** | longue-vue can see the cluster directly | Air-gapped or zero-trust networks |
| **Helm chart** | `charts/longue-vue` (`collector.*` values) | `charts/longue-vue-collector` |

See [Push Collector](deployment/push-collector.md) and [ADR-0009](adr/adr-0009-push-collector-for-airgapped-clusters.md) for the push variant.

---

## Configuration

All variables are read at startup. The daemon must be restarted to pick up changes.

| Variable | Default | Required | Description |
|---|---|---|---|
| `LONGUE_VUE_COLLECTOR_ENABLED` | `false` | — | Set to `true` to activate the pull collector. |
| `LONGUE_VUE_COLLECTOR_CLUSTERS` | — | Yes (or legacy) | JSON array of `{name, kubeconfig}` objects. Primary multi-cluster config. See [Multi-cluster setup](#multi-cluster-setup). |
| `LONGUE_VUE_CLUSTER_NAME` | — | Yes (or JSON) | Legacy single-cluster shortcut: cluster name. |
| `LONGUE_VUE_KUBECONFIG` | `""` | — | Legacy single-cluster shortcut: path to kubeconfig file. Empty string falls back to in-cluster config. |
| `LONGUE_VUE_COLLECTOR_INTERVAL` | `5m` | — | Polling interval (Go duration string, e.g. `2m30s`). |
| `LONGUE_VUE_COLLECTOR_FETCH_TIMEOUT` | `10s` | — | Per-request timeout for Kubernetes API calls. |
| `LONGUE_VUE_COLLECTOR_RECONCILE` | `true` | — | Delete CMDB rows that disappeared from the live cluster. Disable only for debugging — required for ANSSI cartography fidelity. |

`LONGUE_VUE_COLLECTOR_CLUSTERS` and the legacy `LONGUE_VUE_CLUSTER_NAME` / `LONGUE_VUE_KUBECONFIG` are mutually exclusive. Use the JSON array for multi-cluster deployments; the legacy vars still work as a single-cluster shortcut.

---

## Multi-cluster setup

### JSON array format

```json
[
  {
    "name": "prod-eu-west-1",
    "kubeconfig": "/etc/longue-vue/kubeconfigs/prod-eu-west-1.yaml"
  },
  {
    "name": "staging",
    "kubeconfig": "/etc/longue-vue/kubeconfigs/staging.yaml"
  },
  {
    "name": "in-cluster-mgmt",
    "kubeconfig": ""
  }
]
```

Each entry runs a dedicated collector goroutine sharing the same store. Cluster names must be unique. An empty `kubeconfig` path uses the pod's in-cluster service account (see below).

### In-cluster mode

When `longue-vue` itself runs inside Kubernetes and `kubeconfig` is empty (or `LONGUE_VUE_KUBECONFIG` is unset), the collector falls back to in-cluster config: reads `KUBERNETES_SERVICE_HOST` / `KUBERNETES_SERVICE_PORT` and the projected service-account token. The pod must be bound to a ServiceAccount with the RBAC permissions listed in [Required RBAC](#required-rbac).

### Kubeconfig-secret mode (Helm)

When deploying via the `charts/longue-vue` Helm chart, mount each kubeconfig as a Secret and set:

```yaml
collector:
  enabled: true
  clusters: |
    [
      {"name": "prod", "kubeconfig": "/etc/kubeconfigs/prod.yaml"},
      {"name": "staging", "kubeconfig": "/etc/kubeconfigs/staging.yaml"}
    ]
  kubeconfigSecret:
    name: longue-vue-kubeconfigs
```

See `charts/longue-vue/values.yaml` for the full schema.

---

## Reconciliation semantics

When `LONGUE_VUE_COLLECTOR_RECONCILE=true` (the default), the collector deletes CMDB rows that were not returned by the most recent successful Kubernetes API list:

- **Namespaced resources** (pods, workloads, services, ingresses, PVCs): reconciled per-namespace — rows for other namespaces are not touched.
- **Cluster-scoped resources** (nodes, PVs): reconciled across the whole cluster.
- **Workloads**: keyed on the `(kind, name)` tuple, so deleting a `Deployment` named `web` does not remove a still-live `StatefulSet` named `web`.

**Critical safety guard**: reconciliation only runs after a *successful* list. If the Kubernetes API returns an error mid-tick, the collector logs the error, skips reconciliation, and retries on the next interval. A transient API blip never wipes the store.

Reconciliation is required for ANSSI SecNumCloud cartography fidelity: the CMDB must mirror the cluster truthfully. Disable it only temporarily during debugging (`LONGUE_VUE_COLLECTOR_RECONCILE=false`).

---

## Required RBAC

The collector uses only the `list` verb — it never reads individual objects, watches resources, or mutates anything. The required ClusterRole is defined in `charts/longue-vue-collector/templates/clusterrole.yaml`:

| API group | Resources | Verbs |
|---|---|---|
| `""` (core) | `nodes`, `namespaces`, `pods`, `services`, `persistentvolumes`, `persistentvolumeclaims` | `list` |
| `apps` | `deployments`, `statefulsets`, `daemonsets`, `replicasets` | `list` |
| `networking.k8s.io` | `ingresses` | `list` |

When running in-cluster, bind this ClusterRole to the longue-vue ServiceAccount. When using kubeconfig files, the kubeconfig user must hold equivalent permissions on the target cluster.

---

## Observability

The collector exposes the following Prometheus metrics under the `longue_vue_collector_*` family (scraped at `/metrics`, no auth required):

| Metric | Type | Labels | Description |
|---|---|---|---|
| `longue_vue_collector_upserted_total` | Counter | `cluster`, `resource` | Entities upserted per tick. |
| `longue_vue_collector_reconciled_total` | Counter | `cluster`, `resource` | Rows deleted during reconciliation. |
| `longue_vue_collector_errors_total` | Counter | `cluster`, `resource`, `phase` | Errors by phase: `list`, `upsert`, `reconcile`, `lookup`. |
| `longue_vue_collector_last_poll_timestamp_seconds` | Gauge | `cluster`, `resource` | Unix timestamp of the last successful poll for each resource type. |

### Alerting recommendations

```yaml
# Alert when a cluster has not been polled successfully for 3× the interval.
- alert: CollectorStalePoll
  expr: time() - longue_vue_collector_last_poll_timestamp_seconds > 900
  for: 5m
  labels:
    severity: warning
  annotations:
    summary: "Collector has not polled {{ $labels.cluster }}/{{ $labels.resource }} in 15 m"

# Alert on any list-phase error (indicates kube API or RBAC problem).
- alert: CollectorListError
  expr: increase(longue_vue_collector_errors_total{phase="list"}[10m]) > 0
  labels:
    severity: warning
  annotations:
    summary: "Collector list error on {{ $labels.cluster }}/{{ $labels.resource }}"
```

See [Monitoring](monitoring.md) for the full metrics catalogue and Grafana tips.

---

## Troubleshooting

### Inventory is empty after enabling the collector

1. Confirm `LONGUE_VUE_COLLECTOR_ENABLED=true` is set and the daemon was restarted.
2. Check startup logs for `"collector started"` — absence means the env var was not parsed.
3. Verify at least one cluster is configured via `LONGUE_VUE_COLLECTOR_CLUSTERS` or `LONGUE_VUE_CLUSTER_NAME`.
4. Check `longue_vue_collector_errors_total{phase="list"}` — a non-zero value points to a Kubernetes API connectivity or RBAC problem.
5. Run `kubectl auth can-i list pods --as=system:serviceaccount:<namespace>:<sa-name>` to verify RBAC.

### Data is stale

- Reduce `LONGUE_VUE_COLLECTOR_INTERVAL` (e.g. `1m` for faster convergence during initial setup).
- Check `longue_vue_collector_last_poll_timestamp_seconds` — a lagging timestamp indicates slow Kubernetes API responses; increase `LONGUE_VUE_COLLECTOR_FETCH_TIMEOUT`.

### The store was wiped after a kube API blip

It was not — the reconcile-only-on-success guard means a failed list never triggers deletions. If rows disappeared, the Kubernetes API did return a successful (possibly empty) list. Check the kube API server logs and verify the collector's RBAC allows listing the affected resource.

### Pods show `workload_id = null`

The pod's owner is a resource type the collector does not model (e.g. a `Job`, `CronJob`, or custom controller). This is expected. The pod is still ingested correctly; only the FK to the parent workload is absent.

---

## References

- [ADR-0005 — Multi-cluster collector topology](adr/adr-0005-multi-cluster-collector.md)
- [ADR-0009 — Push collector for air-gapped clusters](adr/adr-0009-push-collector-for-airgapped-clusters.md)
- [ADR-0011 — Collector auto-creates cluster records](adr/adr-0011-collector-auto-creates-cluster.md)
- [Push Collector](deployment/push-collector.md) — deploy `longue-vue-collector` for air-gapped or zero-trust networks.
- [Deploy with Helm](deployment/helm.md) — one-command install, including `collector.*` chart values.
- [Monitoring](monitoring.md) — full Prometheus metrics reference and Grafana tips.
