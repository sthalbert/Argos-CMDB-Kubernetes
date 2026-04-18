# Argos

A Configuration Management Database (CMDB) for Kubernetes environments, aligned
with the [ANSSI SecNumCloud](https://cyber.gouv.fr/enjeux-technologiques/cloud/)
(SNC) qualification framework. Argos polls one or more Kubernetes clusters on
an interval, mirrors the result into PostgreSQL, and exposes both a REST API
and a web UI so auditors can see the cartography and operators can annotate
assets with information Kubernetes doesn't carry (business owner, criticality,
runbooks).

Argos replaces the Kubernetes-scoped portion of [Mercator](https://github.com/dbsystel/mercator).
See [ADR-0001](docs/adr/adr-0001-cmdb-for-snc-using-kube.md) for the foundational
architectural decision.

**Status:** alpha. Data model and HTTP contract are stable enough to run
against real clusters; expect additive changes until 1.0.

## What ships today

- **Polling collector** against one or more Kubernetes clusters, covering
  Clusters, Nodes, Namespaces, Workloads (Deployment / StatefulSet / DaemonSet,
  polymorphic per [ADR-0003](docs/adr/adr-0003-workload-polymorphism.md)), Pods
  (with `workload_id` back-reference walked through the ownerReference chain),
  Services, Ingresses, PersistentVolumes, and PersistentVolumeClaims (with
  `bound_volume_id` FK to the backing PV).
- **Reconciliation**: rows that vanish from the live listing are deleted so
  the CMDB mirrors the cluster — required for SNC cartography fidelity.
  Toggleable; disabled reverts to pure append behaviour.
- **Multi-cluster**: one collector goroutine per entry in
  `ARGOS_COLLECTOR_CLUSTERS` (JSON array), all sharing the store — see
  [ADR-0005](docs/adr/adr-0005-multi-cluster-collector.md).
- **ANSSI cartography layers** attached per kind via
  [ADR-0002](docs/adr/adr-0002-kubernetes-to-anssi-cartography-layers.md)
  (`ecosystem` / `business` / `applicative` / `administration` /
  `infrastructure_logical` / `infrastructure_physical`).
- **REST API** (OpenAPI 3.1 contract-first, `api/openapi/openapi.yaml`) with
  cursor pagination and merge-patch updates. Errors follow
  [RFC 7807](https://datatracker.ietf.org/doc/html/rfc7807)
  (`application/problem+json`).
- **Bearer-token auth** with per-operation scopes (`read` / `write` / `delete`
  / `admin`) declared in the spec and enforced server-side.
- **Web UI** (React + TypeScript SPA embedded in the binary at `/ui/`) — see
  [ADR-0006](docs/adr/adr-0006-ui-for-audit-and-curated-metadata.md). Currently
  ships a login page and a Clusters list; more views and curated-metadata
  editing land in follow-up increments.
- **Prometheus metrics** at `/metrics` (HTTP + collector upsert / reconcile /
  error counters, per-resource last-poll gauges, `argos_build_info`).
- **Container image** built from a multi-stage Dockerfile
  (`gcr.io/distroless/static-debian12:nonroot`, UID 65532, static CGO-off
  binary).
- **Kustomize manifests** under `deploy/` for running argosd inside a
  Kubernetes cluster as its own collector target.

## Stack

- Go 1.25+ (toolchain pinned via `go.mod`)
- PostgreSQL 16+ (JSONB for heterogeneous K8s specs, `goose` for migrations
  embedded in the binary)
- `pgx/v5` + `pgxpool`
- `oapi-codegen` for the REST server stubs
- React 18 + TypeScript + Vite for the UI
- `client-go` for Kubernetes polling

## Quick start

### Run argosd with Docker (simplest)

```bash
# 1. Postgres on the host
docker run -d --rm --name argos-pg \
  -e POSTGRES_PASSWORD=argos -e POSTGRES_DB=argos \
  -p 5432:5432 postgres:16-alpine

# 2. Build and run argosd
make ui-build        # produce ui/dist for embedding
make build           # produce bin/argosd
ARGOS_DATABASE_URL="postgres://postgres:argos@localhost:5432/argos?sslmode=disable" \
  ARGOS_API_TOKEN=dev ./bin/argosd

# 3. Try it
# API
curl -H 'Authorization: Bearer dev' \
  -H 'Content-Type: application/json' \
  -d '{"name":"demo","environment":"staging"}' \
  http://localhost:8080/v1/clusters

# UI
open http://localhost:8080/   # redirects to /ui/, paste `dev` to sign in
```

### Deploy into a Kubernetes cluster

See [`deploy/README.md`](deploy/README.md) for the full walkthrough (namespace,
ServiceAccount, RBAC, Deployment, Service, token Secret). Supports both the
in-cluster self-catalogue mode and the multi-cluster mode where argosd reads
several kubeconfigs and polls every target in parallel.

## Configuration

All configuration is env-based.

| Variable | Required | Default | Purpose |
|----------|----------|---------|---------|
| `ARGOS_DATABASE_URL` | yes | — | PostgreSQL DSN. |
| `ARGOS_API_TOKEN` | † | — | Convenience: a single token granted `admin`. |
| `ARGOS_API_TOKENS` | † | — | JSON array of `{name, token, scopes}` tuples. Merged with `ARGOS_API_TOKEN`. |
| `ARGOS_ADDR` | no | `:8080` | HTTP listen address. |
| `ARGOS_AUTO_MIGRATE` | no | `true` | Run embedded goose migrations on startup. |
| `ARGOS_SHUTDOWN_TIMEOUT` | no | `15s` | Graceful shutdown budget on SIGINT / SIGTERM. |
| `ARGOS_COLLECTOR_ENABLED` | no | `false` | Enable the polling collector. |
| `ARGOS_COLLECTOR_CLUSTERS` | no | — | JSON array of `{name, kubeconfig}` tuples (multi-cluster). |
| `ARGOS_CLUSTER_NAME` / `ARGOS_KUBECONFIG` | no | — | Single-cluster shortcut (ignored when `ARGOS_COLLECTOR_CLUSTERS` is set). |
| `ARGOS_COLLECTOR_INTERVAL` | no | `60s` | Time between polls. |
| `ARGOS_COLLECTOR_FETCH_TIMEOUT` | no | `20s` | Per-poll Kubernetes API timeout. |
| `ARGOS_COLLECTOR_RECONCILE` | no | `true` | Delete rows that no longer appear in the live listing. |

† At least one of `ARGOS_API_TOKEN` or `ARGOS_API_TOKENS` must be set.
`/healthz` and `/readyz` stay unauthenticated.

## Development

Prereqs: Go (pinned in `go.mod`), Node 22+, Docker (for the PG service
container used by integration tests).

```bash
# Backend-only loop (no UI toolchain):
make build-noui
make test

# Full loop (default `make build` requires ui/dist):
make ui-install   # once, after a fresh checkout
make ui-build
make build
make test

# UI with hot reload (proxies /v1 + /healthz + /metrics to :8080):
#   terminal 1
ARGOS_DATABASE_URL=... ARGOS_API_TOKEN=dev ./bin/argosd
#   terminal 2
make ui-dev       # open http://localhost:5173/ui/
```

Integration tests that hit PostgreSQL are gated on `PGX_TEST_DATABASE`. Unset
it and they skip; set it and they run against whatever DSN you point at
(CI uses a service container — see `.github/workflows/ci.yml`).

### Common Make targets

| Target | What it does |
|--------|--------------|
| `make build` | Compile `argosd` into `bin/` (embeds `ui/dist`). |
| `make build-noui` | Compile without the UI (no Node required; `/ui/` replies 404). |
| `make test` | `go test -race -cover ./...` |
| `make check` | fmt + vet + lint + test (CI-equivalent). |
| `make docker-build` | Build the container image as `argos:dev`. |
| `make ui-install` | `npm ci` in `ui/`. |
| `make ui-build` | Produce `ui/dist/` (needed by `make build`). |
| `make ui-dev` | Vite dev server on `:5173`. |
| `make ui-check` | TypeScript typecheck. |

## Architecture

High level:

```
   Kubernetes cluster(s)
          │
          │ client-go (list, per-tick)
          ▼
   ┌──────────────┐     ┌────────────┐
   │  collector   │────▶│  PostgreSQL│◀──── REST API / SPA / Prometheus
   │ (goroutine/  │     │  (JSONB +  │
   │  cluster)    │     │   goose)   │
   └──────────────┘     └────────────┘
```

- `cmd/argosd/` — daemon entry point (HTTP server, collector goroutines, graceful shutdown).
- `internal/api/` — REST handlers + `Store` interface + `BearerAuth` middleware.
- `internal/store/` — PostgreSQL implementation via `pgx/v5`.
- `internal/collector/` — Kubernetes polling + reconciliation.
- `internal/metrics/` — Prometheus registry exposed at `/metrics`.
- `ui/` — embedded React SPA served at `/ui/*`.
- `migrations/` — timestamped SQL migrations embedded into the binary.
- `api/openapi/openapi.yaml` — contract source of truth (server + TS client are generated from it).

See [`CLAUDE.md`](CLAUDE.md) for the detailed module-by-module notes.

## Architectural decisions

| # | Topic |
|---|-------|
| [0001](docs/adr/adr-0001-cmdb-for-snc-using-kube.md) | Build a CMDB for SNC against the Kubernetes API (replaces Mercator for the K8s scope). |
| [0002](docs/adr/adr-0002-kubernetes-to-anssi-cartography-layers.md) | Map every Kubernetes kind onto one of the six ANSSI cartography layers. |
| [0003](docs/adr/adr-0003-workload-polymorphism.md) | Single `workloads` table polymorphic on `kind`, with a JSONB `spec` column. |
| [0004](docs/adr/adr-0004-ingress-layer-classification.md) | Classify Ingress in the `applicative` layer. |
| [0005](docs/adr/adr-0005-multi-cluster-collector.md) | Central-pull multi-cluster topology: one argosd, N collector goroutines. |
| [0006](docs/adr/adr-0006-ui-for-audit-and-curated-metadata.md) | Web UI bundled into argosd for audit views and curated asset metadata. |

## License

GPL-3.0 — see [LICENSE](LICENSE).
