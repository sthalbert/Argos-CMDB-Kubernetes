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
| `rbac.yaml` | ServiceAccount + ClusterRole (`list` on the kinds the collector ingests — see [RBAC scope](#rbac-scope)) + ClusterRoleBinding |
| `deployment.yaml` | Single-replica `argosd` with probes, resource limits, non-root security context |
| `service.yaml` | ClusterIP Service on port 8080 |
| `kustomization.yaml` | Applies everything above with one `kubectl apply -k` |
| `secrets.example.yaml` | **Template only — copy, fill in, apply separately** |

The Secret is intentionally out of the Kustomization so the example file can't be accidentally deployed with placeholder values.

## Image

The [`Dockerfile`](../Dockerfile) builds a distroless static image. Pre-built images are published to `ghcr.io/sthalbert/argos` (and `ghcr.io/sthalbert/argos-collector`) on every tagged release. To build locally for a smoke test:

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
# 1. Credentials (database only; auth tokens are issued by an admin in
#    the UI now, not injected via env vars — per ADR-0007).
cp deploy/secrets.example.yaml /tmp/argos-credentials.yaml
# ...edit /tmp/argos-credentials.yaml — set ARGOS_DATABASE_URL...
kubectl apply -f /tmp/argos-credentials.yaml

# 2. Everything else.
kubectl apply -k deploy/

# 3. Watch it come up.
kubectl -n argos-system get pods -w
kubectl -n argos-system logs -l app.kubernetes.io/name=argos -f
```

### First-run bootstrap

On first boot with an empty database, argosd creates an `admin` user and
prints its password **once** to the startup log inside a banner:

```
WARN  ========================================================================
      ARGOS FIRST-RUN BOOTSTRAP
      A default admin user has been created:
        username: admin
        password: <16 random chars, or whatever you set via env>
        source:   generated randomly; capture now — it won't be printed again
      This account MUST rotate its password on first login.
      ========================================================================
```

For a predictable password, set `ARGOS_BOOTSTRAP_ADMIN_PASSWORD` on the
Deployment before the first start. It's only consulted when no admin
user exists yet — safe to leave set across restarts.

### Optional: enable OIDC sign-in

Set the `ARGOS_OIDC_*` variables in `secrets.example.yaml` to let users
federate from your IdP instead of (or alongside) local passwords. The
local `admin` bootstrap still happens — OIDC is additive, not a
replacement — so you always have a break-glass login.

What the operator needs to do:

1. Register argosd as an application at the IdP. Redirect URI
   must be `https://<argos-host>/v1/auth/oidc/callback`; grant types:
   `authorization_code`; request `openid email profile` scopes.
2. Fill in `ARGOS_OIDC_ISSUER`, `ARGOS_OIDC_CLIENT_ID`,
   `ARGOS_OIDC_CLIENT_SECRET`, `ARGOS_OIDC_REDIRECT_URL` in the Secret.
   Optional: `ARGOS_OIDC_LABEL` (button text), `ARGOS_OIDC_SCOPES`.
3. Restart argosd. On boot it fetches the issuer's discovery document
   and fails loudly if unreachable — misconfiguration surfaces at
   startup, not on a user's first login attempt.

First-time OIDC users land as role `viewer` (authorization is not
claim-driven — ADR-0007). An `admin` promotes them through the admin
panel at `/ui/admin/users` as needed.

### Register a cluster

The CMDB requires explicit cluster registration before the collector writes
anything (per ADR-0005). Register it through the admin session — first
log in with the bootstrap password, then:

```sh
# Port-forward the argosd service for a local curl.
kubectl -n argos-system port-forward svc/argosd 8080:8080 &

# Log in, stash the session cookie.
curl -sS -c /tmp/argos.cookies -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<your new rotated password>"}'

# Register the cluster.
curl -sS -b /tmp/argos.cookies -X POST http://localhost:8080/v1/clusters \
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
curl -sS -b /tmp/argos.cookies -X POST http://localhost:8080/v1/admin/tokens \
  -H 'Content-Type: application/json' \
  -d '{"name":"ci-pipeline","scopes":["read","write"]}'
# -> {"token":"argos_pat_....","prefix":"...","id":"...","name":"ci-pipeline",...}
```

The `token` field is the plaintext — store it in a secrets manager now;
only the argon2id hash is persisted and this is the only response that
carries the plaintext. Revoke later with
`DELETE /v1/admin/tokens/{id}` or in the admin UI.

### Verify

```sh
curl -sS -b /tmp/argos.cookies http://localhost:8080/v1/namespaces | jq '.items | length'
```

### Demo data without a cluster

If you just want to explore the UI without pointing the collector at a real
cluster, the repo ships [`scripts/seed-demo.sh`](../scripts/seed-demo.sh) —
run it against a local argosd + Postgres (token `dev`) and it POSTs a
realistic multi-cluster inventory (prod + staging, workloads, pods,
services, a MetalLB-style ingress). See the script header for details.

## Web UI

The React SPA ships inside the same binary at `/ui/`. Port-forward the service and point a browser at `/` (it redirects to `/ui/`):

```sh
kubectl -n argos-system port-forward svc/argosd 8080:8080 &
open http://localhost:8080/
```

Sign in with `admin` + the bootstrap password from the startup log. The
first login forces you through the password-change page; after that the
normal chrome appears (role-aware nav, sign-out button). See
[`ui/README.md`](../ui/README.md) for the page-by-page map.

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

`phase` on `errors_total` is one of `list` / `upsert` / `reconcile` / `lookup`. `resource` is one of `version` / `cluster` / `nodes` / `namespaces` / `pods` / `workloads` / `services` / `ingresses` / `persistentvolumes` / `persistentvolumeclaims` / `replicasets`. Plus the default `go_*` and `process_*` collectors from `client_golang`.

A simple freshness alert:

```
time() - argos_collector_last_poll_timestamp_seconds{resource="nodes"} > 600
```

## RBAC scope

`rbac.yaml` grants strictly `list` on the K8s resource kinds argosd currently ingests:

- `nodes`, `namespaces`, `pods`, `services`, `persistentvolumes`, `persistentvolumeclaims` (core API group)
- `deployments`, `statefulsets`, `daemonsets`, `replicasets` (`apps` API group — `replicasets` is needed to walk the Pod → ReplicaSet → Deployment ownerReference chain)
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
