# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Argos is a CMDB (Configuration Management Database) for Kubernetes environments, aligned with the ANSSI **SecNumCloud (SNC)** qualification framework. It replaces Mercator for the Kubernetes-scoped portion of the inventory. See `docs/adr/adr-0001-cmdb-for-snc-using-kube.md` for the foundational architectural decision.

## Stack

- **Language**: Go (1.23+)
- **Database**: PostgreSQL (with JSONB for heterogeneous Kubernetes specs)
- **API**: REST, contract-first via OpenAPI 3 (spec will live under `api/openapi/`)
- **Ingestion**: polling-based collector querying the Kubernetes API

## Layout

- `cmd/argosd/` — main entry point for the Argos daemon
- `internal/` — application packages (not importable externally); created as subsystems land
- `api/openapi/openapi.yaml` — OpenAPI 3.1 specification (contract source of truth)
- `migrations/` — PostgreSQL schema migrations (to be added)
- `docs/adr/` — Architectural Decision Records

## Common commands

| Command | What it does |
|---------|--------------|
| `make build` | Compile the `argosd` binary into `bin/` (embeds `ui/dist`; run `make ui-build` first) |
| `make build-noui` | Compile `argosd` without the embedded UI (no Node/npm required); `/ui/` replies 404 |
| `make test` | Run all tests with `-race` and coverage |
| `make test-one TEST=TestName` | Run a single test by exact name |
| `make vet` | `go vet ./...` |
| `make lint` | `golangci-lint run` |
| `make fmt` | `gofmt -w .` |
| `make check` | fmt + vet + lint + test (CI-equivalent) |
| `make tidy` | `go mod tidy` |
| `make ui-install` | `npm ci` inside `ui/` (first-time setup and after dep bumps) |
| `make ui-build` | Produce `ui/dist/` for embedding into `argosd` |
| `make ui-dev` | Run Vite dev server on :5173 with `/v1` + `/healthz` proxied to argosd on :8080 |
| `make ui-check` | TypeScript typecheck for `ui/` |

## Architecture notes

The codebase currently covers the API layer only:

- `cmd/argosd/main.go` — daemon entry point: env-based configuration (`ARGOS_ADDR`, `ARGOS_DATABASE_URL`, `ARGOS_AUTO_MIGRATE`, `ARGOS_SHUTDOWN_TIMEOUT`, `ARGOS_BOOTSTRAP_ADMIN_PASSWORD`, `ARGOS_SESSION_SECURE_COOKIE`, collector vars). Opens the PostgreSQL pool, runs migrations, bootstraps the first admin user when the DB has none, starts the HTTP server with the dual-path auth middleware (cookie or bearer), spawns the collector goroutine when enabled, handles graceful shutdown on SIGINT / SIGTERM. Refuses to start if the removed `ARGOS_API_TOKEN` / `ARGOS_API_TOKENS` env vars are set — migrate to admin-panel-issued tokens per ADR-0007.

### Auth (ADR-0007)

Dual-path authentication resolved by `internal/auth.Middleware`:

- **Humans** log in with username + password (local users) against `POST /v1/auth/login` → server-side session cookie (HttpOnly, SameSite=Strict, 8h sliding expiry). OIDC is the follow-up landing PR.
- **Machines** use `Authorization: Bearer argos_pat_<prefix>_<suffix>`. Tokens are argon2id-hashed at rest with an 8-char plaintext prefix stored separately for O(1) lookup; created in the admin UI by an admin only; plaintext shown once and never again (GitHub-PAT pattern).

Four fixed roles mapped to scopes:

| Role     | Scopes                                                            |
|----------|-------------------------------------------------------------------|
| `admin`  | `read` + `write` + `delete` + `admin` + `audit` (implies every endpoint) |
| `editor` | `read` + `write`                                                  |
| `auditor`| `read` + `audit`                                                  |
| `viewer` | `read`                                                            |

The scope check in the OpenAPI `security:` blocks is unchanged from the pre-ADR-0007 world; both cookie and bearer paths feed the same downstream `caller{id, kind, role, scopes}` context.

First-run bootstrap creates a single admin user when `COUNT(users WHERE role='admin' AND disabled_at IS NULL) = 0`. Password comes from `ARGOS_BOOTSTRAP_ADMIN_PASSWORD` if set, else a random 16-char string printed **once** to the startup log inside a loud banner; `must_change_password=true` blocks every endpoint except `/v1/auth/change-password` until rotated. `/healthz` and `/readyz` stay unauthenticated.
- `internal/api/` — generated server (`api.gen.go`), hand-written handlers (`server.go`, `auth_handlers.go`), `Store` interface (`store.go`) with `ErrNotFound` / `ErrConflict` sentinels. RFC 7807 `application/problem+json` for all errors. `auth.go` is a thin adapter wrapping `auth.Middleware` into `api.MiddlewareFunc`.
- `internal/auth/` — password hashing (argon2id, PHC-encoded), token minting (`argos_pat_<8hex>_<32urlb64>`), session cookie helpers, and `Middleware()` that resolves cookie → bearer → 401 and attaches `Caller{id, kind, role, scopes}` to the request context. Role → scope mapping lives here (`ScopesForRole`). Store interface is narrow — the PG store implements it alongside the wider `api.Store`.
- `internal/store/` — PostgreSQL implementation of `api.Store` using `pgx/v5`. Cursor-paginated list, merge-patch updates, embedded `goose` migrations. FK chain: `clusters` → `namespaces` / `nodes` / `persistent_volumes` → `pods` / `workloads` / `services` / `ingresses` / `persistent_volume_claims`, all `ON DELETE CASCADE`. Workloads are polymorphic on a `kind` discriminator per ADR-0003; kind-specific detail lives in the `spec` JSONB column. Pods and Workloads also carry a `containers` JSONB column (`{name, image, image_id?, init}` entries) for SBOM / CVE workflows. Pods also carry a nullable `workload_id` FK to their top-level controlling Workload (`ON DELETE SET NULL` — a deleted Workload doesn't cascade-delete its pods, they're reaped by the normal pod-reconcile pass). PVCs carry a nullable `bound_volume_id` FK to the bound PV with the same `ON DELETE SET NULL` semantics. Nodes carry a Mercator-aligned field set for incident response: role, cloud identity (`provider_id`, `instance_type`, `zone`), networking (`internal_ip`, `external_ip`, `pod_cidr`), OS stack (`kernel_version`, `operating_system`, `container_runtime_version`, `kube_proxy_version`), capacity + allocatable quadruples for cpu/memory/pods/ephemeral_storage, `conditions` + `taints` JSONB arrays, and the `ready`/`unschedulable` flags. Ingresses and Services carry a `load_balancer` JSONB column mirroring `status.loadBalancer.ingress[]` (`{ip?, hostname?, ports?}`) so on-prem VIPs — MetalLB, Kube-VIP, hardware LBs — surface alongside cloud-provisioned ones. `ListPods` and `ListWorkloads` accept a `PodListFilter` / `WorkloadListFilter` struct with optional `ImageSubstring` (JSONB ILIKE over the containers array) and `NodeName` so the UI's image-search and node-impact views can query server-side.
- `internal/metrics/` — Prometheus registry and exporter. `/metrics` endpoint is mounted on the main HTTP mux unauthenticated (Prometheus scrape convention). Exposes HTTP request/duration counters, collector upsert / reconcile / error counters and last-poll gauges per `(cluster, resource)`, plus `argos_build_info`. `InstrumentHandler` wraps the API mux to count requests by method, route pattern, and status class.
- `ui/` — Vite + React + TypeScript SPA per ADR-0006. Built bundle is embedded into the argosd binary via `//go:embed all:dist` (see `ui/embed.go`) and served at `/ui/*`; root `/` redirects to `/ui/`. Per ADR-0007, humans log in with username + password and receive a server-side session cookie; the SPA reads `/v1/auth/me` on every route change to drive role-aware rendering and forced-rotation redirects. Logout hits `/v1/auth/logout`. Build-tag `noui` disables the embed for backend-only workflows (`make build-noui`); the default `make build` requires `make ui-build` to have produced `ui/dist/` first. Dev loop: `make ui-dev` serves the SPA on :5173 with `/v1` + `/healthz` + `/metrics` proxied to a local argosd on :8080 for hot reload.
- `internal/collector/` — Kubernetes polling collector. Each tick fetches the API server version (refreshing the cluster record), lists nodes (upsert by `(cluster_id, name)`; reads the full Mercator-aligned field set from `status.nodeInfo` + `status.capacity` + `status.allocatable` + `status.addresses` + `status.conditions` + `spec.providerID` + `spec.podCIDR` + `spec.taints` + `spec.unschedulable`, derives role from `node-role.kubernetes.io/*` labels, and reads instance-type / zone from the well-known topology labels), lists cluster-scoped PersistentVolumes (upsert by `(cluster_id, name)`, returns name → id map for PVC linking), lists namespaces (upsert by `(cluster_id, name)`, returns name → id map), lists workloads via three `AppsV1` list calls folded into one `[]WorkloadInfo` tagged by `Kind` (upsert by `(namespace_id, kind, name)`, ingested before pods so each pod's `workload_id` can be resolved), lists pods cluster-wide (upsert by `(namespace_id, name)`; the pod's controlling `ownerReference` is walked — `ReplicaSet` → `Deployment` via a side `ListReplicaSetOwners` call, or direct for `StatefulSet` / `DaemonSet` — to set `workload_id`; unmodelled kinds like `Job` leave the FK null), lists services cluster-wide (upsert by `(namespace_id, name)`; flattens `status.loadBalancer.ingress[]` into the `load_balancer` JSONB), lists ingresses via `NetworkingV1` (upsert by `(namespace_id, name)`, rules/tls/load_balancer flattened into JSONB), and lists PersistentVolumeClaims cluster-wide (upsert by `(namespace_id, name)`; each PVC's `spec.volumeName` is resolved against the PV map to set `bound_volume_id`, or null when pending / the PV isn't listed this tick). When `ARGOS_COLLECTOR_RECONCILE=true` (default), rows that disappeared from the live listing are deleted so the CMDB mirrors the cluster — required for ANSSI cartography fidelity. Pods, workloads, services, ingresses, and PVCs reconcile per-namespace; nodes and PVs reconcile cluster-scoped; workload reconcile keys on the `(kind, name)` tuple so a deleted Deployment `web` doesn't wipe the still-live StatefulSet `web`. Reconciliation only runs after a successful list, so a transient Kubernetes error never wipes the store. Disabled by default; enable with `ARGOS_COLLECTOR_ENABLED=true`. Multi-cluster topology per ADR-0005: `ARGOS_COLLECTOR_CLUSTERS` is a JSON array of `{name, kubeconfig}` tuples, one collector goroutine per entry, all sharing the store; the legacy `ARGOS_CLUSTER_NAME` + `ARGOS_KUBECONFIG` still work as a single-cluster shortcut. An empty `kubeconfig` falls back to in-cluster config.
- `migrations/` — timestamped SQL migrations, embedded in the binary via `migrations/embed.go`.
- `.github/workflows/ci.yml` — GitHub Actions pipeline: `setup-node@v4` (Node 22, npm-cache keyed on `ui/package-lock.json`) + `npm ci && npm run build` to produce `ui/dist` so downstream `go build` can `//go:embed` it, then codegen-drift check, `go vet`, `go build`, `go test -race` against a Postgres service container (integration tests gated on `PGX_TEST_DATABASE` run in CI), `golangci-lint`, and a parallel `docker build` job that verifies the image compiles (no publish yet).
- `Dockerfile` — three-stage build: `node:22-alpine` UI builder → `golang` builder (embeds `ui/dist` via `//go:embed`) → `gcr.io/distroless/static-debian12:nonroot` runtime. Produces a static (`CGO_ENABLED=0`) `argosd` binary, runs as UID 65532. Override `VERSION` build arg to stamp `main.version`. Local build via `make docker-build` (tags `argos:dev`).
- `deploy/` — reference Kustomize manifests for running `argosd` in a Kubernetes cluster cataloguing itself via in-cluster ServiceAccount (namespace, ServiceAccount + ClusterRole with `list` on the catalogued kinds — nodes, namespaces, pods, services, persistentvolumes, persistentvolumeclaims in the core group; deployments, statefulsets, daemonsets, replicasets in `apps`; ingresses in `networking.k8s.io`; plus Deployment, ClusterIP Service, Secret template). `deploy/README.md` covers the full walkthrough including the multi-cluster variant (mount multiple kubeconfigs, switch to `ARGOS_COLLECTOR_CLUSTERS`).
- `scripts/seed-demo.sh` — populates a running argosd (on `:8080`, token `dev` by default) with a realistic multi-cluster inventory for demos / screenshots / UI development. Re-runnable after a `TRUNCATE clusters CASCADE`.

Follow-up work queued against ADR-0006: curated-metadata columns (owner / criticality / notes / runbook_url / annotations) on the durable kinds so users can annotate assets through the UI without the collector overwriting the edits. Snapshots / time-travel (per the original ADR-0001 roadmap) remain a longer-horizon item.
