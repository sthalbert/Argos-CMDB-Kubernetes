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

### Data model

- **Polling collector** against one or more Kubernetes clusters, covering
  Clusters, Nodes, Namespaces, Workloads (Deployment / StatefulSet / DaemonSet,
  polymorphic per [ADR-0003](docs/adr/adr-0003-workload-polymorphism.md)), Pods,
  Services, Ingresses, PersistentVolumes, and PersistentVolumeClaims.
- **Relationship FKs** resolved at ingest time:
  - Pod → controlling Workload (`workload_id`), walked via the K8s
    ownerReference chain — ReplicaSet → Deployment in one hop, direct for
    StatefulSet / DaemonSet.
  - PVC → bound PV (`bound_volume_id`), resolved against the PV name map each
    tick. Both FKs use `ON DELETE SET NULL` so deletion of the parent leaves
    the child to the reconcile pass rather than cascading.
- **Mercator-aligned Node model** — role (control-plane / worker), cloud
  identity (`provider_id`, `instance_type`, `zone`), networking
  (`internal_ip`, `external_ip`, `pod_cidr`), full OS stack
  (`kernel_version`, `operating_system`, `container_runtime_version`,
  `kube_proxy_version`), capacity + allocatable quadruples for
  cpu / memory / pods / ephemeral_storage, status conditions + scheduling
  taints, and `ready` / `unschedulable` flags.
- **External load balancer** on Ingress and Service (`load_balancer` JSONB)
  mirroring `status.loadBalancer.ingress[]` — populated by the cloud
  controller on managed clusters, or by MetalLB / Kube-VIP / a hardware LB
  on-prem. Answers "what's the external entry point for this thing?"
  without bouncing into kubectl.
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

### API

- **REST** (OpenAPI 3.1 contract-first, `api/openapi/openapi.yaml`) with
  cursor pagination and merge-patch updates. Errors follow
  [RFC 7807](https://datatracker.ietf.org/doc/html/rfc7807)
  (`application/problem+json`).
- **Dual-path auth** per
  [ADR-0007](docs/adr/adr-0007-auth-and-rbac.md): humans log in with
  either local username + password **or** OIDC (authorization-code flow
  with PKCE + nonce + state; shadow users keyed on `(issuer, sub)`;
  first-login role `viewer`, admins promote manually) → server-side
  session cookie; machines use `Authorization: Bearer` with tokens
  minted by an admin in the UI (argon2id-hashed, plaintext shown once,
  immediate revocation). Four fixed roles — `admin` / `editor` /
  `auditor` / `viewer` — map to the existing per-operation scope
  declarations. First install bootstraps an `admin` user with a random
  password printed once to the startup log (`must_change_password`
  forces rotation on first login).
- **Filter endpoints** for incident-response queries:
  - `GET /v1/workloads?image=log4j:2.15` — case-insensitive substring match
    over every container's `image` field, init containers included.
  - `GET /v1/pods?image=…` — same shape for pods.
  - `GET /v1/pods?node_name=worker-02.prod` — exact match; powers the
    "if this node dies, which pods are lost?" view.

### Web UI

React + TypeScript SPA embedded in the binary at `/ui/` — see
[ADR-0006](docs/adr/adr-0006-ui-for-audit-and-curated-metadata.md).

- **List pages** for all 9 kinds with context-aware columns (Node role /
  zone / instance-type / CPU-mem / Ready status; Ingress / Service
  load-balancer address; PVC bound PV; Workload container images; …).
- **Drill-down detail pages** for the core cartography chain
  (Cluster → Namespace → Workload → Pod; Cluster → Node). Namespace detail
  aggregates every asset in it ("application = namespace" view); Workload
  detail aggregates its pods + unique nodes ("application = workload" view).
- **Node detail** renders the full Mercator-aligned picture — Identity,
  OS & runtime, Networking, Resources (capacity vs allocatable), Conditions
  with per-row health colouring, Taints, Labels — plus an impact-analysis
  callout and a workload-grouped breakdown of affected pods.
- **Ingress detail** renders the load-balancer block first, then routing
  rules and TLS, so on-prem auditors can see the VIP at a glance.
- **Component search** (`/ui/search/image`) with URL-persisted query —
  find every workload + pod running `log4j:2.15.0`, grouped by
  cluster / namespace with clickable breadcrumbs.
- Humans authenticate through the same-origin session cookie set by
  `/v1/auth/login` (or the OIDC callback); no token paste, no CORS.
  The "Sign in with <IdP>" button renders automatically when OIDC is
  configured — the SPA probes `/v1/auth/config` on page load.

### Ops

- **Prometheus metrics** at `/metrics` (HTTP + collector upsert / reconcile /
  error counters, per-resource last-poll gauges, `argos_build_info`).
- **Container image** built from a multi-stage Dockerfile
  (`gcr.io/distroless/static-debian12:nonroot`, UID 65532, static CGO-off
  binary). A first stage builds the UI bundle with `node:22-alpine`; the
  Go stage embeds it via `//go:embed`.
- **Kustomize manifests** under `deploy/` for running argosd inside a
  Kubernetes cluster as its own collector target.
- **Demo seed** — `scripts/seed-demo.sh` populates a realistic
  multi-cluster inventory (prod/staging × 6 namespaces × workloads + pods
  + services + one MetalLB-style ingress) so the UI has something to show
  without a real cluster.

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

# 2. Build and run argosd with a known bootstrap password
make ui-build        # produce ui/dist for embedding
make build           # produce bin/argosd
ARGOS_DATABASE_URL="postgres://postgres:argos@localhost:5432/argos?sslmode=disable" \
  ARGOS_BOOTSTRAP_ADMIN_PASSWORD="local-dev-bootstrap-0123456789" \
  ./bin/argosd

# 3. Sign in
open http://localhost:8080/   # redirects to /ui/
# → user: admin
# → pass: local-dev-bootstrap-0123456789
# → rotate on first login (must_change_password is enforced)

# 4. Mint a machine token via the admin panel — or via curl, using the
#    session cookie you just picked up in the browser.
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
| `ARGOS_BOOTSTRAP_ADMIN_PASSWORD` | no | random | First-install only: password for the auto-created `admin` user. If unset, argosd generates one and prints it once to the startup log. |
| `ARGOS_SESSION_SECURE_COOKIE` | no | `auto` | Session-cookie `Secure` flag: `auto` (mirror request scheme), `always`, or `never`. |
| `ARGOS_ADDR` | no | `:8080` | HTTP listen address. |
| `ARGOS_AUTO_MIGRATE` | no | `true` | Run embedded goose migrations on startup. |
| `ARGOS_SHUTDOWN_TIMEOUT` | no | `15s` | Graceful shutdown budget on SIGINT / SIGTERM. |
| `ARGOS_COLLECTOR_ENABLED` | no | `false` | Enable the polling collector. |
| `ARGOS_COLLECTOR_CLUSTERS` | no | — | JSON array of `{name, kubeconfig}` tuples (multi-cluster). |
| `ARGOS_CLUSTER_NAME` / `ARGOS_KUBECONFIG` | no | — | Single-cluster shortcut (ignored when `ARGOS_COLLECTOR_CLUSTERS` is set). |
| `ARGOS_COLLECTOR_INTERVAL` | no | `60s` | Time between polls. |
| `ARGOS_COLLECTOR_FETCH_TIMEOUT` | no | `20s` | Per-poll Kubernetes API timeout. |
| `ARGOS_COLLECTOR_RECONCILE` | no | `true` | Delete rows that no longer appear in the live listing. |

`/healthz`, `/readyz`, `/metrics`, `/ui/*`, and `/v1/auth/login` stay
unauthenticated. Everything else requires either a session cookie
(obtained via login) or a machine bearer token (minted in the admin
panel).

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
ARGOS_DATABASE_URL=... \
  ARGOS_BOOTSTRAP_ADMIN_PASSWORD=local-dev-passphrase \
  ./bin/argosd
#   terminal 2
make ui-dev       # open http://localhost:5173/ui/, sign in with admin
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
