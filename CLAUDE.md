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

- `cmd/argosd/main.go` — daemon entry point: env-based configuration (`ARGOS_ADDR`, `ARGOS_SHUTDOWN_TIMEOUT`), HTTP server startup, graceful shutdown on SIGINT / SIGTERM.
- `internal/api/` — generated server (`api.gen.go`) + hand-written handlers (`server.go`) implementing `ServerInterface`. Health probes are real; cluster handlers stub to `501 Not Implemented` until the store lands. RFC 7807 `application/problem+json` for all errors.

Remaining subsystems (PostgreSQL store, Kubernetes collector) will arrive with follow-up docs on how K8s kinds map to ANSSI cartography layers and how snapshots are versioned in PostgreSQL.
