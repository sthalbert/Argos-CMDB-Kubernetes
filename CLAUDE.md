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
- `internal/` — `api/`, `auth/`, `store/` (pgx/v5), `collector/` (K8s pull), `vmcollector/`, `ingestgw/`, `eol/`, `eolagg/`, `imageversions/`, `mcp/`, `impact/`, `metrics/`, `httputil/`, `httptransport/` (collector-side HTTP client shared by both apiclients: TLS/mTLS transport, retries, per-caller `ErrorMapper`), `secrets/`.
- `internal/store/`: `pg.go` is the core (pool, migrations, shared helpers incl. `withTx` + `isUniqueViolation`); entity CRUD lives in per-entity `pg_*.go` files. `api.Store` is composed of per-domain interfaces (`ClusterStore`, `AuthStore`, …) — new consumers should depend on the narrowest slice that fits. Hand-written handler plumbing (`writeProblem`, `requireScope`, `pathUUID`, `parseLimit`) lives in `internal/api/httphelpers.go`.
- **Applications (ADR-0029)**: `applications` + `application_blocks` are operator-curated top-level tables; Application is the first-class SNC applicative-layer entity (Block → Application → workloads/VMs). `workloads.application_id` + `virtual_machines.application_id` (and an optional per-entry `application_id` on the VM `applications` JSONB) link each asset to at most one Application, all `ON DELETE SET NULL`. Hand-written handlers under `internal/api/application_*` (documented in OpenAPI but not codegen'd).
- `api/openapi/openapi.yaml` — contract source of truth (codegen drift checked in CI).
- `migrations/` — goose-style timestamped SQL, embedded.
- `ui/` — SPA; `make ui-build` produces `ui/dist/` for embed. `noui` build tag skips it.
- `charts/` — one Helm chart per binary (ADR-0018). `deploy/` Kustomize is examples only.
- `docs/adr/` — ADRs are the canonical source for _why_; this file should not duplicate them.

## Commands

| Command                                     | Purpose                                                               |
| ------------------------------------------- | --------------------------------------------------------------------- |
| `make build`                                | Build binary (requires `make ui-build` first)                         |
| `make build-noui`                           | Build without UI (no Node needed)                                     |
| `make test`                                 | `go test -race` + coverage                                            |
| `make test-one TEST=Name`                   | Single test                                                           |
| `make check`                                | fmt + vet + lint + test (CI-equivalent)                               |
| `make ui-dev`                               | Vite on :5173, proxies `/v1` + `/healthz` + `/metrics` to :8080       |
| `make ui-build` / `ui-check` / `ui-install` | UI build / typecheck / `npm ci`                                       |
| `make swagger-sync`                         | Copy `api/openapi/openapi.yaml` → `internal/api/swagger/openapi.yaml` |
| `make swagger-sync-check`                   | CI guard: fails if the embedded copy drifted from source              |

## Key conventions

- **Errors**: RFC 7807 `application/problem+json`. Store sentinels: `ErrNotFound`, `ErrConflict`, `ErrLastAdmin`.
- **Pagination**: cursor-based on list endpoints; uniform `name=` (ci substring / `*`-glob), `sort=` (per-entity allowlist) + `order=` since ADR-0042. Cursors are tagged base64-JSON, bound to their sort params; mismatched replay → 400.
- **Updates**: merge-patch via PATCH; collector tick writes only its own fields, leaving curated metadata alone.
- **JSONB**: heterogeneous specs (workload `spec`, `containers`, `conditions`, `taints`, `load_balancer`, `applications`, `annotations`).
- **FK chain**: `clusters` → `namespaces`/`nodes`/`persistent_volumes` → `pods`/`workloads`/`services`/`ingresses`/`pvcs`, all `ON DELETE CASCADE`. `pods.workload_id` and `pvcs.bound_volume_id` are `ON DELETE SET NULL`.
- **Kyverno policies (ADR-0043)**: `cluster_policies` + `policy_reports` tables, gated by `policies_enabled` setting (default off, seeded from `LONGUE_VUE_POLICIES_ENABLED`). When off, all policy endpoints return 409. UI has a Policies sidebar page with 409 banner. Two POST endpoints (`POST /v1/cluster-policies`, `POST /v1/policy-reports`) use a `source` discriminator column (`'collector'`/`'api'`) so the reconcile sweep only deletes collector-originated rows.
- **Reconcile semantics**: only run after a successful list; transient API errors must never wipe the store. Workload reconcile keys on `(kind, name)`.
- **VMs vs nodes**: `virtual_machines` is a separate top-level table from `nodes`; dedup on POST checks `provider_id` substring against `nodes`.

## Auth (ADR-0007, ADR-0015 §5, ADR-0017)

- Dual-path middleware: session cookie (humans, login or OIDC) **or** `Authorization: Bearer longue_vue_pat_…`. Both feed `Caller{id, kind, role, scopes}`.
- Roles: `admin` / `editor` / `auditor` / `viewer` plus the machine-only `vm-collector` token preset bound to a `cloud_account` UUID at issuance.
- `auth.HasScope`: admin implies everything **except** `vm-collector` (preserves the SK-read boundary).
- First-run bootstrap creates one admin from `LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD` or a random one printed once; `must_change_password=true` blocks all but `/v1/auth/me`, `/v1/auth/change-password`, `/v1/auth/logout`, and `/openapi.yaml` (so the docs page can load).
- Last-admin guard: `UpdateUserGuarded` / `DeleteUserGuarded` use `SELECT … FOR UPDATE` over active admins; returns `ErrLastAdmin` → 409.
- Account auto-locks after 6 consecutive failed password verifications (`failed_login_count`, `locked_at`). No last-admin guard — the only admin can be locked. Admin unlocks via `PATCH /v1/admin/users/{id}` with `unlock=true`. If every admin is locked out: set `LONGUE_VUE_ADMIN_RESCUE_PASSWORD` and restart — the boot hook resets the most-recently-active admin's password, clears the lock, and forces `must_change_password=true`.
- OIDC optional via `LONGUE_VUE_OIDC_ISSUER`; PKCE+nonce+state, one-shot rows in `oidc_auth_states`. Shadow users default to `viewer`; group claims are not trusted.
- Removed env vars `LONGUE_VUE_API_TOKEN(S)` are a hard startup error — migrate to admin-issued PATs.

## Audit log

`AuditMiddleware` (mounted after auth) records all non-GET + every `/v1/admin/*` read and the credentials-fetch path into `audit_events`. Body scrubber redacts password / token / OIDC-secret / `access_key` / `secret_key`. Insert failures log at ERROR but never 5xx. `audit_events.source ∈ {api, ingest_gw, system}`. Extract endpoints (`/v1/search/extract`, `/v1/search/extract.zip`, `/v1/eol/extract`) are GET-but-audited via the `shouldAudit` allowlist; each request gets a row with `details.action="extract"`, plus page/format/filters/`row_count`/`truncated`.

## Listeners (ADR-0016, ADR-0017)

- Public `:8080` — humans + machines. TLS via `LONGUE_VUE_PUBLIC_LISTEN_TLS_{CERT,KEY}` (mtime hot reload).
- Ingest mTLS-only `:8443` (opt-in via `LONGUE_VUE_INGEST_LISTEN_ADDR`) — exposes the 22 push-collector writes + `POST /v1/auth/verify`. Allowlist in `internal/ingestgw/allowlist.go` must stay in sync with `internal/api/ingest_mux.go`.
- `LONGUE_VUE_TRUSTED_PROXIES` (CIDRs, empty by default) gates trust in `X-Forwarded-For` / `-Proto`. Used by rate-limiter, audit IP, secure-cookie decision, HSTS — see `internal/httputil/`.
- `LONGUE_VUE_REQUIRE_HTTPS=true` startup guard: refuse boot unless native TLS is on **or** trusted-proxy + `SecureAlways` cookie posture is set.
- `/healthz`, `/readyz`, `/metrics` are unauthenticated.

## API docs (ADR-0025)

Interactive Swagger UI 5.x is served at `/docs/` on the public listener.
The shell is unauthenticated (matches the `/ui/*` precedent); the spec
itself lives at `/openapi.yaml` and is gated under `requireReadScope` +
auth middleware. "Try it out" carries the operator's session cookie
(`withCredentials: true`) or a PAT pasted via the Authorize dialog.

The Swagger UI bundle is vendored under `internal/api/swagger/dist/`
(pinned in `dist/.version`); `internal/api/swagger/index.html` is our
hand-written bootstrap and sits **outside** `dist/` so upstream upgrades
are a straight directory replacement. The OpenAPI spec is copied into
`internal/api/swagger/openapi.yaml` by `make swagger-sync` and enforced
by `make swagger-sync-check` in CI.

Docs are unaffected by the `noui` build tag — the swagger package has
no dependency on the React SPA bundle.

## Secrets (ADR-0015 §4)

`internal/secrets` — AES-256-GCM with AAD bound to the row PK. Master key from `LONGUE_VUE_SECRETS_MASTER_KEY` (base64 32 bytes). Required only when any `cloud_accounts` row carries an encrypted SK. `Fingerprint()` (first 8 hex of SHA-256) logged at startup.

## Cloud accounts + VM collector (ADR-0015, ADR-0019)

- `cloud_accounts` status: `pending_credentials` → `active` → `error` → `disabled`. Hybrid onboarding: collector POSTs a placeholder, admin pastes AK/SK in UI, collector picks up on next refresh.
- AK plaintext (public identifier); SK encrypted. Plaintext SK only ever leaves via `GET /v1/cloud-accounts/by-{name,id}/{x}/credentials` — audit-logged, response body never captured.
- `virtual_machines.applications` JSONB (curator-only; collector never writes it). GIN `jsonb_path_ops` index. Soft-delete via `terminated_at`; reconcile never hard-deletes.
- VM list filters: `cloud_account_id|name`, `region`, `role`, `power_state`, `include_terminated`, `name`, `image`, `application`, `application_version`. LIKE metacharacters escaped; product names normalized server-side.

## Settings + feature toggles

Single-row `settings` table (`id=1 CHECK`). Runtime toggles `eol_enabled`, `mcp_enabled`, `policies_enabled` (admin scope, hand-written `GET`/`PATCH /v1/admin/settings`, not in OpenAPI). Env vars seed the initial value. `policies_enabled` (default off, seeded from `LONGUE_VUE_POLICIES_ENABLED`) gates all Kyverno policy endpoints (409 when off).

**Image versions enricher (`image_versions_enabled`, ADR-0022):** queries public registries for the latest tag of each container image used in workloads/pods. Default interval 24h (`LONGUE_VUE_IMAGE_VERSIONS_INTERVAL`). Allowlist of registries is in `image_versions_registries` (DB-backed, admin CRUD). Computes a `freshness` field (`up_to_date` / `outdated` / `far_behind` / `unknown`) based on minor/major version distance — see ADR-0041. This replaces the former `eol_status` field on `ContainerVersionInfo`. Freshness is surfaced via `GET /v1/container-freshness` and the Container Freshness UI page (`/container-freshness`); it does **not** appear in the EOL dashboard or extract.

**Manual origin mappings (ADR-0030, table `image_origin_mappings`).**
Operator-curated `image_name → public_registry` dictionary consulted by
the mirror resolver before OCI annotation fetch. CRUD via
`/v1/admin/image-origin-mappings/*` (admin scope). The OCI fallback path
(ADR-0026 / ADR-0028) stays in place for unmapped images. Metric
`imageversions_mirror_resolve_total{result="ok_manual"}` distinguishes
manual vs auto resolution.

## Collectors

- `internal/collector/` — K8s pull + push. Same package, two consumers (`cmd/longue-vue` in-process, `cmd/longue-vue-collector` via apiclient through ingest GW). Multi-cluster via `LONGUE_VUE_COLLECTOR_CLUSTERS` JSON (legacy single-cluster vars still work). Reconcile is on by default; per-namespace for namespaced resources, cluster-scoped for nodes + PVs. Pods resolve `workload_id` by walking `ownerReferences` (RS→Deployment, or direct StatefulSet/DaemonSet). NetworkPolicy collection works in both modes since ADR-0038.
- `internal/vmcollector/` — push-mode. `Provider` interface in `provider/`; Outscale impl + fake. `apiclient/` mirrors the K8s collector's CA/mTLS/proxy transport. `filter/` is a cheap pre-filter; server-side `nodes.provider_id` dedup is canonical. Private Prometheus registry.

## Curated metadata pattern

Cluster carries `owner` / `criticality` / `notes` / `runbook_url` / `annotations` JSONB, merge-patched via PATCH; collector leaves them alone. UI: "Ownership & context" card with inline edit. Same pattern queued for namespaces / nodes / workloads.

**DICT (ADR-0029).** DICT security-need axes (`sec_disponibilite`/`_integrite`/`_confidentialite`/`_tracabilite` 0..4 + `sec_notes`) live **only** on the `applications` row — the Application is their home per the EBIOS-RM convention. Linked workloads + VMs surface a read-only `effective_dict` (application-wins precedence). Note: ADR-0008's planned namespace/workload DICT columns were never built (slot `00023` became `create_cloud_accounts`), so `effective_dict.source` is `application` or `none` in practice and `longue_vue_dict_coverage{source="workload"}` is always 0. The classification heat-map (`/ui/admin/classification`) + `GET /v1/applications/extract.csv?dict_min=N` produce the SNC §8.3 evidence.

## EOL enricher (ADR-0012, ADR-0019)

`internal/eol/` — periodic endoflife.date queries; writes `longue-vue.io/eol.<product>` annotations on clusters, nodes, and (per VM `applications` entry) VMs. Stale keys reaped per tick. `latest_available` field shows newest published version. Centralised on the server — push collectors are unaffected.

**Per-application aggregation (ADR-0029, amended ADR-0041).** `GET /v1/applications/{id}/eol` (and the detail-page EOL card) call `internal/eolagg` to roll up EOL signal across a linked Application's members at read time (no new enricher pass). VM member rows come from `longue-vue.io/eol.*` endoflife annotations (eol / approaching / supported / unknown). Each row carries a `sources` list of contributing assets and a `signal` field (`eol` for endoflife.date-backed VM rows, `freshness` for workload image rows). Workload image rows surface as `signal="freshness"` and are excluded from EOL `statusRank` precedence.

## Extracts (search & EOL)

**Extracts (search & EOL):** bulk download of Search results (`/v1/search/extract?kind=workloads|pods|virtual_machines&format=csv|json` and `/v1/search/extract.zip?q=...`) and the EOL Dashboard (`/v1/eol/extract?format=csv|json`). The EOL extract covers clusters, nodes, and VMs only — `entity_type=workload` returns `400`. Capped at `LONGUE_VUE_EXTRACT_MAX_ROWS` (default 50 000); `X-Longue-Vue-Truncated: true` header signals the cap. Audit-logged. Aggregation lives in `internal/eolagg`. Container image freshness is available via `GET /v1/container-freshness` (ADR-0041).

## Idempotency

`POST /v1/clusters` is idempotent on `name` via `EnsureCluster` (200 on hit, 201 on insert; never returns `ErrConflict`).
