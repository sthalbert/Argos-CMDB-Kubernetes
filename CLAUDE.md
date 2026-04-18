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

Implementation is currently a skeleton (`cmd/argosd/main.go` only). When the codebase spans multiple subsystems — collector, store, API — expand this section with how they interact, how Kubernetes kinds map to ANSSI cartography layers, and how snapshots are versioned in PostgreSQL.
