# Deploy argosd on Kubernetes

This guide deploys argosd into a Kubernetes cluster using the reference Kustomize manifests in `deploy/`. argosd catalogues the cluster it runs on (and optionally remote clusters) via its ServiceAccount.

> **Prefer Helm?** See [Deploy with Helm](helm.md) for a one-command install with optional bundled PostgreSQL.

## Prerequisites

- A Kubernetes cluster (kind, minikube, or a production cluster).
- A PostgreSQL 14+ instance reachable from the cluster. The argosd pod needs `CREATE` privileges on the target database for goose migrations.
- A container image. Pre-built images are published to `ghcr.io/sthalbert/argos` on every release. To build your own, see [Build the image](#build-the-image) below.
- `kubectl` configured to talk to the cluster.

## Files overview

The `deploy/` directory contains:

| File | Purpose |
|------|---------|
| `namespace.yaml` | Creates the `argos-system` namespace. |
| `rbac.yaml` | ServiceAccount + ClusterRole (read-only `list` on ingested kinds) + ClusterRoleBinding. |
| `deployment.yaml` | Single-replica argosd with probes, resource limits, non-root security context. |
| `service.yaml` | ClusterIP Service on port 8080. |
| `kustomization.yaml` | Ties everything together for `kubectl apply -k`. |
| `secrets.example.yaml` | Template for credentials -- copy, fill, apply separately. |

The Secret is intentionally excluded from the Kustomization to prevent accidental deployment with placeholder values.

## Build the image

```bash
make docker-build    # tags argos:dev
```

Load it into your cluster:

```bash
# kind
kind load docker-image argos:dev --name <your-kind-cluster>

# minikube
minikube image load argos:dev

# Remote registry
docker tag argos:dev registry.example.com/argos:dev
docker push registry.example.com/argos:dev
```

Update the `image:` field in `deployment.yaml` to match.

## Step-by-step deployment

### 1. Create the credentials Secret

```bash
cp deploy/secrets.example.yaml /tmp/argos-credentials.yaml
```

Edit `/tmp/argos-credentials.yaml` and set at minimum:

- `ARGOS_DATABASE_URL` -- your PostgreSQL DSN.
- `ARGOS_BOOTSTRAP_ADMIN_PASSWORD` -- (optional) a known password for the first admin. If omitted, argosd generates one and prints it to the startup log.

```bash
kubectl apply -f /tmp/argos-credentials.yaml
```

### 2. Apply the manifests

```bash
kubectl apply -k deploy/
```

### 3. Watch it start

```bash
kubectl -n argos-system get pods -w
kubectl -n argos-system logs -l app.kubernetes.io/name=argos -f
```

## First-run bootstrap

On first boot with an empty database, argosd creates an `admin` user and prints the password once to the startup log:

```
WARN  ========================================================================
      ARGOS FIRST-RUN BOOTSTRAP
      A default admin user has been created:
        username: admin
        password: <16 random chars, or your ARGOS_BOOTSTRAP_ADMIN_PASSWORD>
        source:   generated randomly; capture now -- it won't be printed again
      This account MUST rotate its password on first login.
      ========================================================================
```

Capture this password immediately. The first login (via UI or API) requires a password change.

## Register the cluster

The collector will not ingest data until the cluster is registered. Port-forward the service and register:

```bash
kubectl -n argos-system port-forward svc/argosd 8080:8080 &

# Log in and stash the session cookie.
curl -sS -c /tmp/argos.cookies -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<your rotated password>"}'

# Register the cluster.
curl -sS -b /tmp/argos.cookies -X POST http://localhost:8080/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{"name":"in-cluster","display_name":"Self","environment":"production"}'
```

The `name` value must match `ARGOS_CLUSTER_NAME` in the Deployment env vars (default: `in-cluster`).

On the next collector tick (default: 60 seconds), nodes, namespaces, pods, workloads, services, ingresses, PVs, and PVCs populate.

## Multi-cluster setup

To catalogue multiple clusters from a single argosd (per ADR-0005):

### 1. Create a kubeconfig Secret

```bash
kubectl -n argos-system create secret generic argos-kubeconfigs \
  --from-file=prod.yaml=/path/to/prod-kubeconfig \
  --from-file=staging.yaml=/path/to/staging-kubeconfig
```

### 2. Mount it into the argosd pod

Add to the Deployment:

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

### 3. Switch to ARGOS_COLLECTOR_CLUSTERS

Replace `ARGOS_CLUSTER_NAME` / `ARGOS_KUBECONFIG` in the Deployment env with:

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

The empty `kubeconfig` entry falls back to the pod's in-cluster ServiceAccount.

### 4. RBAC on each remote cluster

Each kubeconfig must authenticate a ServiceAccount with the same read-only RBAC as `deploy/rbac.yaml` on its own cluster. The ClusterRole grants `list` on:

- Core: `nodes`, `namespaces`, `pods`, `services`, `persistentvolumes`, `persistentvolumeclaims`
- Apps: `deployments`, `statefulsets`, `daemonsets`, `replicasets`
- Networking: `ingresses`

No `get`, no `watch`, no write. argosd is read-only by design.

### 5. Register each cluster

```bash
curl -sS -b /tmp/argos.cookies -X POST http://localhost:8080/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{"name":"prod","display_name":"Production","environment":"production"}'

curl -sS -b /tmp/argos.cookies -X POST http://localhost:8080/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{"name":"staging","display_name":"Staging","environment":"staging"}'
```

## OIDC setup (optional)

To let users federate from your Identity Provider:

1. Register argosd as an application at the IdP. Set the redirect URI to `https://<argos-host>/v1/auth/oidc/callback`. Grant types: `authorization_code`. Request scopes: `openid email profile`.

2. Add the OIDC variables to the credentials Secret:

```yaml
stringData:
  ARGOS_OIDC_ISSUER: "https://idp.example.com/realms/argos"
  ARGOS_OIDC_CLIENT_ID: "argos"
  ARGOS_OIDC_CLIENT_SECRET: "your-client-secret"
  ARGOS_OIDC_REDIRECT_URL: "https://argos.example.com/v1/auth/oidc/callback"
  # Optional:
  ARGOS_OIDC_LABEL: "Sign in with Keycloak"
  ARGOS_OIDC_SCOPES: "openid,email,profile"
```

3. Restart argosd. It fetches the issuer's discovery document on boot and fails loudly if unreachable.

First-time OIDC users land as role `viewer`. An admin promotes them through the admin panel at `/ui/admin/users`.

See [Authentication](../authentication.md) for full details.

## Verify

```bash
# Check the health endpoint.
curl -sS http://localhost:8080/healthz | jq .

# Count namespaces (requires auth).
curl -sS -b /tmp/argos.cookies http://localhost:8080/v1/namespaces | jq '.items | length'

# Check metrics.
curl -sS http://localhost:8080/metrics | grep argos_collector_last_poll
```

## Uninstall

```bash
kubectl delete -k deploy/
kubectl delete -f /tmp/argos-credentials.yaml
kubectl delete namespace argos-system
```

The PostgreSQL database is untouched. Drop the `argos` database separately if you want a clean slate.
