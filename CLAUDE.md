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

- `cmd/argosd/main.go` — daemon entry point: env-based configuration (`ARGOS_ADDR`, `ARGOS_DATABASE_URL`, `ARGOS_API_TOKEN`, `ARGOS_AUTO_MIGRATE`, `ARGOS_SHUTDOWN_TIMEOUT`, collector vars), opens the PostgreSQL pool, runs migrations, starts the HTTP server (wrapped in `BearerAuth` middleware), spawns the collector goroutine when enabled, handles graceful shutdown on SIGINT / SIGTERM.
- `internal/api/` — generated server (`api.gen.go`), hand-written handlers (`server.go`), `Store` interface (`store.go`) with `ErrNotFound` / `ErrConflict` sentinels. RFC 7807 `application/problem+json` for all errors.
- `internal/store/` — PostgreSQL implementation of `api.Store` using `pgx/v5`. Cursor-paginated list, merge-patch updates, embedded `goose` migrations.
- `internal/collector/` — Kubernetes polling collector (v1 scope: fetches the API server version via `client-go` and refreshes the matching cluster record by name). Disabled by default; enable with `ARGOS_COLLECTOR_ENABLED=true` and `ARGOS_CLUSTER_NAME=...`.
- `migrations/` — timestamped SQL migrations, embedded in the binary via `migrations/embed.go`.

Follow-up work: extend the OpenAPI spec and collector to cover Node, Namespace, Workload, Pod; add bearer-token auth middleware; document how K8s kinds map to ANSSI cartography layers and how snapshots are versioned in PostgreSQL.
