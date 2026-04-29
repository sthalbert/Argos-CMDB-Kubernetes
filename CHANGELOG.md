# Changelog

All notable changes to Argos are recorded here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
— the REST and database contracts may still change incompatibly before
`v1.0.0`.

## [0.11.1] — 2026-04-29

Helm chart 0.14.0 / appVersion 0.11.1. Hardening release driven by the
2026-04-28 penetration test. Closes three P0 findings — plaintext-HTTP
credential transit (AUTH-VULN-01/02/03), forgeable `X-Forwarded-For`
rate-limit bypass (AUTH-VULN-04), and admin-account orphaning via the
admin-user lifecycle endpoints (AUTHZ-VULN-01/02). New ADR-0017 documents
the public-listener TLS posture and proxy-trust contract introduced here.

### Security

- **Native TLS termination on the public listener** (ADR-0017 §4) —
  argosd can now serve HTTPS directly when `ARGOS_PUBLIC_LISTEN_TLS_CERT`
  and `ARGOS_PUBLIC_LISTEN_TLS_KEY` are set. Cert + key are loaded at
  startup, hot-reloaded via fsnotify on file change (works with cert-manager,
  Vault Agent atomic-rename, manual file writes), and pinned to TLS 1.3 with
  session tickets disabled. Refuses to start on parse error rather than
  falling through to plain HTTP.
- **Trust-aware Secure cookie + HSTS + client IP resolution** (ADR-0017 §5)
  — `X-Forwarded-For` and `X-Forwarded-Proto` are honored only when the
  immediate TCP peer's IP falls inside `ARGOS_TRUSTED_PROXIES` (a
  comma-separated CIDR list). Empty list = ignore both headers entirely
  (the secure default). Fixes AUTH-VULN-04: a remote client sending
  `X-Forwarded-For: <victim-ip>` could previously bypass per-IP rate
  limits on `/v1/auth/login`. Fixes AUTH-VULN-02: the session cookie's
  `Secure` flag now reflects the resolved transport, not a forgeable
  XFP header. Fixes AUTH-VULN-03: HSTS is emitted only over a verified
  HTTPS request, with a force-emit override for operators declaring the
  full deployment HTTPS-only.
- **Startup posture guard** (ADR-0017 §7) — `ARGOS_REQUIRE_HTTPS=true`
  refuses to boot unless either native TLS is configured (cert + key
  present) or both `ARGOS_TRUSTED_PROXIES` is non-empty AND
  `ARGOS_SESSION_SECURE_COOKIE=always`. Fails closed: "warn and serve
  plain HTTP" is not an option once the operator has declared the
  deployment HTTPS-only.
- **Last-admin invariant guard on `DELETE /v1/admin/users/{id}` and
  `PATCH /v1/admin/users/{id}`** (AUTHZ-VULN-01/-02) — both endpoints
  now refuse with `409 Conflict` when the operation would leave the
  deployment with zero active admins. The check + write run in a single
  PostgreSQL transaction with `SELECT … FOR UPDATE` on the active-admin
  set, closing the TOCTOU race two concurrent demotions could otherwise
  exploit. New `Store.UpdateUserGuarded` and `Store.DeleteUserGuarded`
  methods plus a new `api.ErrLastAdmin` sentinel.

### Added

- **`internal/httputil`** — small package centralising the trust-aware
  client-IP / IsHTTPS helpers (`ParseTrustedCIDRs`, `ClientIP`, `IsHTTPS`).
  All previously-duplicated XFF parsing routes through it; downstream
  code is single-sourced.
- **`internal/tlsutil`** — extracted `CertReloader` from `internal/ingestgw`
  so both the public and ingest listeners share the same fsnotify-driven
  hot-reload path. Both listeners now use the same loader, the same parse
  guards, and the same atomic-rename support.
- **Helm chart `argosd.tls` block** — `existingSecret` references a
  `kubernetes.io/tls` Secret; the chart mounts it at
  `/etc/argos/tls` and wires `ARGOS_PUBLIC_LISTEN_TLS_CERT/_KEY`
  automatically.
- **Helm chart `argosd.trustedProxies` and `argosd.requireHTTPS`** —
  surface the new env vars so operators don't have to reach for
  `extraEnv:` overrides.
- **OpenAPI: `409 Conflict` on `PATCH /v1/admin/users/{id}`** —
  documents the last-admin guard so generated clients render the
  conflict correctly. The DELETE endpoint already declared 409.

### Changed

- **`api.AuthMiddleware` signature** — gains a `trustedProxies []*net.IPNet`
  parameter; callers must update. Pass nil to ignore proxy headers
  (the secure default).
- **`api.AuditMiddleware` signature** — gains a `trustedProxies []*net.IPNet`
  parameter so audit-event source IPs reflect the trust-aware client IP,
  not the immediate proxy peer.
- **`auth.Middleware` signature** — gains a `trustedProxies []*net.IPNet`
  parameter, threaded through to the cookie helpers
  (`SessionCookie`, `SetSessionCookie`, `ClearSessionCookie`).

### Migration

No DB migration. Two operational changes:

1. **If you run argosd behind a TLS-terminating reverse proxy** (the
   common pattern: ingress-nginx, Envoy, a cloud LB), set
   `ARGOS_TRUSTED_PROXIES` to the proxy's CIDR(s) and pin
   `ARGOS_SESSION_SECURE_COOKIE=always`. Without trust, the upgrade
   silently downgrades cookie security and rate-limit accuracy — the
   defaults are the safest possible state, not the most useful.

2. **If you want HSTS / require HTTPS**, set `ARGOS_REQUIRE_HTTPS=true`.
   The pod will refuse to start unless one of the two postures (native
   TLS or trusted-proxy + SecureAlways) is present — failing closed
   beats accidentally serving credentials over plain HTTP.

`charts/argos` upgrades transparently with the existing values: the new
`argosd.tls`, `argosd.trustedProxies`, `argosd.requireHTTPS` keys all
default to empty / false, so existing releases are unaffected until the
operator opts in.

## [0.11.0] — 2026-04-28

Helm chart 0.13.0 / appVersion 0.11.0. Adds the DMZ ingest gateway track
from ADR-0016: Argos can now accept collector push traffic through a
hardened perimeter component (`argos-ingest-gw`) without exposing argosd
to the internet. Also makes `POST /v1/clusters` idempotent on `name` so
the collector no longer needs a read-before-write at startup.

### Added

- **`argos-ingest-gw` binary** (ADR-0016) — standalone stateless
  reverse-proxy for the DMZ. Exposes a TLS inbound listener (`:8443`,
  Envoy/WAF-fronted) and a health/metrics listener (`:9090`, pod-IP only,
  no TLS). No database, no queue, no replay buffer. Source under
  `cmd/argos-ingest-gw/`; shared gateway logic under `internal/ingestgw/`.
  Built `CGO_ENABLED=0` from `Dockerfile.ingest-gw`, distroless base, UID
  65532.
- **Helm chart `argos-ingest-gw`** (chart `0.1.0`) — first ship of the
  gateway chart. Ships independently of the umbrella `argos` chart so the
  DMZ release contains only what belongs in the DMZ. Three TLS cert-source
  modes: `vault` (Vault Agent sidecar + PKI secrets engine, hot-rotate at
  50% TTL), `secret` (Kubernetes `kubernetes.io/tls` Secret, works with
  cert-manager), `file` (operator-mounted path, any tooling). Default 2
  replicas + PodDisruptionBudget. Optional ServiceMonitor + PrometheusRule
  (suggested alerts shipped as values block). NetworkPolicy restricts
  egress to argosd's ingest port only (plus Vault CIDRs in `vault` mode).
- **mTLS-only ingest listener on argosd** (ADR-0016 §3) — a second
  `*http.Server` starts when `ARGOS_INGEST_LISTEN_ADDR` is set (disabled
  when empty; existing deployments unaffected). The listener requires
  `RequireAndVerifyClientCert` (TLS 1.3 floor, session tickets disabled)
  and is wired by `api.NewIngestMux`, which registers exactly 19 routes:
  the 18 collector writes plus `POST /v1/auth/verify`. New env vars:
  `ARGOS_INGEST_LISTEN_ADDR`, `ARGOS_INGEST_LISTEN_TLS_CERT`,
  `ARGOS_INGEST_LISTEN_TLS_KEY`, `ARGOS_INGEST_LISTEN_CLIENT_CA_FILE`,
  `ARGOS_INGEST_LISTEN_CLIENT_CN_ALLOW`.
- **`POST /v1/auth/verify`** (ADR-0016 §5) — internal-only endpoint
  registered exclusively on the mTLS-only ingest listener (not on `:8080`).
  The gateway calls it to short-circuit invalid tokens before forwarding
  writes across the firewall. Response carries `valid`, `caller_id`,
  `kind`, `scopes`, `bound_cloud_account_id`. Rate-limited at argosd to
  100 req/s per source IP (burst 200) via the new `api.VerifyRateLimiter`.
  The `token` field in captured request bodies is scrubbed by the audit
  middleware.
- **`audit_events.source` column** (migration `00027_audit_events_source.sql`)
  — distinguishes `api` (public listener), `ingest_gw` (mTLS ingest
  listener, DMZ-origin writes), and `system` (synthetic argosd-emitted
  events). Empty strings in pre-ADR-0016 rows are treated as `api` for
  backwards compatibility. `AuditMiddleware` now accepts a `source` string
  argument; existing call sites pass `"api"`, the ingest-listener wiring
  passes `"ingest_gw"`. `GET /v1/admin/audit` accepts an optional `source`
  query parameter.
- **Gateway Prometheus metrics** — new metrics on the gateway's private
  registry: `argos_ingest_gw_requests_total{method,route,status_class,outcome}`,
  `argos_ingest_gw_request_duration_seconds`, `argos_ingest_gw_upstream_duration_seconds`,
  `argos_ingest_gw_token_verify_total{result}`,
  `argos_ingest_gw_token_cache_total{event}`, `argos_ingest_gw_token_cache_size`,
  `argos_ingest_gw_cert_not_after_seconds`, `argos_ingest_gw_cert_reload_total{result}`,
  `argos_ingest_gw_body_bytes`, `argos_ingest_gw_inflight_requests`,
  `argos_ingest_gw_build_info`. Argosd gains `argos_auth_verify_total{result}`
  and `argos_ingest_listener_client_cert_failures_total{reason}`.

### Changed

- **`POST /v1/clusters` is now idempotent on `name`** (ADR-0016 §6) —
  `api.Store.EnsureCluster` replaces `CreateCluster`. A new row returns
  `201 Created`; an existing row returns `200 OK` with the existing record
  (request body ignored on hit). The collector no longer issues a startup
  `GET /v1/clusters?name=…` — it unconditionally POSTs and follows up with
  PATCH regardless of the 200/201 response. One round-trip removed from
  every collector startup, for all deployments, whether or not the gateway
  is in play.
- **`api.NewServer` signature** — gains a `verifyLimiter *VerifyRateLimiter`
  parameter (pass nil to disable rate limiting, e.g. in test fixtures).
- **`api.AuditMiddleware` signature** — gains a `source string` parameter
  (`"api"` or `"ingest_gw"`); callers must update.

### Migration

Migration `00027_audit_events_source.sql` adds a nullable `source` column
to `audit_events`. The `ARGOS_AUTO_MIGRATE=true` default applies it on
startup. Rows inserted by the previous version carry a NULL source; queries
treat NULL as `"api"` for backwards compatibility.

`api.AuditMiddleware` now requires a `source` argument. Any code outside
`cmd/argosd/main.go` that constructs a middleware chain must be updated to
pass `"api"` or the appropriate source string.

`api.NewServer` requires the new `verifyLimiter` argument; pass
`api.NewVerifyRateLimiter()` in production and nil in tests.

No changes to existing API consumers. The new `200` status from
`POST /v1/clusters` is additive; clients treating `201` as the only
success code should be updated to also accept `200`, but the old behaviour
(checking for the row's existence first and then POSTing) still works
through the `PATCH` path.

## [0.10.0] — 2026-04-26

Helm chart 0.12.0 / appVersion 0.10.0. Adds the VM-collector track from
ADR-0015: Argos now inventories the non-Kubernetes platform VMs sitting
underneath the clusters (VPN, DNS, Bastion, Vault, …) per cloud account,
with encrypted-at-rest credentials and a separate push-mode collector
binary.

### Added

- **VM collector binary `argos-vm-collector`** (ADR-0015 §1, §IMP-004) —
  standalone push-mode binary mirroring `argos-collector`. Stateless,
  distroless, UID 65532, env-var configured. One binary instance per
  cloud account; multi-account = N deployments. Source under
  `cmd/argos-vm-collector/`; reusable polling logic under
  `internal/vmcollector/`.
- **`cloud_accounts` table** (ADR-0015 §3) — operator-editable
  cloud-provider accounts with status workflow
  (`pending_credentials` → `active` → `error` / `disabled`),
  encrypted SK column, and curated metadata. Migration
  `00023_create_cloud_accounts.sql`.
- **`virtual_machines` table** (ADR-0015 §2) — top-level table for
  non-Kubernetes platform VMs. FK to `cloud_accounts(id)`
  `ON DELETE CASCADE`. Captures the rich Outscale payload (image AMI,
  keypair, VPC/subnet, NICs, SGs, block devices, deletion protection,
  provider creation date) plus the curated-metadata five-tuple. Soft
  delete via `terminated_at` so the audit history of decommissioned
  VMs is preserved. Migration `00024_create_virtual_machines.sql`.
- **`vm-collector` token scope** (ADR-0015 §5 / IMP-008) — narrowest
  scope in the system. Grants exactly: fetch own credentials, register
  placeholder cloud account, heartbeat status updates, upsert VMs,
  reconcile VMs. PATs are bound to a single `cloud_account_id` at
  issuance via the new `tokens.bound_cloud_account_id` column
  (migration `00025_add_token_bound_cloud_account.sql`); enforced by
  `auth.Caller.EnforceCloudAccountBinding`. The token-issuance UI
  requires picking a bound cloud account when the `vm-collector`
  preset is selected.
- **Master-key envelope encryption** (ADR-0015 §4 / IMP-002) — new
  package `internal/secrets/`. AES-256-GCM with the master key from
  `ARGOS_SECRETS_MASTER_KEY` (base64-encoded 32 bytes; rejected at
  startup on any other length). AAD bound to the row UUID so a
  database backup-restore cannot move a ciphertext between rows.
  Master-key fingerprint (first 8 hex chars of SHA-256) logged at
  startup. Master key is required only when at least one
  `cloud_accounts` row carries a non-NULL `secret_key_encrypted`.
- **Outscale provider** (ADR-0015 §IMP-003) — first cloud-provider
  implementation behind the new `internal/vmcollector/provider.Provider`
  seam. Wraps `github.com/outscale/osc-sdk-go/v2`, maps `osc.Vm` into
  the canonical `VM` struct, hardcodes `ansible_group` as the role-tag
  key, parses TINA-family instance types into CPU/memory, normalises
  Outscale's `shutting-down` to the AWS-spelling `terminating` so
  `power_state` carries one vocabulary across providers.
- **Tag-driven kube-node dedup** (ADR-0015 §8) — server-side check on
  `POST /v1/virtual-machines`: looks up `nodes.provider_id LIKE '%' ||
  $1 || '%'` against the posted `provider_vm_id` (Outscale CCM stamps
  the `VmId` substring into `node.spec.providerID`). 409
  `already_inventoried_as_kubernetes_node` on hit; the collector
  skips. Tag-independent — works for any cloud-controller-manager.
  Local pre-filter on `OscK8sClusterID/*` / `OscK8sNodeName=*` /
  `argos.io/ignore=true` saves the round-trip per kube worker.
- **New endpoints** (ADR-0015 §IMP-006):
  - `POST /v1/admin/cloud-accounts`,
    `GET /v1/admin/cloud-accounts`,
    `GET /v1/admin/cloud-accounts/{id}`,
    `PATCH /v1/admin/cloud-accounts/{id}`,
    `PATCH /v1/admin/cloud-accounts/{id}/credentials`,
    `POST /v1/admin/cloud-accounts/{id}/disable` and `/enable`,
    `DELETE /v1/admin/cloud-accounts/{id}` — all admin scope.
  - `GET /v1/cloud-accounts/by-name/{name}/credentials` and
    `GET /v1/cloud-accounts/{id}/credentials` — `vm-collector` scope,
    the only places plaintext SK leaves the database.
  - `POST /v1/cloud-accounts` — `vm-collector` scope, idempotent
    first-contact registration.
  - `PATCH /v1/cloud-accounts/{id}/status` — `vm-collector` scope,
    heartbeat-only.
  - `POST /v1/virtual-machines`,
    `POST /v1/virtual-machines/reconcile` — `vm-collector` scope.
  - `GET /v1/virtual-machines`, `GET /v1/virtual-machines/{id}` —
    `read` scope.
  - `PATCH /v1/virtual-machines/{id}` — `write` scope, curated
    metadata only.
  - `DELETE /v1/virtual-machines/{id}` — `delete` scope, soft delete.
- **Soft-delete reconciliation** (ADR-0015 §9) —
  `POST /v1/virtual-machines/reconcile` flips `terminated_at = NOW()`
  + `power_state = 'terminated'` + `ready = false` for rows whose
  `provider_vm_id` is not in the keep list. Rows are never
  hard-deleted by reconciliation. A reappearing
  `(cloud_account_id, provider_vm_id)` is resurrected by clearing
  `terminated_at`.
- **Hybrid onboarding flow** (ADR-0015 §6) — operator deploys the
  collector with only a PAT and an account name; the collector posts
  a placeholder row to `POST /v1/cloud-accounts`, the admin sees a
  red "pending credentials" banner in the UI, pastes AK/SK, the
  collector picks them up on the next refresh tick. Hot AK/SK
  rotation works the same way: PATCH `/credentials`, collector picks
  up the new SK within `ARGOS_VM_COLLECTOR_CREDENTIAL_REFRESH`
  (default 1 h).
- **Virtual Machines and Cloud Accounts UI pages** (ADR-0015 §10) —
  `/ui/virtual-machines` list + detail (mirrors Node detail layout
  via cards extracted into `ui/src/components/inventory/`),
  `/ui/admin/cloud-accounts` list with status badges and "Set
  credentials" / "Rotate credentials" / "Disable" forms, "Issue
  collector token" button pre-binding the new PAT to the cloud
  account. Sidebar gains a new "Virtual Machines" entry with a
  distinct server/tower SVG icon. Home-page admin banner surfaces
  the count of `pending_credentials` accounts.
- **Prometheus metrics**:
  - argosd: `argos_cloud_accounts_total{status}`,
    `argos_cloud_accounts_pending_credentials` (gauge for alerting),
    `argos_virtual_machines_total{cloud_account, terminated}`,
    `argos_credentials_reads_total{cloud_account}`.
  - collector binary: `argos_vm_collector_ticks_total{status}`,
    `argos_vm_collector_tick_duration_seconds`,
    `argos_vm_collector_vms_observed`,
    `argos_vm_collector_vms_skipped_kubernetes_total`,
    `argos_vm_collector_credential_refreshes_total{result}`,
    `argos_vm_collector_last_success_timestamp_seconds`,
    `argos_vm_collector_build_info{version}`. Exposed on a private
    registry on a localhost-only `/metrics` listener.

### Changed

- **`internal/auth.HasScope` no longer treats admin scope as implying
  `vm-collector`** (ADR-0015 §5). Only collector tokens carrying the
  scope explicitly can read plaintext SK; admin tokens can manage
  cloud-account metadata via the admin endpoints but cannot exercise
  the credentials-fetch endpoint. Preserves the
  SK-is-write-only-from-admin-endpoints guarantee.
- **Sidebar reorganised into Kubernetes / Cloud Infrastructure /
  Tools sections** so the new Virtual Machines entry sits alongside
  the cloud-account admin link without crowding the existing
  Kubernetes inventory drill-down.
- **Audit middleware now wraps the hand-written cloud-accounts and
  VM routes** (ADR-0015 §IMP-007). These hand-written routes
  previously bypassed `AuditMiddleware` (a security gap); they now
  produce audit rows for every write, plus every credentials-fetch
  GET (the response body is intentionally never logged, even at
  debug level). The scrubber list gains `secret_key` and
  `access_key`.

### Security

- **Encrypted-at-rest cloud-provider AK/SK** — secret keys live as
  AES-256-GCM ciphertexts AAD-bound to their row UUID; database
  backup-restore alone cannot move a ciphertext to another row.
  Master key required only when at least one row carries a non-NULL
  `secret_key_encrypted`; argosd refuses to start otherwise.
- **vm-collector PATs bound to a single cloud account** — a leaked
  collector PAT exposes exactly one account's credentials and one
  account's VM writes. Strictly less than a `read`-scope PAT
  (which can list every entity in the CMDB).
- **LIKE-wildcard escaping on the dedup query** — `_` and `%` inside
  `provider_vm_id` are escaped before interpolation into the
  `nodes.provider_id LIKE '%' || $1 || '%'` lookup, so a maliciously
  named VM cannot match every node row.

### Upgrading

Migrations `00023` / `00024` / `00025` are additive (the schema for
new tables, plus a nullable column on `tokens`); the
`ARGOS_AUTO_MIGRATE=true` default applies them on startup. Existing
deployments without any `cloud_accounts` row do not need
`ARGOS_SECRETS_MASTER_KEY`; argosd only refuses to start when the
table contains an encrypted SK and the env var is unset.

The Helm chart bumps to `0.12.0` / appVersion `0.10.0`. The new
`secrets.masterKey` value (delivered via a Kubernetes Secret, never
in `values.yaml`) is required only if you intend to register a cloud
account.

## [0.7.0] — 2026-04-24

### Added

- **Impact analysis graph** (ADR-0013) — server-side dependency graph
  traversal from any CMDB entity. New endpoint
  `GET /v1/impact/{entity_type}/{id}?depth=2` walks FK relationships
  bidirectionally across all 9 entity types with 4 relation types
  (`contains`, `owns`, `hosts`, `binds`). Depth-limited to 1–3 hops.
  Interactive SVG diagram on every entity detail page with depth selector
  and click-to-navigate. Prometheus metrics:
  `argos_impact_queries_total`, `argos_impact_query_duration_seconds`.

- **MCP server** (ADR-0014) — Model Context Protocol server exposing 17
  read-only CMDB tools for AI agents. SSE and stdio transports. Bearer
  token auth. Admin toggle at Admin > Settings. Prometheus metrics.

- **UI redesign — design system alignment** — CSS migrated to canonical
  design system tokens (~50 CSS variables). Space Grotesk (headings/body)
  and JetBrains Mono (code) webfonts installed via Google Fonts. 11 SVG
  entity icons added to sidebar navigation, list page headings, detail
  page headings, EOL dashboard, and login page.

- **Sidebar navigation** — top nav bar replaced with a left sidebar.
  Entity links (Clusters, Namespaces, Nodes, etc.) are always visible
  with icons. Burger button collapses the sidebar to icons-only (48px).
  Active link highlighted with cyan accent + left border. Top header
  bar retained for app title, username, role pill, and sign out.

### Changed

- **EOL Inventory redesign** — the EOL Dashboard is renamed to
  "End-of-Life Inventory". Table columns are grouped into "what we run"
  (Status, Product, Version, Patch, Entity, Cluster) and "what's
  available" (Latest Available, EOL Date, Checked) with a visual
  separator. Rows are highlighted red for EOL, orange for approaching
  EOL. Column renames: "Cycle" → Version, "Cycle Latest" → Patch.

- **`latest_available` field in EOL annotations** — the enricher now
  stores the newest version of the product published on endoflife.date
  (e.g. `1.32.3` when the entity runs `1.28`). Zero additional API
  calls — the data is already fetched.

### Fixed

- **Workload detail missing pods on large clusters** — the WorkloadDetail
  page fetched all pods cluster-wide (`limit=200`) and filtered
  client-side by `workload_id`. On clusters with 500+ pods,
  StatefulSet pods (long-lived, less recently updated) fell outside the
  first pagination page and were never displayed. Fixed by adding a
  server-side `workload_id` query parameter to `GET /v1/pods` so the
  API returns only the matching pods.

- **Pod pages showing UUIDs instead of names** — the Pods list page
  rendered workload as a truncated UUID; the Pod detail page rendered
  both namespace and workload as UUIDs. Both now resolve and display
  human-readable names with links.

### Security

- **HTTP security headers** — new middleware sets `Content-Security-Policy`,
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  `Referrer-Policy`, and `Strict-Transport-Security` (HSTS, conditional
  on TLS) on every response.

- **Login rate limiting** (ADR-0007 IMP-009) — per-IP sliding window
  rate limiter on `POST /v1/auth/login`: 5 requests/minute, burst 5.
  Returns 429 when exceeded. Idle IPs evicted after 30 minutes.

- **golang.org/x/net upgrade** — v0.50.0 → v0.51.0 fixes GO-2026-4559
  (HTTP/2 server panic via crafted frames).

- **Request body size limit** — all POST/PATCH/PUT bodies are capped at
  1 MiB via `http.MaxBytesHandler`. Returns 413 when exceeded.

- **HTTP server timeouts** — `ReadTimeout: 30s`, `WriteTimeout: 60s`,
  `IdleTimeout: 120s` prevent slowloris-style connection exhaustion.

- **Error message sanitization** — `ResponseErrorHandlerFunc` and
  settings handlers no longer leak internal error details (database
  messages, constraint names) to clients. Errors are logged server-side
  and a generic message is returned.

- **Impact graph traversal cap** — graph nodes capped at 500 per query
  to prevent resource exhaustion on large clusters. Response includes
  `truncated: true` when the cap is hit.

- **Reconcile endpoints require `delete` scope** — all 8 reconcile
  endpoints (`POST /v1/{resource}/reconcile`) now require the `delete`
  scope instead of `write`. Prevents editors from mass-deleting
  resources via empty `keep_names`.

### Upgrading

The `workload_id` query parameter on `GET /v1/pods` and the impact
endpoint are additive. EOL annotations are updated with `latest_available`
on the next enrichment tick.

**Breaking change:** reconcile endpoints now require `delete` scope.
Existing push collector tokens with only `write` scope must be re-issued
with `write` + `delete` scopes.

## [0.1.1] — 2026-04-20

Patch release on top of `v0.1.0` "Canopus". Adds the first two steps of
the ADR-0008 asset-management rollout (curated metadata on Namespace
and Node, including `hardware_model`) and fixes three UUID-instead-of-
name rendering bugs on detail pages. Schema is additive only; `v0.1.0`
→ `v0.1.1` is a straight `ARGOS_AUTO_MIGRATE=true` bump, no data
migration required.

### Added

- **Curated metadata on Namespace**
  ([#56](https://github.com/sthalbert/Argos/pull/56)) — `owner` /
  `criticality` / `notes` / `runbook_url` / `annotations` (JSONB)
  columns editable at `/ui/namespaces/:id` by editor / admin. The
  collector's `UpsertNamespace` leaves these columns alone on conflict
  so per-tick upserts can't clobber operator edits.
- **Curated metadata on Node + `hardware_model`**
  ([#57](https://github.com/sthalbert/Argos/pull/57)) — same five
  curated columns on nodes plus a free-form `hardware_model` field for
  bare-metal installs to record a server model alongside the cloud-shaped
  `instance_type` populated by the collector. Closes the SNC §8.1.a
  "model" requirement for on-prem deployments. Editable at
  `/ui/nodes/:id`. `UpsertNode`'s `DO UPDATE SET` clause is explicit
  about which columns the collector owns; the new columns are absent
  from it by design.
- **ADR-0008** ([#55](https://github.com/sthalbert/Argos/pull/55)) —
  SecNumCloud v3.2 chapter 8 coverage. Maps every §8.1 sub-clause to a
  concrete Argos column or explicit cross-reference (licenses →
  Dependency-Track via `containers[].image`; §8.2 and §8.5 are
  procedural / out of system scope). DICT
  (disponibilité / intégrité / confidentialité / traçabilité) classification
  will land on Namespace + Workload in a later release at the Application
  abstraction.

### Fixed

- **Detail pages resolve parent names instead of UUIDs**
  ([#58](https://github.com/sthalbert/Argos/pull/58)) —
  `/ui/namespaces/:id` previously had no Cluster row at all;
  `/ui/nodes/:id` showed the cluster id as a truncated UUID;
  `/ui/workloads/:id` did the same for namespace. Each page now
  resolves the parent and renders a `<Link>` with its name. The
  Workload breadcrumb also gains cluster + namespace hops so the
  drill-down trail reads *"Workloads / <cluster> / <namespace> / this
  workload"* instead of dead-ending.
- **Namespace pods table shows workload name**
  ([#59](https://github.com/sthalbert/Argos/pull/59)) — the Workload
  column in `/ui/namespaces/:id`'s pods table rendered each pod's
  `workload_id` as a UUID link. Now renders the workload's name and
  kind (`web-frontend · Deployment`) by resolving against the
  in-scope workloads fetch — no extra network call.

### Schema migrations

- `00019_namespace_curated_metadata.sql` — adds 5 columns on
  `namespaces`.
- `00020_node_curated_metadata.sql` — adds 6 columns (5 curated +
  `hardware_model`) on `nodes`.

Both are additive; existing rows get NULL for the new columns and the
JSONB defaults to `{}`. No data rewrite, no downtime.

### Upgrading

```bash
# From v0.1.0. Keep your existing ARGOS_BOOTSTRAP_ADMIN_PASSWORD — the
# bootstrap only fires when no admin exists, so it's a no-op here.
make build VERSION=0.1.1
# Point at the same DSN as v0.1.0; ARGOS_AUTO_MIGRATE=true (default)
# applies 00019 + 00020 on startup.
./bin/argosd
```

No client-side break: new columns show up as `null` on existing rows
and the UI renders an "Edit" placeholder until an editor fills them in.

## [0.1.0] — 2026-04-19 — "Canopus"

First tagged release. Argos is a Kubernetes-aware CMDB aligned with the
ANSSI **SecNumCloud (SNC)** qualification framework. Named after the
principal star of the old *Argo Navis* constellation — a classical
navigation marker.

### Highlights

- Multi-cluster polling collector mirrors a full Kubernetes inventory
  (nodes, namespaces, pods, workloads, services, ingresses, PVs, PVCs)
  into PostgreSQL and reconciles rows that disappear from the live
  listing.
- REST API is OpenAPI 3.1 contract-first with RFC 7807 errors, cursor
  pagination, and merge-patch updates.
- Dual-path authentication: humans log in with local password **or**
  OIDC (authorization-code flow with PKCE + nonce + state); machines
  carry admin-minted bearer tokens (argon2id-hashed, prefix-indexed).
- Four fixed roles — `admin` / `editor` / `auditor` / `viewer` — wired
  through the existing scope checks.
- React/TypeScript SPA embedded in the binary; admin panel, audit log
  viewer, component search, and inline cluster-metadata editor.
- Append-only audit log captures every write + every admin-panel read.
- Prometheus `/metrics` exposes per-cluster collector + HTTP counters.

### Architecture — ADRs

- **ADR-0001** — CMDB for SNC using the Kubernetes API as source of truth.
- **ADR-0002** — Mapping Kubernetes kinds onto the ANSSI cartography layers.
- **ADR-0003** — Workload polymorphism (Deployment / StatefulSet / DaemonSet
  on one table, discriminated by `kind`).
- **ADR-0004** — Ingress layer classification.
- **ADR-0005** — Multi-cluster collector topology
  (`ARGOS_COLLECTOR_CLUSTERS`).
- **ADR-0006** — Web UI bundled into argosd; curated-metadata columns.
- **ADR-0007** — Auth & RBAC (sessions + OIDC + bearer tokens).

### API & data model

- Nine resource kinds: **Cluster**, **Namespace**, **Node**, **Pod**,
  **Workload** (poly over Deployment / StatefulSet / DaemonSet),
  **Service**, **Ingress**, **PersistentVolume**,
  **PersistentVolumeClaim**. All carry a `layer` field derived from
  ADR-0002.
- FK chain `clusters → namespaces/nodes/persistent_volumes → pods /
  workloads / services / ingresses / persistent_volume_claims`, all
  `ON DELETE CASCADE`. Pods also carry a nullable `workload_id` FK to
  their top-level controller (`ON DELETE SET NULL`); PVCs carry a
  nullable `bound_volume_id` FK (same semantics).
- Pods and Workloads include a `containers` JSONB column for SBOM /
  CVE workflows. Nodes carry an enriched field set (role, cloud
  identity, OS stack, capacity + allocatable, conditions, taints).
  Services and Ingresses carry a `load_balancer` JSONB column so
  on-prem VIPs (MetalLB, Kube-VIP, hardware LBs) surface alongside
  cloud-provisioned ones.
- Filter endpoints for incident response:
  - `GET /v1/workloads?image=…` and `GET /v1/pods?image=…` —
    case-insensitive substring match over every container image.
  - `GET /v1/pods?node_name=…` — powers the "if this node dies, which
    pods are lost?" view.
- Merge-patch `PATCH /v1/clusters/{id}` supports **display_name,
  environment, provider, region, api_endpoint, labels, owner,
  criticality, notes, runbook_url, annotations**. Collector writes
  only `kubernetes_version`, so operator annotations are preserved
  across polls.

### Collector

- Polling-based; each tick refreshes the API-server version and lists
  every catalogued kind cluster-wide. Default 5 minute interval,
  configurable.
- Reconciliation (`ARGOS_COLLECTOR_RECONCILE=true`, default on) deletes
  rows that disappear from the live listing so the CMDB mirrors
  ground truth — required for ANSSI cartography fidelity. Runs only
  after a successful list so a transient Kubernetes error never wipes
  the store.
- Multi-cluster via `ARGOS_COLLECTOR_CLUSTERS` (JSON array of
  `{name, kubeconfig}` tuples); legacy single-cluster env vars still
  work. Empty kubeconfig falls back to in-cluster config.
- Exposes Prometheus counters and last-poll gauges per
  `(cluster, resource)`.

### Auth (ADR-0007)

- **Local login**: `POST /v1/auth/login` → server-side session cookie
  (HttpOnly, SameSite=Strict, 8h sliding).
- **OIDC**: `GET /v1/auth/oidc/authorize` → IdP →
  `GET /v1/auth/oidc/callback`; authorization-code flow with PKCE
  (S256) + nonce + state. Shadow users keyed on `(issuer, sub)`;
  first-login role is `viewer` — admins promote manually (authorization
  is never claim-driven).
- **Machine tokens**: `Authorization: Bearer argos_pat_<prefix>_<suffix>`;
  argon2id-hashed at rest, 8-char prefix for O(1) lookup, plaintext
  shown once at creation (GitHub-PAT pattern). Minted in the admin UI.
- **First-run bootstrap**: creates a single `admin` user when none
  exists; password comes from `ARGOS_BOOTSTRAP_ADMIN_PASSWORD` or a
  random 16-char string printed once to the startup log. Forced
  rotation on first login.
- Role → scope mapping: `admin` carries everything, `editor` =
  `read + write`, `auditor` = `read + audit`, `viewer` = `read`.

### Audit log

- `audit_events` table is append-only; the audit middleware observes
  every state-changing call plus every `/v1/admin/*` read and records
  actor + verb + target + status + source IP.
- Password / token / OIDC client-secret fields are scrubbed before
  the row is written.
- `GET /v1/admin/audit` with filters for actor, resource, action, and
  time window. `audit` scope (admin or auditor).

### Web UI (ADR-0006)

- Embedded React/TypeScript SPA at `/ui/` (build-tag `noui` disables
  the embed for backend-only builds).
- List and detail pages for every resource kind; **Cluster →
  Namespace → Workload → Pod** and **Cluster → Node** drill-downs.
- Node detail renders the full enriched picture: identity, OS &
  runtime, networking, resources (capacity vs allocatable), conditions
  with per-row health colouring, taints, labels — plus an
  impact-analysis callout of affected pods grouped by workload.
- Ingress detail surfaces the load-balancer block first, then rules
  and TLS.
- Component search at `/ui/search/image` with URL-persisted query.
- Admin panel at `/ui/admin/`: Users / Machine tokens / Active
  sessions / Audit. Auditors see only the Audit tab; admins see all.
- Inline "Ownership & context" editor on `/ui/clusters/:id` for
  editors and admins — edits environment, provider, region, labels,
  owner, criticality, runbook URL, notes, and annotations without the
  collector clobbering them.

### Operations

- Dockerfile: three-stage build (Node UI builder → Go builder →
  distroless runtime), static binary, runs as UID 65532.
- `deploy/` ships reference Kustomize manifests for running argosd in
  a Kubernetes cluster cataloguing itself via in-cluster
  ServiceAccount (`list` on every catalogued kind). Multi-cluster
  variant documented in `deploy/README.md`.
- `/metrics` mounted unauthenticated on the main mux (Prometheus
  scrape convention): HTTP request / duration counters, collector
  upsert / reconcile / error counters, per-`(cluster, resource)`
  last-poll gauges, `argos_build_info`.
- CI (GitHub Actions): conventional-commit title check, `go vet` /
  `go build` / `go test -race` against a Postgres service container,
  `golangci-lint`, UI `npm run build` + typecheck, Docker image
  build verification.

### Known limitations

- REST + DB contracts may change incompatibly before `v1.0.0`. Pin to
  this tag for stability.
- Only the **Cluster** kind carries curated-metadata columns in
  `v0.1.0`; the same pattern will land on namespaces / nodes /
  workloads in a follow-up.
- No snapshots / time-travel yet (longer-horizon roadmap item from
  ADR-0001).
- No built-in MFA on the local password path; customers who need it
  should federate through their OIDC provider (which already gives
  every argosd instance MFA as a side-effect).

[0.1.1]: https://github.com/sthalbert/Argos/releases/tag/v0.1.1
[0.1.0]: https://github.com/sthalbert/Argos/releases/tag/v0.1.0
