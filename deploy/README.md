# longue-vue Kubernetes deploy example

Reference manifests for running `longue-vue` on a Kubernetes cluster, with longue-vue itself cataloguing the cluster it runs on via its ServiceAccount. Everything here is plain Kustomize — no Helm, no operator — so you can read it top to bottom in five minutes and adapt it.

For multi-cluster operation (one longue-vue cataloguing N clusters via mounted kubeconfigs), see [Multi-cluster setup](#multi-cluster-setup) at the bottom.

## Prerequisites

- A Kubernetes cluster (`kind`, `minikube`, or a real one).
- A PostgreSQL instance reachable from the cluster. Any Postgres ≥ 14 will do; the collector opens a standard `pgx` connection. For SecNumCloud-aligned production, pair with a qualified managed Postgres or an in-cluster operator (e.g. CloudNativePG). The longue-vue pod needs `CREATE` privileges on the target database so `goose` can apply migrations on startup.
- A container image reachable from the cluster. The image itself is built by [`Dockerfile`](../Dockerfile) in this repo. See [Image](#image) below.

## Files

| File | Purpose |
|---|---|
| `namespace.yaml` | `longue-vue-system` namespace |
| `rbac.yaml` | ServiceAccount + ClusterRole (`list` on the kinds the collector ingests — see [RBAC scope](#rbac-scope)) + ClusterRoleBinding |
| `deployment.yaml` | Single-replica `longue-vue` with probes, resource limits, non-root security context |
| `service.yaml` | ClusterIP Service on port 8080 |
| `kustomization.yaml` | Applies everything above with one `kubectl apply -k` |
| `secrets.example.yaml` | **Template only — copy, fill in, apply separately** |

The Secret is intentionally out of the Kustomization so the example file can't be accidentally deployed with placeholder values.

## Image

The [`Dockerfile`](../Dockerfile) builds a distroless static image. Pre-built images are published to `ghcr.io/sthalbert/longue-vue` (and `ghcr.io/sthalbert/longue-vue-collector`) on every tagged release. To build locally for a smoke test:

```sh
# 1. Build locally.
make docker-build                       # tags longue-vue:dev

# 2. Load into the cluster (pick the line that matches your cluster).
kind load docker-image longue-vue:dev --name <your-kind-cluster>
# or: minikube image load longue-vue:dev
# or: docker push <your-registry>/longue-vue:dev

# 3. Patch the image reference in deployment.yaml before applying,
#    or via `kubectl set image deployment/longue-vue longue-vue=longue-vue:dev`
#    after the initial apply.
```

## Deploy

```sh
# 1. Credentials (database only; auth tokens are issued by an admin in
#    the UI now, not injected via env vars — per ADR-0007).
cp deploy/secrets.example.yaml /tmp/longue-vue-credentials.yaml
# ...edit /tmp/longue-vue-credentials.yaml — set LONGUE_VUE_DATABASE_URL...
kubectl apply -f /tmp/longue-vue-credentials.yaml

# 2. Everything else.
kubectl apply -k deploy/

# 3. Watch it come up.
kubectl -n longue-vue-system get pods -w
kubectl -n longue-vue-system logs -l app.kubernetes.io/name=longue-vue -f
```

### First-run bootstrap

On first boot with an empty database, longue-vue creates an `admin` user and
prints its password **once** to the startup log inside a banner:

```
WARN  ========================================================================
      LONGUE-VUE FIRST-RUN BOOTSTRAP
      A default admin user has been created:
        username: admin
        password: <16 random chars, or whatever you set via env>
        source:   generated randomly; capture now — it won't be printed again
      This account MUST rotate its password on first login.
      ========================================================================
```

For a predictable password, set `LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD` on the
Deployment before the first start. It's only consulted when no admin
user exists yet — safe to leave set across restarts.

### Optional: enable OIDC sign-in

Set the `LONGUE_VUE_OIDC_*` variables in `secrets.example.yaml` to let users
federate from your IdP instead of (or alongside) local passwords. The
local `admin` bootstrap still happens — OIDC is additive, not a
replacement — so you always have a break-glass login.

What the operator needs to do:

1. Register longue-vue as an application at the IdP. Redirect URI
   must be `https://<longue-vue-host>/v1/auth/oidc/callback`; grant types:
   `authorization_code`; request `openid email profile` scopes.
2. Fill in `LONGUE_VUE_OIDC_ISSUER`, `LONGUE_VUE_OIDC_CLIENT_ID`,
   `LONGUE_VUE_OIDC_CLIENT_SECRET`, `LONGUE_VUE_OIDC_REDIRECT_URL` in the Secret.
   Optional: `LONGUE_VUE_OIDC_LABEL` (button text), `LONGUE_VUE_OIDC_SCOPES`.
3. Restart longue-vue. On boot it fetches the issuer's discovery document
   and fails loudly if unreachable — misconfiguration surfaces at
   startup, not on a user's first login attempt.

First-time OIDC users land as role `viewer` (authorization is not
claim-driven — ADR-0007). An `admin` promotes them through the admin
panel at `/ui/admin/users` as needed.

### Cluster registration

The collector auto-creates a minimal cluster record (name only) on first contact if the cluster doesn't exist in the CMDB (ADR-0011). No manual step is required — after `kubectl apply -k deploy/`, the collector starts ingesting on the next tick.

**Optional: pre-register with curated metadata.** If you want display name, environment, owner, or criticality populated before the first tick, register the cluster manually:

```sh
# Port-forward the longue-vue service for a local curl.
kubectl -n longue-vue-system port-forward svc/longue-vue 8080:8080 &

# Log in, stash the session cookie.
curl -sS -c /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<your new rotated password>"}'

# Pre-register the cluster with metadata.
curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{"name":"in-cluster","display_name":"Self","environment":"dev"}'
```

On the next tick (≤ 5 minutes with defaults) the collector refreshes
`kubernetes_version` and populates nodes, namespaces, pods, workloads,
services, ingresses, persistent volumes, and persistent volume claims.

### Issue a machine token (for CI / agents)

Every non-human caller (CI scripts, agent-per-cluster deployments,
scrapers) uses a bearer token minted by an admin:

```sh
curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/admin/tokens \
  -H 'Content-Type: application/json' \
  -d '{"name":"ci-pipeline","scopes":["read","write"]}'
# -> {"token":"longue_vue_pat_....","prefix":"...","id":"...","name":"ci-pipeline",...}
```

The `token` field is the plaintext — store it in a secrets manager now;
only the argon2id hash is persisted and this is the only response that
carries the plaintext. Revoke later with
`DELETE /v1/admin/tokens/{id}` or in the admin UI.

### Verify

```sh
curl -sS -b /tmp/longue-vue.cookies http://localhost:8080/v1/namespaces | jq '.items | length'
```

### Demo data without a cluster

If you just want to explore the UI without pointing the collector at a real
cluster, the repo ships [`scripts/seed-demo.sh`](../scripts/seed-demo.sh) —
run it against a local longue-vue + Postgres (token `dev`) and it POSTs a
realistic multi-cluster inventory (prod + staging, workloads, pods,
services, a MetalLB-style ingress). See the script header for details.

## Web UI

The React SPA ships inside the same binary at `/ui/`. Port-forward the service and point a browser at `/` (it redirects to `/ui/`):

```sh
kubectl -n longue-vue-system port-forward svc/longue-vue 8080:8080 &
open http://localhost:8080/
```

Sign in with `admin` + the bootstrap password from the startup log. The
first login forces you through the password-change page; after that the
normal chrome appears (role-aware nav, sign-out button). See
[`ui/README.md`](../ui/README.md) for the page-by-page map.

## Metrics

`longue-vue` exposes a Prometheus `/metrics` endpoint on the same port as the API (default `8080`). The endpoint is unauthenticated — matching Prometheus scrape convention. Restrict access with a NetworkPolicy or a separate listener if your threat model requires it.

The Deployment carries classic `prometheus.io/scrape=true` + `prometheus.io/port=8080` + `prometheus.io/path=/metrics` annotations for in-cluster Prometheus setups. For Prometheus Operator / kube-prometheus-stack, create a `PodMonitor` or `ServiceMonitor` pointing at the `longue-vue` Service.

Exported series:

| Metric | Type | Labels |
|---|---|---|
| `longue_vue_http_requests_total` | counter | `method`, `route`, `status` |
| `longue_vue_http_request_duration_seconds` | histogram | `method`, `route` |
| `longue_vue_collector_upserted_total` | counter | `cluster`, `resource` |
| `longue_vue_collector_reconciled_total` | counter | `cluster`, `resource` |
| `longue_vue_collector_errors_total` | counter | `cluster`, `resource`, `phase` |
| `longue_vue_collector_last_poll_timestamp_seconds` | gauge | `cluster`, `resource` |
| `longue_vue_build_info` | gauge | `version`, `go_version` |

`phase` on `errors_total` is one of `list` / `upsert` / `reconcile` / `lookup`. `resource` is one of `version` / `cluster` / `nodes` / `namespaces` / `pods` / `workloads` / `services` / `ingresses` / `persistentvolumes` / `persistentvolumeclaims` / `replicasets`. Plus the default `go_*` and `process_*` collectors from `client_golang`.

A simple freshness alert:

```
time() - longue_vue_collector_last_poll_timestamp_seconds{resource="nodes"} > 600
```

## RBAC scope

`rbac.yaml` grants strictly `list` on the K8s resource kinds longue-vue currently ingests:

- `nodes`, `namespaces`, `pods`, `services`, `persistentvolumes`, `persistentvolumeclaims` (core API group)
- `deployments`, `statefulsets`, `daemonsets`, `replicasets` (`apps` API group — `replicasets` is needed to walk the Pod → ReplicaSet → Deployment ownerReference chain)
- `ingresses` (`networking.k8s.io` API group)

No `get`, no `watch`, no access to Secret / ConfigMap contents, no write of any kind. longue-vue is read-only by design — the CMDB is a cartography tool, never a controller.

When new entity kinds land in the collector, this file grows a line; there's a one-to-one mapping between the ingestion passes in `internal/collector/` and the `rules` entries here.

## Multi-cluster setup

To catalogue multiple clusters from a single longue-vue (per ADR-0005):

1. Create a Secret in `longue-vue-system` with each cluster's kubeconfig as a distinct key:

   ```sh
   kubectl -n longue-vue-system create secret generic longue-vue-kubeconfigs \
     --from-file=prod.yaml=/path/to/prod-kubeconfig \
     --from-file=staging.yaml=/path/to/staging-kubeconfig
   ```

2. Mount the Secret into the longue-vue pod:

   ```yaml
   volumes:
     - name: kubeconfigs
       secret:
         secretName: longue-vue-kubeconfigs
   containers:
     - name: longue-vue
       volumeMounts:
         - name: kubeconfigs
           mountPath: /etc/longue-vue/kubeconfigs
           readOnly: true
   ```

3. Replace `LONGUE_VUE_CLUSTER_NAME` in `deployment.yaml` with `LONGUE_VUE_COLLECTOR_CLUSTERS` pointing at each mounted path:

   ```yaml
   env:
     - name: LONGUE_VUE_COLLECTOR_CLUSTERS
       value: |
         [
           {"name":"prod","kubeconfig":"/etc/longue-vue/kubeconfigs/prod.yaml"},
           {"name":"staging","kubeconfig":"/etc/longue-vue/kubeconfigs/staging.yaml"},
           {"name":"in-cluster","kubeconfig":""}
         ]
   ```

   The empty `kubeconfig` entry falls back to the pod's in-cluster ServiceAccount, which is already bound via `rbac.yaml` to the cluster longue-vue runs on.

4. Each kubeconfig should authenticate a ServiceAccount in *its own* cluster with the same RBAC as `rbac.yaml`. A compromise of the longue-vue pod exposes every catalogued cluster — keep the kubeconfigs read-only-scoped (ADR-0005 NEG-001).

5. **Optional:** pre-register each cluster via `POST /v1/clusters` to populate curated metadata (display name, environment, owner, criticality) before the first tick. If skipped, the collector auto-creates a minimal record (name only) on first contact (ADR-0011).

## Uninstall

```sh
kubectl delete -k deploy/
kubectl delete -f /tmp/longue-vue-credentials.yaml
kubectl delete namespace longue-vue-system    # also cleans up anything stray
```

The Postgres database is untouched by these manifests. Drop `longue-vue` (or the tables underneath it) separately if you want a clean slate.
