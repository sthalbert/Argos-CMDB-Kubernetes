# Deploy longue-vue on Kubernetes

This guide deploys longue-vue into a Kubernetes cluster using the reference Kustomize manifests in `deploy/`. longue-vue catalogues the cluster it runs on (and optionally remote clusters) via its ServiceAccount.

> **Prefer Helm?** See [Deploy with Helm](helm.md) for a one-command install with optional bundled PostgreSQL.

## Prerequisites

- A Kubernetes cluster (kind, minikube, or a production cluster).
- A PostgreSQL 14+ instance reachable from the cluster. The longue-vue pod needs `CREATE` privileges on the target database for goose migrations.
- A container image. Pre-built images are published to `ghcr.io/sthalbert/longue-vue` on every release. To build your own, see [Build the image](#build-the-image) below.
- `kubectl` configured to talk to the cluster.

## Files overview

The `deploy/` directory contains:

| File | Purpose |
|------|---------|
| `namespace.yaml` | Creates the `longue-vue-system` namespace. |
| `rbac.yaml` | ServiceAccount + ClusterRole (read-only `list` on ingested kinds) + ClusterRoleBinding. |
| `deployment.yaml` | Single-replica longue-vue with probes, resource limits, non-root security context. |
| `service.yaml` | ClusterIP Service on port 8080. |
| `kustomization.yaml` | Ties everything together for `kubectl apply -k`. |
| `secrets.example.yaml` | Template for credentials -- copy, fill, apply separately. |

The Secret is intentionally excluded from the Kustomization to prevent accidental deployment with placeholder values.

## Build the image

```bash
make docker-build    # tags longue-vue:dev
```

Load it into your cluster:

```bash
# kind
kind load docker-image longue-vue:dev --name <your-kind-cluster>

# minikube
minikube image load longue-vue:dev

# Remote registry
docker tag longue-vue:dev registry.example.com/longue-vue:dev
docker push registry.example.com/longue-vue:dev
```

Update the `image:` field in `deployment.yaml` to match.

## Step-by-step deployment

### 1. Create the credentials Secret

```bash
cp deploy/secrets.example.yaml /tmp/longue-vue-credentials.yaml
```

Edit `/tmp/longue-vue-credentials.yaml` and set at minimum:

- `LONGUE_VUE_DATABASE_URL` -- your PostgreSQL DSN.
- `LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD` -- (optional) a known password for the first admin. If omitted, longue-vue generates one and prints it to the startup log.

```bash
kubectl apply -f /tmp/longue-vue-credentials.yaml
```

### 2. Apply the manifests

```bash
kubectl apply -k deploy/
```

### 3. Watch it start

```bash
kubectl -n longue-vue-system get pods -w
kubectl -n longue-vue-system logs -l app.kubernetes.io/name=longue-vue -f
```

## First-run bootstrap

On first boot with an empty database, longue-vue creates an `admin` user and prints the password once to the startup log:

```
WARN  ========================================================================
      LONGUE-VUE FIRST-RUN BOOTSTRAP
      A default admin user has been created:
        username: admin
        password: <16 random chars, or your LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD>
        source:   generated randomly; capture now -- it won't be printed again
      This account MUST rotate its password on first login.
      ========================================================================
```

Capture this password immediately. The first login (via UI or API) requires a password change.

## Cluster registration

The collector auto-creates a minimal cluster record (name only) on first contact (ADR-0011). No manual step is required — the CMDB starts populating on the next tick after deployment.

**Optional: pre-register with curated metadata.** To populate display name, environment, or owner before the first tick:

```bash
kubectl -n longue-vue-system port-forward svc/longue-vue 8080:8080 &

# Log in and stash the session cookie.
curl -sS -c /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<your rotated password>"}'

# Pre-register the cluster with metadata.
curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{"name":"in-cluster","display_name":"Self","environment":"production"}'
```

The `name` value must match `LONGUE_VUE_CLUSTER_NAME` in the Deployment env vars (default: `in-cluster`).

On the next collector tick (default: 60 seconds), nodes, namespaces, pods, workloads, services, ingresses, PVs, and PVCs populate.

## Multi-cluster setup

To catalogue multiple clusters from a single longue-vue (per ADR-0005):

### 1. Create a kubeconfig Secret

```bash
kubectl -n longue-vue-system create secret generic longue-vue-kubeconfigs \
  --from-file=prod.yaml=/path/to/prod-kubeconfig \
  --from-file=staging.yaml=/path/to/staging-kubeconfig
```

### 2. Mount it into the longue-vue pod

Add to the Deployment:

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

### 3. Switch to LONGUE_VUE_COLLECTOR_CLUSTERS

Replace `LONGUE_VUE_CLUSTER_NAME` in the Deployment env with:

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

The empty `kubeconfig` entry falls back to the pod's in-cluster ServiceAccount.

### 4. RBAC on each remote cluster

Each kubeconfig must authenticate a ServiceAccount with the same read-only RBAC as `deploy/rbac.yaml` on its own cluster. The ClusterRole grants `list` on:

- Core: `nodes`, `namespaces`, `pods`, `services`, `persistentvolumes`, `persistentvolumeclaims`
- Apps: `deployments`, `statefulsets`, `daemonsets`, `replicasets`
- Networking: `ingresses`

No `get`, no `watch`, no write. longue-vue is read-only by design.

### 5. Register each cluster

```bash
curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{"name":"prod","display_name":"Production","environment":"production"}'

curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{"name":"staging","display_name":"Staging","environment":"staging"}'
```

## OIDC setup (optional)

To let users federate from your Identity Provider:

1. Register longue-vue as an application at the IdP. Set the redirect URI to `https://<longue-vue-host>/v1/auth/oidc/callback`. Grant types: `authorization_code`. Request scopes: `openid email profile`.

2. Add the OIDC variables to the credentials Secret:

```yaml
stringData:
  LONGUE_VUE_OIDC_ISSUER: "https://idp.example.com/realms/longue-vue"
  LONGUE_VUE_OIDC_CLIENT_ID: "longue-vue"
  LONGUE_VUE_OIDC_CLIENT_SECRET: "your-client-secret"
  LONGUE_VUE_OIDC_REDIRECT_URL: "https://longue-vue.example.com/v1/auth/oidc/callback"
  # Optional:
  LONGUE_VUE_OIDC_LABEL: "Sign in with Keycloak"
  LONGUE_VUE_OIDC_SCOPES: "openid,email,profile"
```

3. Restart longue-vue. It fetches the issuer's discovery document on boot and fails loudly if unreachable.

First-time OIDC users land as role `viewer`. An admin promotes them through the admin panel at `/ui/admin/users`.

See [Authentication](../authentication.md) for full details.

## Verify

```bash
# Check the health endpoint.
curl -sS http://localhost:8080/healthz | jq .

# Count namespaces (requires auth).
curl -sS -b /tmp/longue-vue.cookies http://localhost:8080/v1/namespaces | jq '.items | length'

# Check metrics.
curl -sS http://localhost:8080/metrics | grep longue_vue_collector_last_poll
```

## Uninstall

```bash
kubectl delete -k deploy/
kubectl delete -f /tmp/longue-vue-credentials.yaml
kubectl delete namespace longue-vue-system
```

The PostgreSQL database is untouched. Drop the `longue-vue` database separately if you want a clean slate.
