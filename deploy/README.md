# argosd Kubernetes deploy example

Reference manifests for running `argosd` on a Kubernetes cluster, with argosd itself cataloguing the cluster it runs on via its ServiceAccount. Everything here is plain Kustomize — no Helm, no operator — so you can read it top to bottom in five minutes and adapt it.

For multi-cluster operation (one argosd cataloguing N clusters via mounted kubeconfigs), see [Multi-cluster setup](#multi-cluster-setup) at the bottom.

## Prerequisites

- A Kubernetes cluster (`kind`, `minikube`, or a real one).
- A PostgreSQL instance reachable from the cluster. Any Postgres ≥ 14 will do; the collector opens a standard `pgx` connection. For SecNumCloud-aligned production, pair with a qualified managed Postgres or an in-cluster operator (e.g. CloudNativePG). The argosd pod needs `CREATE` privileges on the target database so `goose` can apply migrations on startup.
- A container image reachable from the cluster. The image itself is built by [`Dockerfile`](../Dockerfile) in this repo. See [Image](#image) below.

## Files

| File | Purpose |
|---|---|
| `namespace.yaml` | `argos-system` namespace |
| `rbac.yaml` | ServiceAccount + ClusterRole (`list` on the six kinds the collector ingests) + ClusterRoleBinding |
| `deployment.yaml` | Single-replica `argosd` with probes, resource limits, non-root security context |
| `service.yaml` | ClusterIP Service on port 8080 |
| `kustomization.yaml` | Applies everything above with one `kubectl apply -k` |
| `secrets.example.yaml` | **Template only — copy, fill in, apply separately** |

The Secret is intentionally out of the Kustomization so the example file can't be accidentally deployed with placeholder values.

## Image

The [`Dockerfile`](../Dockerfile) builds a distroless static image. It's **not yet published to a registry** — a future PR wires GHCR publish into CI. Until then, for a first smoke test on a local cluster:

```sh
# 1. Build locally.
make docker-build                       # tags argos:dev

# 2. Load into the cluster (pick the line that matches your cluster).
kind load docker-image argos:dev --name <your-kind-cluster>
# or: minikube image load argos:dev
# or: docker push <your-registry>/argos:dev

# 3. Patch the image reference in deployment.yaml before applying,
#    or via `kubectl set image deployment/argosd argosd=argos:dev`
#    after the initial apply.
```

## Deploy

```sh
# 1. Credentials. Copy the template, fill in real values, apply.
cp deploy/secrets.example.yaml /tmp/argos-credentials.yaml
# ...edit /tmp/argos-credentials.yaml...
kubectl apply -f /tmp/argos-credentials.yaml

# 2. Everything else.
kubectl apply -k deploy/

# 3. Watch it come up.
kubectl -n argos-system get pods -w
kubectl -n argos-system logs -l app.kubernetes.io/name=argos -f
```

The first time the collector ticks you'll see:

```
collector: cluster not registered; POST /v1/clusters first cluster_name=in-cluster
```

That's expected — the CMDB requires explicit cluster registration before the collector writes anything (per ADR-0005). Register it:

```sh
# Port-forward the argosd service for a local curl (or use any client).
kubectl -n argos-system port-forward svc/argosd 8080:8080 &

# Replace TOKEN with the value from your Secret's ARGOS_API_TOKEN.
curl -sS -X POST http://localhost:8080/v1/clusters \
  -H "Authorization: Bearer TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"in-cluster","display_name":"Self","environment":"dev"}'
```

On the next tick (≤ 5 minutes with defaults) the collector will refresh `kubernetes_version` and populate nodes, namespaces, pods, workloads, services, ingresses.

Verify:

```sh
curl -sS -H "Authorization: Bearer TOKEN" http://localhost:8080/v1/namespaces | jq '.items | length'
```

## Metrics

`argosd` exposes a Prometheus `/metrics` endpoint on the same port as the API (default `8080`). The endpoint is unauthenticated — matching Prometheus scrape convention. Restrict access with a NetworkPolicy or a separate listener if your threat model requires it.

The Deployment carries classic `prometheus.io/scrape=true` + `prometheus.io/port=8080` + `prometheus.io/path=/metrics` annotations for in-cluster Prometheus setups. For Prometheus Operator / kube-prometheus-stack, create a `PodMonitor` or `ServiceMonitor` pointing at the `argosd` Service.

Exported series:

| Metric | Type | Labels |
|---|---|---|
| `argos_http_requests_total` | counter | `method`, `route`, `status` |
| `argos_http_request_duration_seconds` | histogram | `method`, `route` |
| `argos_collector_upserted_total` | counter | `cluster`, `resource` |
| `argos_collector_reconciled_total` | counter | `cluster`, `resource` |
| `argos_collector_errors_total` | counter | `cluster`, `resource`, `phase` |
| `argos_collector_last_poll_timestamp_seconds` | gauge | `cluster`, `resource` |
| `argos_build_info` | gauge | `version`, `go_version` |

`phase` on `errors_total` is one of `list` / `upsert` / `reconcile` / `lookup`. `resource` is one of `version` / `cluster` / `nodes` / `namespaces` / `pods` / `workloads` / `services` / `ingresses`. Plus the default `go_*` and `process_*` collectors from `client_golang`.

A simple freshness alert:

```
time() - argos_collector_last_poll_timestamp_seconds{resource="nodes"} > 600
```

## RBAC scope

`rbac.yaml` grants strictly `list` on the six K8s resource kinds argosd currently ingests:

- `nodes`, `namespaces`, `pods`, `services` (core API group)
- `deployments`, `statefulsets`, `daemonsets` (`apps` API group)
- `ingresses` (`networking.k8s.io` API group)

No `get`, no `watch`, no access to Secret / ConfigMap contents, no write of any kind. Argosd is read-only by design — the CMDB is a cartography tool, never a controller.

When new entity kinds land in the collector, this file grows a line; there's a one-to-one mapping between the ingestion passes in `internal/collector/` and the `rules` entries here.

## Multi-cluster setup

To catalogue multiple clusters from a single argosd (per ADR-0005):

1. Create a Secret in `argos-system` with each cluster's kubeconfig as a distinct key:

   ```sh
   kubectl -n argos-system create secret generic argos-kubeconfigs \
     --from-file=prod.yaml=/path/to/prod-kubeconfig \
     --from-file=staging.yaml=/path/to/staging-kubeconfig
   ```

2. Mount the Secret into the argosd pod:

   ```yaml
   volumes:
     - name: kubeconfigs
       secret:
         secretName: argos-kubeconfigs
   containers:
     - name: argosd
       volumeMounts:
         - name: kubeconfigs
           mountPath: /etc/argos/kubeconfigs
           readOnly: true
   ```

3. Replace `ARGOS_CLUSTER_NAME` / `ARGOS_KUBECONFIG` in `deployment.yaml` with `ARGOS_COLLECTOR_CLUSTERS` pointing at each mounted path:

   ```yaml
   env:
     - name: ARGOS_COLLECTOR_CLUSTERS
       value: |
         [
           {"name":"prod","kubeconfig":"/etc/argos/kubeconfigs/prod.yaml"},
           {"name":"staging","kubeconfig":"/etc/argos/kubeconfigs/staging.yaml"},
           {"name":"in-cluster","kubeconfig":""}
         ]
   ```

   The empty `kubeconfig` entry falls back to the pod's in-cluster ServiceAccount, which is already bound via `rbac.yaml` to the cluster argosd runs on.

4. Each kubeconfig should authenticate a ServiceAccount in *its own* cluster with the same RBAC as `rbac.yaml`. A compromise of the argosd pod exposes every catalogued cluster — keep the kubeconfigs read-only-scoped (ADR-0005 NEG-001).

5. `POST /v1/clusters` for each named cluster before the collectors start ingesting them.

## Uninstall

```sh
kubectl delete -k deploy/
kubectl delete -f /tmp/argos-credentials.yaml
kubectl delete namespace argos-system    # also cleans up anything stray
```

The Postgres database is untouched by these manifests. Drop `argos` (or the tables underneath it) separately if you want a clean slate.
