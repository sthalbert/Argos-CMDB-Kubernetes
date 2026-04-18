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
| `make build` | Compile the `argosd` binary into `bin/` |
| `make test` | Run all tests with `-race` and coverage |
| `make test-one TEST=TestName` | Run a single test by exact name |
| `make vet` | `go vet ./...` |
| `make lint` | `golangci-lint run` |
| `make fmt` | `gofmt -w .` |
| `make check` | fmt + vet + lint + test (CI-equivalent) |
| `make tidy` | `go mod tidy` |

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
- `internal/store/` — PostgreSQL implementation of `api.Store` using `pgx/v5`. Cursor-paginated list, merge-patch updates, embedded `goose` migrations. Nodes are FK-linked to clusters with `ON DELETE CASCADE`.
- `internal/collector/` — Kubernetes polling collector. Each tick fetches the API server version (refreshing the matching cluster record), lists all nodes (upserting each by `(cluster_id, name)` into the `nodes` table), and lists all namespaces (upserting into `namespaces`). When `ARGOS_COLLECTOR_RECONCILE=true` (default), rows that disappeared from the live listing are deleted so the CMDB mirrors the cluster — required for ANSSI cartography fidelity. Reconciliation only runs after a successful list, so a transient Kubernetes error never wipes the store. Disabled by default; enable with `ARGOS_COLLECTOR_ENABLED=true` and `ARGOS_CLUSTER_NAME=...`.
- `migrations/` — timestamped SQL migrations, embedded in the binary via `migrations/embed.go`.
- `.github/workflows/ci.yml` — GitHub Actions pipeline: codegen-drift check, `go vet`, `go build`, `go test -race` against a Postgres service container (so the integration tests gated on `PGX_TEST_DATABASE` run in CI), and `golangci-lint`.

Follow-up work: extend the OpenAPI spec and collector to cover Node, Namespace, Workload, Pod; add bearer-token auth middleware; document how K8s kinds map to ANSSI cartography layers and how snapshots are versioned in PostgreSQL.
