# Getting Started

This guide walks you from zero to a working Argos installation with data flowing in. By the end you will have argosd running, the web UI accessible, and at least one cluster registered.

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
docker run -d --rm --name argos-pg \
  -e POSTGRES_PASSWORD=argos -e POSTGRES_DB=argos \
  -p 5432:5432 postgres:16-alpine
```

### 2. Build and run argosd

```bash
git clone https://github.com/sthalbert/argos.git
cd argos

make ui-install    # first time only -- installs npm deps
make ui-build      # produces ui/dist/ for embedding
make build         # produces bin/argosd

ARGOS_DATABASE_URL="postgres://postgres:argos@localhost:5432/argos?sslmode=disable" \
  ARGOS_BOOTSTRAP_ADMIN_PASSWORD="changeme-on-first-login" \
  ./bin/argosd
```

argosd runs database migrations automatically on startup, then starts listening on `:8080`.

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
curl -sS -c /tmp/argos.cookies -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<your new password>"}'

curl -sS -b /tmp/argos.cookies -X POST http://localhost:8080/v1/admin/tokens \
  -H 'Content-Type: application/json' \
  -d '{"name":"seed","scopes":["read","write","delete"]}'
# Copy the "token" value from the response.

ARGOS_URL=http://localhost:8080 ARGOS_TOKEN=argos_pat_... ./scripts/seed-demo.sh
```

Refresh the UI -- you should see clusters, namespaces, workloads, pods, services, and ingresses.

## Option B -- Binary from source (no Docker for argosd)

If you prefer to skip Docker entirely for the argosd binary (you still need PostgreSQL somewhere):

```bash
git clone https://github.com/sthalbert/argos.git
cd argos

make ui-install
make ui-build
make build
```

Point argosd at any PostgreSQL 14+ instance:

```bash
export ARGOS_DATABASE_URL="postgres://user:pass@pg-host:5432/argos?sslmode=require"
export ARGOS_BOOTSTRAP_ADMIN_PASSWORD="changeme-on-first-login"
./bin/argosd
```

If you only need the API (no web UI), skip the Node toolchain entirely:

```bash
make build-noui
```

The `/ui/` path returns 404 in this mode; the REST API works normally.

## Option C -- Helm install on Kubernetes

If you have a Kubernetes cluster and Helm 3 installed, this is the fastest path to a production-like setup:

```bash
helm install argos charts/argos \
  -n argos-system --create-namespace \
  --set argosd.bootstrapAdminPassword="changeme-on-first-login"
```

The chart deploys argosd and a PostgreSQL instance (`postgres:17-alpine`). Retrieve the admin password from the logs if you did not set one:

```bash
kubectl -n argos-system logs -l app.kubernetes.io/name=argos | grep "ARGOS FIRST-RUN"
```

Access the UI:

```bash
kubectl -n argos-system port-forward svc/argos 8080:8080
open http://localhost:8080/
```

See [Deploy with Helm](deployment/helm.md) for the full values reference, external database setup, OIDC, Ingress, and ServiceMonitor configuration.

## First steps after login

### Change the admin password

This happens automatically on first login in the UI. If you are scripting against the API:

```bash
curl -sS -b /tmp/argos.cookies -X POST http://localhost:8080/v1/auth/change-password \
  -H 'Content-Type: application/json' \
  -d '{"current_password":"changeme-on-first-login","new_password":"a-strong-passphrase"}'
```

### Cluster registration

The collector auto-creates a minimal cluster record (name only) on first contact if the cluster doesn't exist (ADR-0011). No manual step is required to start ingesting.

**Optional: pre-register with curated metadata.** To populate display name, environment, or owner before the first tick:

```bash
curl -sS -b /tmp/argos.cookies -X POST http://localhost:8080/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{"name":"my-cluster","display_name":"My Cluster","environment":"dev"}'
```

The `name` field must match the `ARGOS_CLUSTER_NAME` (or the `name` in `ARGOS_COLLECTOR_CLUSTERS`) that the collector uses.

### Enable the pull collector

Add these environment variables when starting argosd:

```bash
ARGOS_COLLECTOR_ENABLED=true \
ARGOS_CLUSTER_NAME=my-cluster \
ARGOS_DATABASE_URL="postgres://..." \
  ./bin/argosd
```

This uses the in-cluster ServiceAccount by default. For remote clusters, mount kubeconfig files from a Kubernetes Secret and use `ARGOS_COLLECTOR_CLUSTERS` — see [How to securely provide kubeconfigs](how-to-secure-kubeconfig.md).

On the next tick (default: 60 seconds) the collector populates nodes, namespaces, pods, workloads, services, ingresses, persistent volumes, and PVCs.

### Verify data appears

```bash
curl -sS -b /tmp/argos.cookies http://localhost:8080/v1/namespaces | jq '.items | length'
```

Or simply refresh the UI -- the cluster detail page shows all discovered resources.

### Mint a machine token

For CI pipelines, automation scripts, or the push-mode collector, create a bearer token:

```bash
curl -sS -b /tmp/argos.cookies -X POST http://localhost:8080/v1/admin/tokens \
  -H 'Content-Type: application/json' \
  -d '{"name":"ci-pipeline","scopes":["read","write"]}'
```

The response contains the plaintext token exactly once -- store it in a secrets manager immediately. Subsequent API calls use it as:

```bash
curl -H "Authorization: Bearer argos_pat_..." http://localhost:8080/v1/clusters
```

## Next steps

- [Configuration reference](configuration.md) -- all environment variables for argosd and argos-collector.
- [Deploy with Helm](deployment/helm.md) -- one-command Kubernetes install with optional bundled PostgreSQL.
- [Deploy with Kustomize](deployment/kubernetes.md) -- production deployment with plain manifests.
- [Push collector for air-gapped clusters](deployment/push-collector.md) -- deploy argos-collector.
- [Authentication guide](authentication.md) -- OIDC, roles, tokens.
- [API reference](api-reference.md) -- every endpoint with curl examples.
