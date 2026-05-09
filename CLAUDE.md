# CLAUDE.md

Guidance for Claude Code working in this repo. For deep design rationale, read `docs/adr/`.

## Project

**longue-vue** — Kubernetes + cloud-VM CMDB aligned with ANSSI **SecNumCloud**. Foundational rationale: ADR-0001.

## Stack

- Go 1.23+, PostgreSQL (JSONB for heterogeneous specs), REST + OpenAPI 3.1 (`api/openapi/`), polling collectors.
- UI: Vite + React + TypeScript, embedded into the binary via `//go:embed` (ADR-0006).

## Layout

- `cmd/longue-vue/` — main daemon (API + UI + in-process collectors).
- `cmd/longue-vue-ingest-gw/` — stateless DMZ reverse-proxy (ADR-0016).
- `cmd/longue-vue-vm-collector/` — push-mode cloud-VM collector (ADR-0015).
- `internal/` — `api/`, `auth/`, `store/` (pgx/v5), `collector/` (K8s pull), `vmcollector/`, `ingestgw/`, `eol/`, `imageversions/`, `mcp/`, `impact/`, `metrics/`, `httputil/`, `secrets/`.
- `api/openapi/openapi.yaml` — contract source of truth (codegen drift checked in CI).
- `migrations/` — goose-style timestamped SQL, embedded.
- `ui/` — SPA; `make ui-build` produces `ui/dist/` for embed. `noui` build tag skips it.
- `charts/` — one Helm chart per binary (ADR-0018). `deploy/` Kustomize is examples only.
- `docs/adr/` — ADRs are the canonical source for *why*; this file should not duplicate them.

## Commands

| Command | Purpose |
|---|---|
| `make build` | Build binary (requires `make ui-build` first) |
| `make build-noui` | Build without UI (no Node needed) |
| `make test` | `go test -race` + coverage |
| `make test-one TEST=Name` | Single test |
| `make check` | fmt + vet + lint + test (CI-equivalent) |
| `make ui-dev` | Vite on :5173, proxies `/v1` + `/healthz` + `/metrics` to :8080 |
| `make ui-build` / `ui-check` / `ui-install` | UI build / typecheck / `npm ci` |

## Key conventions

- **Errors**: RFC 7807 `application/problem+json`. Store sentinels: `ErrNotFound`, `ErrConflict`, `ErrLastAdmin`.
- **Pagination**: cursor-based on list endpoints.
- **Updates**: merge-patch via PATCH; collector tick writes only its own fields, leaving curated metadata alone.
- **JSONB**: heterogeneous specs (workload `spec`, `containers`, `conditions`, `taints`, `load_balancer`, `applications`, `annotations`).
- **FK chain**: `clusters` → `namespaces`/`nodes`/`persistent_volumes` → `pods`/`workloads`/`services`/`ingresses`/`pvcs`, all `ON DELETE CASCADE`. `pods.workload_id` and `pvcs.bound_volume_id` are `ON DELETE SET NULL`.
- **Reconcile semantics**: only run after a successful list; transient API errors must never wipe the store. Workload reconcile keys on `(kind, name)`.
- **VMs vs nodes**: `virtual_machines` is a separate top-level table from `nodes`; dedup on POST checks `provider_id` substring against `nodes`.

## Auth (ADR-0007, ADR-0015 §5, ADR-0017)

- Dual-path middleware: session cookie (humans, login or OIDC) **or** `Authorization: Bearer longue_vue_pat_…`. Both feed `Caller{id, kind, role, scopes}`.
- Roles: `admin` / `editor` / `auditor` / `viewer` plus the machine-only `vm-collector` token preset bound to a `cloud_account` UUID at issuance.
- `auth.HasScope`: admin implies everything **except** `vm-collector` (preserves the SK-read boundary).
- First-run bootstrap creates one admin from `LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD` or a random one printed once; `must_change_password=true` blocks all but `/v1/auth/change-password`.
- Last-admin guard: `UpdateUserGuarded` / `DeleteUserGuarded` use `SELECT … FOR UPDATE` over active admins; returns `ErrLastAdmin` → 409.
- OIDC optional via `LONGUE_VUE_OIDC_ISSUER`; PKCE+nonce+state, one-shot rows in `oidc_auth_states`. Shadow users default to `viewer`; group claims are not trusted.
- Removed env vars `LONGUE_VUE_API_TOKEN(S)` are a hard startup error — migrate to admin-issued PATs.

## Audit log

`AuditMiddleware` (mounted after auth) records all non-GET + every `/v1/admin/*` read and the credentials-fetch path into `audit_events`. Body scrubber redacts password / token / OIDC-secret / `access_key` / `secret_key`. Insert failures log at ERROR but never 5xx. `audit_events.source ∈ {api, ingest_gw, system}`.

## Listeners (ADR-0016, ADR-0017)

- Public `:8080` — humans + machines. TLS via `LONGUE_VUE_PUBLIC_LISTEN_TLS_{CERT,KEY}` (mtime hot reload).
- Ingest mTLS-only `:8443` (opt-in via `LONGUE_VUE_INGEST_LISTEN_ADDR`) — exposes the 18 push-collector writes + `POST /v1/auth/verify`. Allowlist in `internal/ingestgw/allowlist.go` must stay in sync with `internal/api/ingest_mux.go`.
- `LONGUE_VUE_TRUSTED_PROXIES` (CIDRs, empty by default) gates trust in `X-Forwarded-For` / `-Proto`. Used by rate-limiter, audit IP, secure-cookie decision, HSTS — see `internal/httputil/`.
- `LONGUE_VUE_REQUIRE_HTTPS=true` startup guard: refuse boot unless native TLS is on **or** trusted-proxy + `SecureAlways` cookie posture is set.
- `/healthz`, `/readyz`, `/metrics` are unauthenticated.

## Secrets (ADR-0015 §4)

`internal/secrets` — AES-256-GCM with AAD bound to the row PK. Master key from `LONGUE_VUE_SECRETS_MASTER_KEY` (base64 32 bytes). Required only when any `cloud_accounts` row carries an encrypted SK. `Fingerprint()` (first 8 hex of SHA-256) logged at startup.

## Cloud accounts + VM collector (ADR-0015, ADR-0019)

- `cloud_accounts` status: `pending_credentials` → `active` → `error` → `disabled`. Hybrid onboarding: collector POSTs a placeholder, admin pastes AK/SK in UI, collector picks up on next refresh.
- AK plaintext (public identifier); SK encrypted. Plaintext SK only ever leaves via `GET /v1/cloud-accounts/by-{name,id}/{x}/credentials` — audit-logged, response body never captured.
- `virtual_machines.applications` JSONB (curator-only; collector never writes it). GIN `jsonb_path_ops` index. Soft-delete via `terminated_at`; reconcile never hard-deletes.
- VM list filters: `cloud_account_id|name`, `region`, `role`, `power_state`, `include_terminated`, `name`, `image`, `application`, `application_version`. LIKE metacharacters escaped; product names normalized server-side.

## Settings + feature toggles

Single-row `settings` table (`id=1 CHECK`). Runtime toggles `eol_enabled`, `mcp_enabled` (admin scope, hand-written `GET`/`PATCH /v1/admin/settings`, not in OpenAPI). Env vars seed the initial value.

**Image versions enricher (`image_versions_enabled`, ADR-0022):** queries public registries for the latest tag of each container image used in workloads/pods. Default interval 24h (`LONGUE_VUE_IMAGE_VERSIONS_INTERVAL`). Allowlist of registries is in `image_versions_registries` (DB-backed, admin CRUD). Reuses the `eol.Annotation` shape so a richer V3 (EOL/CVE) is purely additive.

## Collectors

- `internal/collector/` — K8s pull. Multi-cluster via `LONGUE_VUE_COLLECTOR_CLUSTERS` JSON (legacy single-cluster vars still work). Reconcile is on by default; per-namespace for namespaced resources, cluster-scoped for nodes + PVs. Pods resolve `workload_id` by walking `ownerReferences` (RS→Deployment, or direct StatefulSet/DaemonSet).
- `internal/vmcollector/` — push-mode. `Provider` interface in `provider/`; Outscale impl + fake. `apiclient/` mirrors the K8s collector's CA/mTLS/proxy transport. `filter/` is a cheap pre-filter; server-side `nodes.provider_id` dedup is canonical. Private Prometheus registry.

## Curated metadata pattern

Cluster carries `owner` / `criticality` / `notes` / `runbook_url` / `annotations` JSONB, merge-patched via PATCH; collector leaves them alone. UI: "Ownership & context" card with inline edit. Same pattern queued for namespaces / nodes / workloads.

## EOL enricher (ADR-0012, ADR-0019)

`internal/eol/` — periodic endoflife.date queries; writes `longue-vue.io/eol.<product>` annotations on clusters, nodes, and (per VM `applications` entry) VMs. Stale keys reaped per tick. `latest_available` field shows newest published version. Centralised on the server — push collectors are unaffected.

## Idempotency

`POST /v1/clusters` is idempotent on `name` via `EnsureCluster` (200 on hit, 201 on insert; never returns `ErrConflict`).
