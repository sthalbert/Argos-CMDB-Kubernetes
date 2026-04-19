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

- `cmd/argosd/main.go` — daemon entry point: env-based configuration (`ARGOS_ADDR`, `ARGOS_DATABASE_URL`, `ARGOS_API_TOKEN` and/or `ARGOS_API_TOKENS`, `ARGOS_AUTO_MIGRATE`, `ARGOS_SHUTDOWN_TIMEOUT`, collector vars). Opens the PostgreSQL pool, runs migrations, builds the bearer-token store, starts the HTTP server with the scope-aware `BearerAuth` middleware, spawns the collector goroutine when enabled, handles graceful shutdown on SIGINT / SIGTERM.

### Auth scopes

`BearerAuth` enforces per-operation scopes declared in the OpenAPI spec:

| Scope    | Grants                                              |
|----------|-----------------------------------------------------|
| `read`   | list and get cluster endpoints                      |
| `write`  | create and update                                   |
| `delete` | removal                                             |
| `admin`  | implicit grant of every other scope                 |

Configure tokens via either or both env vars (merged at startup):

- `ARGOS_API_TOKEN=<value>` — convenience: a single token granted `admin`.
- `ARGOS_API_TOKENS=<json>` — JSON array, e.g.
  `[{"name":"collector","token":"...","scopes":["read","write"]}]`.

At least one token must be configured; `/healthz` and `/readyz` stay open.
- `internal/api/` — generated server (`api.gen.go`), hand-written handlers (`server.go`), `Store` interface (`store.go`) with `ErrNotFound` / `ErrConflict` sentinels. RFC 7807 `application/problem+json` for all errors.
- `internal/store/` — PostgreSQL implementation of `api.Store` using `pgx/v5`. Cursor-paginated list, merge-patch updates, embedded `goose` migrations. FK chain: `clusters` → `namespaces` / `nodes` / `persistent_volumes` → `pods` / `workloads` / `services` / `ingresses` / `persistent_volume_claims`, all `ON DELETE CASCADE`. Workloads are polymorphic on a `kind` discriminator per ADR-0003; kind-specific detail lives in the `spec` JSONB column. Pods and Workloads also carry a `containers` JSONB column (`{name, image, image_id?, init}` entries) for SBOM / CVE workflows. Pods also carry a nullable `workload_id` FK to their top-level controlling Workload (`ON DELETE SET NULL` — a deleted Workload doesn't cascade-delete its pods, they're reaped by the normal pod-reconcile pass). PVCs carry a nullable `bound_volume_id` FK to the bound PV with the same `ON DELETE SET NULL` semantics. Nodes carry a Mercator-aligned field set for incident response: role, cloud identity (`provider_id`, `instance_type`, `zone`), networking (`internal_ip`, `external_ip`, `pod_cidr`), OS stack (`kernel_version`, `operating_system`, `container_runtime_version`, `kube_proxy_version`), capacity + allocatable quadruples for cpu/memory/pods/ephemeral_storage, `conditions` + `taints` JSONB arrays, and the `ready`/`unschedulable` flags.
- `internal/metrics/` — Prometheus registry and exporter. `/metrics` endpoint is mounted on the main HTTP mux unauthenticated (Prometheus scrape convention). Exposes HTTP request/duration counters, collector upsert / reconcile / error counters and last-poll gauges per `(cluster, resource)`, plus `argos_build_info`. `InstrumentHandler` wraps the API mux to count requests by method, route pattern, and status class.
- `ui/` — Vite + React + TypeScript SPA per ADR-0006. Built bundle is embedded into the argosd binary via `//go:embed all:dist` (see `ui/embed.go`) and served at `/ui/*`; root `/` redirects to `/ui/`. Bearer token is entered on a login page and kept in `sessionStorage`; the SPA calls the same `/v1/*` endpoints the CLI uses, with the token in `Authorization: Bearer <token>`. Build-tag `noui` disables the embed for backend-only workflows (`make build-noui`); the default `make build` requires `make ui-build` to have produced `ui/dist/` first. Dev loop: `make ui-dev` serves the SPA on :5173 with `/v1` + `/healthz` + `/metrics` proxied to a local argosd on :8080 for hot reload.
- `internal/collector/` — Kubernetes polling collector. Each tick fetches the API server version (refreshing the cluster record), lists nodes (upsert by `(cluster_id, name)`), lists cluster-scoped PersistentVolumes (upsert by `(cluster_id, name)`, returns name → id map for PVC linking), lists namespaces (upsert by `(cluster_id, name)`, returns name → id map), lists workloads via three `AppsV1` list calls folded into one `[]WorkloadInfo` tagged by `Kind` (upsert by `(namespace_id, kind, name)`, ingested before pods so each pod's `workload_id` can be resolved), lists pods cluster-wide (upsert by `(namespace_id, name)`; the pod's controlling `ownerReference` is walked — `ReplicaSet` → `Deployment` via a side `ListReplicaSetOwners` call, or direct for `StatefulSet` / `DaemonSet` — to set `workload_id`; unmodelled kinds like `Job` leave the FK null), lists services cluster-wide (upsert by `(namespace_id, name)`), lists ingresses via `NetworkingV1` (upsert by `(namespace_id, name)`, rules/tls flattened into JSONB), and lists PersistentVolumeClaims cluster-wide (upsert by `(namespace_id, name)`; each PVC's `spec.volumeName` is resolved against the PV map to set `bound_volume_id`, or null when pending / the PV isn't listed this tick). When `ARGOS_COLLECTOR_RECONCILE=true` (default), rows that disappeared from the live listing are deleted so the CMDB mirrors the cluster — required for ANSSI cartography fidelity. Pods, workloads, services, ingresses, and PVCs reconcile per-namespace; nodes and PVs reconcile cluster-scoped; workload reconcile keys on the `(kind, name)` tuple so a deleted Deployment `web` doesn't wipe the still-live StatefulSet `web`. Reconciliation only runs after a successful list, so a transient Kubernetes error never wipes the store. Disabled by default; enable with `ARGOS_COLLECTOR_ENABLED=true`. Multi-cluster topology per ADR-0005: `ARGOS_COLLECTOR_CLUSTERS` is a JSON array of `{name, kubeconfig}` tuples, one collector goroutine per entry, all sharing the store; the legacy `ARGOS_CLUSTER_NAME` + `ARGOS_KUBECONFIG` still work as a single-cluster shortcut. An empty `kubeconfig` falls back to in-cluster config.
- `migrations/` — timestamped SQL migrations, embedded in the binary via `migrations/embed.go`.
- `.github/workflows/ci.yml` — GitHub Actions pipeline: codegen-drift check, `go vet`, `go build`, `go test -race` against a Postgres service container (so the integration tests gated on `PGX_TEST_DATABASE` run in CI), `golangci-lint`, and a parallel `docker build` job that verifies the image compiles (no publish yet).
- `Dockerfile` — multi-stage build: `golang` builder → `gcr.io/distroless/static-debian12:nonroot` runtime. Produces a static (`CGO_ENABLED=0`) `argosd` binary, runs as UID 65532. Override `VERSION` build arg to stamp `main.version`. Local build via `make docker-build` (tags `argos:dev`).
- `deploy/` — reference Kustomize manifests for running `argosd` in a Kubernetes cluster cataloguing itself via in-cluster ServiceAccount (namespace, ServiceAccount + ClusterRole with `list` on the six catalogued kinds, Deployment, ClusterIP Service, Secret template). `deploy/README.md` covers the full walkthrough including the multi-cluster variant (mount multiple kubeconfigs, switch to `ARGOS_COLLECTOR_CLUSTERS`).

Follow-up work: extend the OpenAPI spec and collector to cover Node, Namespace, Workload, Pod; add bearer-token auth middleware; document how K8s kinds map to ANSSI cartography layers and how snapshots are versioned in PostgreSQL.
