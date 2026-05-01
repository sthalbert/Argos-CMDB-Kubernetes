# Deploy longue-vue with Helm

This guide deploys longue-vue on Kubernetes using the Helm chart in `charts/longue-vue/`. The chart bundles an optional PostgreSQL instance (official `postgres:17-alpine` image) so you can get a fully working longue-vue with a single `helm install` -- no external operator or dependency required.

> **Prefer Kustomize?** See [Deploy on Kubernetes](kubernetes.md) for the plain-manifest approach.

## Prerequisites

- A Kubernetes cluster (kind, minikube, OrbStack, or a production cluster).
- [Helm 3](https://helm.sh/docs/intro/install/) installed locally.
- `kubectl` configured to talk to the cluster.

Pre-built images are published to `ghcr.io/sthalbert/longue-vue` on every release. To build your own, see [Build the image](#build-the-image) below.

## Install with bundled PostgreSQL

The simplest path -- one command deploys longue-vue and PostgreSQL together:

```bash
helm install longue-vue charts/longue-vue \
  -n longue-vue-system --create-namespace
```

longue-vue starts, runs database migrations automatically, and bootstraps an admin user. Retrieve the password from the logs:

```bash
kubectl -n longue-vue-system logs -l app.kubernetes.io/name=longue-vue | grep "LONGUE-VUE FIRST-RUN"
```

To set a predictable password instead:

```bash
helm install longue-vue charts/longue-vue \
  -n longue-vue-system --create-namespace \
  --set longue-vue.bootstrapAdminPassword="my-strong-passphrase"
```

## Install with external PostgreSQL

If you already have a PostgreSQL instance (managed service, CloudNativePG, etc.), disable the bundled one:

```bash
helm install longue-vue charts/longue-vue \
  -n longue-vue-system --create-namespace \
  --set postgresql.enabled=false \
  --set externalDatabase.url="postgres://longue-vue:secret@pg.prod.svc:5432/longue-vue?sslmode=require"
```

The chart validates that `externalDatabase.url` is set when `postgresql.enabled=false` -- it refuses to render otherwise.

## Use an existing Secret

If you manage secrets externally (Vault, External Secrets Operator, SealedSecrets), point the chart at your Secret instead of generating one:

```bash
helm install longue-vue charts/longue-vue \
  -n longue-vue-system --create-namespace \
  --set existingSecret=my-longue-vue-credentials \
  --set postgresql.enabled=false
```

Your Secret must contain at least `LONGUE_VUE_DATABASE_URL`. It can also include `LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD`, `LONGUE_VUE_OIDC_*` variables, or any other `LONGUE_VUE_*` key.

## Enable the collector

To have longue-vue catalogue the cluster it runs on:

```bash
helm install longue-vue charts/longue-vue \
  -n longue-vue-system --create-namespace \
  --set collector.enabled=true \
  --set collector.clusterName=my-cluster
```

The chart creates a ClusterRole granting read-only `list` on the Kubernetes resources the collector ingests. The ServiceAccount is bound automatically.

The collector auto-creates the cluster record on first contact (ADR-0011). To populate curated metadata (display name, environment, owner) before the first tick, optionally pre-register:

```bash
kubectl -n longue-vue-system port-forward svc/longue-vue 8080:8080 &
curl -X POST http://localhost:8080/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{"name":"my-cluster","display_name":"My Cluster","environment":"prod"}'
```

## Enable OIDC

```bash
helm install longue-vue charts/longue-vue \
  -n longue-vue-system --create-namespace \
  --set oidc.enabled=true \
  --set oidc.issuer="https://accounts.example.com" \
  --set oidc.clientId="longue-vue" \
  --set oidc.clientSecret="s3cret" \
  --set oidc.redirectUrl="https://longue-vue.example.com/v1/auth/oidc/callback"
```

See [Authentication](../authentication.md) for details on OIDC setup and shadow user behavior.

## Expose with Ingress

```bash
helm install longue-vue charts/longue-vue \
  -n longue-vue-system --create-namespace \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set 'ingress.hosts[0].host=longue-vue.example.com' \
  --set 'ingress.hosts[0].paths[0].path=/' \
  --set 'ingress.hosts[0].paths[0].pathType=Prefix' \
  --set 'ingress.tls[0].secretName=longue-vue-tls' \
  --set 'ingress.tls[0].hosts[0]=longue-vue.example.com'
```

## Enable ServiceMonitor

For Prometheus Operator environments:

```bash
helm install longue-vue charts/longue-vue \
  -n longue-vue-system --create-namespace \
  --set metrics.serviceMonitor.enabled=true \
  --set metrics.serviceMonitor.interval=30s
```

See [Monitoring](../monitoring.md) for the full metrics reference and alert examples.

## Build the image

Pre-built images are published to GHCR on every release. For local development or custom builds:

```bash
make docker-build    # tags longue-vue:dev

# Load into local clusters:
kind load docker-image longue-vue:dev --name <cluster>
# or: minikube image load longue-vue:dev

# Use the local image:
helm install longue-vue charts/longue-vue \
  -n longue-vue-system --create-namespace \
  --set image.repository=longue-vue \
  --set image.tag=dev \
  --set image.pullPolicy=Never
```

## Values reference

The table below lists the most common values. See [`charts/longue-vue/values.yaml`](../../charts/longue-vue/values.yaml) for the complete file with inline comments.

### Core

| Value | Default | Description |
|-------|---------|-------------|
| `replicaCount` | `1` | Must be 1 (single-writer constraint). |
| `image.repository` | `ghcr.io/sthalbert/longue-vue` | Container image repository. |
| `image.tag` | `""` (appVersion) | Image tag override. |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |

### longue-vue

| Value | Default | Description |
|-------|---------|-------------|
| `longue-vue.addr` | `":8080"` | HTTP listen address. |
| `longue-vue.autoMigrate` | `true` | Run DB migrations on startup. |
| `longue-vue.shutdownTimeout` | `"15s"` | Graceful shutdown budget. |
| `longue-vue.bootstrapAdminPassword` | `""` | Admin password (empty = random). |
| `longue-vue.sessionSecureCookie` | `"auto"` | Cookie Secure flag: auto/always/never. |

### Collector

| Value | Default | Description |
|-------|---------|-------------|
| `collector.enabled` | `false` | Enable the pull-mode collector. |
| `collector.clusterName` | `"in-cluster"` | Single-cluster name (ignored when `clusters` is non-empty). |
| `collector.clusters` | `[]` | Multi-cluster list. Each entry: `{name, kubeconfig}`. |
| `collector.kubeconfigSecret` | `""` | Name of an existing Secret containing kubeconfig files. Mounted read-only at `/etc/longue-vue/kubeconfigs/`. See [How to securely provide kubeconfigs](../how-to-secure-kubeconfig.md). |
| `collector.interval` | `"5m"` | Poll interval. |
| `collector.fetchTimeout` | `"10s"` | Per-poll K8s API timeout. |
| `collector.reconcile` | `true` | Delete stale rows. |

### OIDC

| Value | Default | Description |
|-------|---------|-------------|
| `oidc.enabled` | `false` | Enable OIDC sign-in. |
| `oidc.issuer` | `""` | OIDC issuer URL. |
| `oidc.clientId` | `""` | OAuth2 client ID. |
| `oidc.clientSecret` | `""` | OAuth2 client secret. |
| `oidc.redirectUrl` | `""` | Authorization callback URL. |
| `oidc.scopes` | `"openid,email,profile"` | OAuth2 scopes. |
| `oidc.label` | `"OIDC"` | Login button text. |

### Database

| Value | Default | Description |
|-------|---------|-------------|
| `postgresql.enabled` | `true` | Deploy bundled PostgreSQL (`postgres:17-alpine`). |
| `postgresql.image.repository` | `postgres` | PostgreSQL image. |
| `postgresql.image.tag` | `"17-alpine"` | PostgreSQL image tag. |
| `postgresql.auth.username` | `longue-vue` | PG username. |
| `postgresql.auth.password` | `longue-vue` | PG password. |
| `postgresql.auth.database` | `longue-vue` | PG database name. |
| `postgresql.persistence.size` | `5Gi` | PVC size. |
| `postgresql.persistence.storageClass` | `""` | Storage class (empty = cluster default). |
| `postgresql.resources` | 100m-500m CPU, 128-512Mi | PG resource requests/limits. |
| `externalDatabase.url` | `""` | External PG DSN (when bundled PG disabled). |
| `existingSecret` | `""` | Use an existing Secret for credentials. |

### Networking

| Value | Default | Description |
|-------|---------|-------------|
| `service.type` | `ClusterIP` | Service type. |
| `service.port` | `8080` | Service port. |
| `ingress.enabled` | `false` | Create an Ingress resource. |
| `metrics.serviceMonitor.enabled` | `false` | Create a ServiceMonitor. |

### Security

| Value | Default | Description |
|-------|---------|-------------|
| `rbac.create` | `true` | Create ClusterRole and binding. |
| `serviceAccount.create` | `true` | Create a ServiceAccount. |
| `podSecurityContext.runAsUser` | `65532` | Non-root UID (distroless). |

## Upgrade

```bash
helm upgrade longue-vue charts/longue-vue -n longue-vue-system
```

The Deployment includes a `checksum/secret` annotation, so any change to Secret values triggers a rolling restart automatically.

## Uninstall

```bash
helm uninstall longue-vue -n longue-vue-system
kubectl delete namespace longue-vue-system
```

If you used the bundled PostgreSQL, the PVC persists by default. Delete it manually if you want a clean slate:

```bash
kubectl -n longue-vue-system delete pvc data-longue-vue-postgresql-0
```
