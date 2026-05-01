# Getting Started

This guide walks you from zero to a working longue-vue installation with data flowing in. By the end you will have longue-vue running, the web UI accessible, and at least one cluster registered.

## Prerequisites

Regardless of the path you choose below, you need:

- **Docker** (for the PostgreSQL container; Docker Desktop or a standalone engine both work)
- **Git** (to clone the repository)

For the "build from source" path you also need:

- **Go** (version pinned in `go.mod` -- currently 1.25+)
- **Node.js 22+** and **npm** (for the UI build)
- **golangci-lint** (optional, for `make lint`)

## Option A -- Docker quick start (5 minutes)

### 1. Start PostgreSQL

```bash
docker run -d --rm --name longue-vue-pg \
  -e POSTGRES_PASSWORD=longue-vue -e POSTGRES_DB=longue-vue \
  -p 5432:5432 postgres:16-alpine
```

### 2. Build and run longue-vue

```bash
git clone https://github.com/sthalbert/longue-vue.git
cd longue-vue

make ui-install    # first time only -- installs npm deps
make ui-build      # produces ui/dist/ for embedding
make build         # produces bin/longue-vue

LONGUE_VUE_DATABASE_URL="postgres://postgres:longue-vue@localhost:5432/longue-vue?sslmode=disable" \
  LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD="changeme-on-first-login" \
  ./bin/longue-vue
```

longue-vue runs database migrations automatically on startup, then starts listening on `:8080`.

### 3. Open the UI and sign in

```
http://localhost:8080/
```

The root URL redirects to `/ui/`. Sign in with:

- **Username:** `admin`
- **Password:** `changeme-on-first-login` (or whatever you set above)

The first login forces you to choose a new password -- this is the `must_change_password` enforcement. Pick something strong, then the dashboard appears.

### 4. Seed demo data (optional)

If you do not have a Kubernetes cluster handy, the demo seed script populates a realistic multi-cluster inventory so the UI has something to show:

```bash
# First, mint a token for the script.
# Log in via curl, then create a token:
curl -sS -c /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<your new password>"}'

curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/admin/tokens \
  -H 'Content-Type: application/json' \
  -d '{"name":"seed","scopes":["read","write","delete"]}'
# Copy the "token" value from the response.

LONGUE_VUE_URL=http://localhost:8080 LONGUE_VUE_TOKEN=longue_vue_pat_... ./scripts/seed-demo.sh
```

Refresh the UI -- you should see clusters, namespaces, workloads, pods, services, and ingresses.

## Option B -- Binary from source (no Docker for longue-vue)

If you prefer to skip Docker entirely for the longue-vue binary (you still need PostgreSQL somewhere):

```bash
git clone https://github.com/sthalbert/longue-vue.git
cd longue-vue

make ui-install
make ui-build
make build
```

Point longue-vue at any PostgreSQL 14+ instance:

```bash
export LONGUE_VUE_DATABASE_URL="postgres://user:pass@pg-host:5432/longue-vue?sslmode=require"
export LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD="changeme-on-first-login"
./bin/longue-vue
```

If you only need the API (no web UI), skip the Node toolchain entirely:

```bash
make build-noui
```

The `/ui/` path returns 404 in this mode; the REST API works normally.

## Option C -- Helm install on Kubernetes

If you have a Kubernetes cluster and Helm 3 installed, this is the fastest path to a production-like setup:

```bash
helm install longue-vue charts/longue-vue \
  -n longue-vue-system --create-namespace \
  --set longue-vue.bootstrapAdminPassword="changeme-on-first-login"
```

The chart deploys longue-vue and a PostgreSQL instance (`postgres:17-alpine`). Retrieve the admin password from the logs if you did not set one:

```bash
kubectl -n longue-vue-system logs -l app.kubernetes.io/name=longue-vue | grep "LONGUE-VUE FIRST-RUN"
```

Access the UI:

```bash
kubectl -n longue-vue-system port-forward svc/longue-vue 8080:8080
open http://localhost:8080/
```

See [Deploy with Helm](deployment/helm.md) for the full values reference, external database setup, OIDC, Ingress, and ServiceMonitor configuration.

## First steps after login

### Change the admin password

This happens automatically on first login in the UI. If you are scripting against the API:

```bash
curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/auth/change-password \
  -H 'Content-Type: application/json' \
  -d '{"current_password":"changeme-on-first-login","new_password":"a-strong-passphrase"}'
```

### Cluster registration

The collector auto-creates a minimal cluster record (name only) on first contact if the cluster doesn't exist (ADR-0011). No manual step is required to start ingesting.

**Optional: pre-register with curated metadata.** To populate display name, environment, or owner before the first tick:

```bash
curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{"name":"my-cluster","display_name":"My Cluster","environment":"dev"}'
```

The `name` field must match the `LONGUE_VUE_CLUSTER_NAME` (or the `name` in `LONGUE_VUE_COLLECTOR_CLUSTERS`) that the collector uses.

### Enable the pull collector

Add these environment variables when starting longue-vue:

```bash
LONGUE_VUE_COLLECTOR_ENABLED=true \
LONGUE_VUE_CLUSTER_NAME=my-cluster \
LONGUE_VUE_DATABASE_URL="postgres://..." \
  ./bin/longue-vue
```

This uses the in-cluster ServiceAccount by default. For remote clusters, mount kubeconfig files from a Kubernetes Secret and use `LONGUE_VUE_COLLECTOR_CLUSTERS` — see [How to securely provide kubeconfigs](how-to-secure-kubeconfig.md).

On the next tick (default: 60 seconds) the collector populates nodes, namespaces, pods, workloads, services, ingresses, persistent volumes, and PVCs.

### Verify data appears

```bash
curl -sS -b /tmp/longue-vue.cookies http://localhost:8080/v1/namespaces | jq '.items | length'
```

Or simply refresh the UI -- the cluster detail page shows all discovered resources.

### Mint a machine token

For CI pipelines, automation scripts, or the push-mode collector, create a bearer token:

```bash
curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/admin/tokens \
  -H 'Content-Type: application/json' \
  -d '{"name":"ci-pipeline","scopes":["read","write"]}'
```

The response contains the plaintext token exactly once -- store it in a secrets manager immediately. Subsequent API calls use it as:

```bash
curl -H "Authorization: Bearer longue_vue_pat_..." http://localhost:8080/v1/clusters
```

## Next steps

- [Configuration reference](configuration.md) -- all environment variables for longue-vue and longue-vue-collector.
- [Deploy with Helm](deployment/helm.md) -- one-command Kubernetes install with optional bundled PostgreSQL.
- [Deploy with Kustomize](deployment/kubernetes.md) -- production deployment with plain manifests.
- [Push collector for air-gapped clusters](deployment/push-collector.md) -- deploy longue-vue-collector.
- [Authentication guide](authentication.md) -- OIDC, roles, tokens.
- [API reference](api-reference.md) -- every endpoint with curl examples.
