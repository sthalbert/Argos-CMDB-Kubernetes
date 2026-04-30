# Changelog

All notable changes to Argos are recorded here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
— the REST and database contracts may still change incompatibly before
`v1.0.0`.

## [0.12.2] — 2026-04-30

Helm charts realigned on `appVersion 0.12.2` across the family
(`argos` 0.15.2, `argos-collector` 0.1.2, `argos-ingest-gw` 0.1.3,
`argos-vm-collector` 0.1.2). Two bug fixes against 0.12.1: the DMZ ingest
gateway can actually verify tokens against argosd (a spec-level oversight
made the verify endpoint reject every call from the gateway with 401),
and EOL enrichment now matches major-only product cycles.

### Fixed

- **DMZ ingest gateway → argosd verify call always returned `401 missing
  or invalid credentials`, blocking every forwarded write.** `POST
  /v1/auth/verify` (ADR-0016 §5) is authenticated by the mTLS-only
  listener handshake, not by an `Authorization` header — the gateway
  sends the token to verify in the request body and never presents a
  bearer credential of its own. The OpenAPI spec did not declare
  `security: []` on the operation, so it inherited the document-wide
  `BearerAuth + SessionCookie` requirement; the codegen wrapper then set
  a non-nil empty `BearerAuthScopes` on the request context, and
  `auth.Middleware` interpreted that as "auth required" and 401-rejected
  every call before `VerifyToken` ran. The fix adds `security: []` to
  the operation (matching the existing pattern on `/healthz`,
  `/v1/auth/login`, and `/v1/auth/oidc/authorize`); `internal/api/` was
  regenerated; a new regression test
  `TestVerifyToken_PublicEndpoint_NoAuthHeader_Returns200` exercises the
  real `auth.Middleware` and locks the contract. The listener-level
  `RequireAndVerifyClientCert` is unchanged — mTLS remains the sole
  authentication mechanism for the call.
- **EOL enrichment fell through to `eol_status=unknown` for products
  whose endoflife.date cycle key is a single major version
  (e.g. `postgresql` `15`).** `extractMajorMinor` required at least two
  numeric components, so a VM application declared as `postgresql`
  version `15` never matched cycle `15` even though endoflife.date
  exposes it. Replaced with `extractCycleCandidates` returning
  `[major.minor, major]` in priority order; the resolver now retries
  with the major-only candidate before stubbing the annotation.

## [0.12.1] — 2026-04-30

Helm charts realigned on `appVersion 0.12.1` across the family
(`argos` 0.15.1, `argos-collector` 0.1.1, `argos-ingest-gw` 0.1.2,
`argos-vm-collector` 0.1.1). UI hotfix for the VM applications editor
introduced in 0.12.0; the collector binaries are unchanged but their
charts bump in lockstep so `helm list` shows a single coherent
appVersion across an Argos deployment.

### Fixed

- **`PATCH /v1/virtual-machines/{id}` returned `400 invalid JSON body` when
  adding a new application row from the UI.** The `ApplicationsCard` editor
  was sending `added_at: ""` and `added_by: ""` for fresh rows; the server
  decodes `added_at` into `time.Time`, which cannot parse the empty string,
  so the entire request was rejected before reaching the diff logic that
  would have stamped the missing values. The form now omits those fields
  entirely on new rows (and only re-sends them for rows that already carry
  server-stamped values), letting the server take its existing
  preserve-or-stamp path. No schema or API change — only the UI payload
  shape is fixed.

## [0.12.0] — 2026-04-29

Helm chart 0.15.0 / appVersion 0.12.0. Adds VM application inventory and EOL
enrichment for platform software (ADR-0019): operators can now record what runs
on each non-Kubernetes VM, the EOL enricher evaluates those declared versions
against endoflife.date, and the VM list grows six new server-side filters plus
a distinct-applications endpoint for autocomplete.

### Added

- **`virtual_machines.applications` JSONB column** (ADR-0019 §1, migration
  `00028_add_vm_applications.sql`) — operator-curated list of platform software
  entries per VM (`product`, `version`, `name`, `notes`, `added_at`,
  `added_by`). `product` is normalized server-side (trimmed, lower-cased,
  whitespace collapsed to hyphens) so `"Hashicorp Vault"` and `"vault"`
  deduplicate to the same key. The column is backed by a GIN
  `jsonb_path_ops` index for O(log n) `@>` containment queries; a functional
  index on `LOWER(name)` and a btree index on `image_id` are also added.
  `UpsertVirtualMachine` (the collector path) never touches `applications`;
  only `PATCH /v1/virtual-machines/{id}` does.
- **EOL enrichment for VM applications** (ADR-0019 §2) — the `internal/eol/`
  enricher gains a third pass, `enrichVirtualMachines`, that walks every
  non-terminated VM's `applications` list and writes `argos.io/eol.<product>`
  annotations using the same endoflife.date lookup used for clusters and nodes.
  Products not on endoflife.date receive a stub annotation with
  `eol_status=unknown` so operators see the row was evaluated rather than
  silently dropped. Stale EOL annotations from removed applications are reaped
  automatically on the next enrichment tick.
- **Six new server-side filters on `GET /v1/virtual-machines`** (ADR-0019 §3)
  — `name` (case-insensitive substring on `name` / `display_name`), `image`
  (case-insensitive substring on `image_id` / `image_name`),
  `cloud_account_name` (resolves to UUID via an inner subquery on the UNIQUE
  index), `application` (JSONB containment on `applications[].product`,
  normalized server-side), `application_version` (narrows `application` to a
  specific version; ignored when `application` is absent), and `region` /
  `role` remain exact-match. All six AND with the existing filters and respect
  the `vm-collector` PAT account-binding restriction. LIKE metacharacters in
  `name` and `image` values are escaped before interpolation.
- **`GET /v1/virtual-machines/applications/distinct`** (ADR-0019 §3) —
  returns `{products: [{product, versions}]}` with up to 200 distinct
  normalized product names and, for each, the sorted list of distinct versions
  seen across non-terminated VMs. Requires `read` scope. Drives the cascading
  product → version dropdown in the VM list UI.
- **`applications` field on `PATCH /v1/virtual-machines/{id}`** (ADR-0019 §4)
  — accepts a `*[]VMApplication` with replace-not-merge semantics: the
  submitted list replaces the stored list in full. The handler diffs input
  against the existing list to preserve `added_at` / `added_by` for unchanged
  `(product, version, name)` tuples and stamps fresh values on new entries.
  Maximum 100 entries; per-field length caps enforced (product 64, version 64,
  name 200, notes 4096 characters).
- **VM Applications card on `/ui/virtual-machines/:id`** — read mode shows a
  table (product, version, name, notes, EOL status badge, latest available,
  added by, added at); edit mode flips to a per-row editor with add/remove
  buttons submitting the full list. Editor and admin see the Edit button;
  viewer and auditor see read-only.
- **Cascading filter on `/ui/virtual-machines`** — Application dropdown
  populates from `GET /v1/virtual-machines/applications/distinct`; selecting a
  product immediately narrows the App-version dropdown to the versions in
  inventory for that product.
- **Search and Clear buttons on the VM list filter bar** — replaces the
  previous debounced auto-apply. Filters now require explicit submission;
  Clear resets all inputs at once.

### Changed

- **`/ui/search/image` renamed "Search by image or application"** — the page
  now also surfaces platform VMs by image substring (`image_id` / `image_name`)
  and by exact normalized product (via `?application=<product>` on
  `GET /v1/virtual-machines`). The two result sets (K8s workloads/pods and
  platform VMs) are unioned by entity id and displayed in separate sections.
  The URL slug and browser title are updated; existing bookmarks using the old
  slug continue to work via a redirect.
- **EOL dashboard at `/ui/eol` now includes VMs as an entity dimension** —
  the aggregator reads `argos.io/eol.*` annotations from `virtual_machines`
  alongside clusters and nodes. A new "Type" column (cluster / node / vm) and
  a corresponding filter chip appear in the summary card row. Row-level
  red/orange highlighting and the two-column-group layout ("What we run" /
  "What's available") are unchanged.
- **Filter layout on `/ui/virtual-machines` regrouped** — filters block sits
  above the VM search block, separated by a `border-top` divider.

## [0.11.2] — 2026-04-29

Charts-only release. `appVersion` stays at `0.11.1` — no binary changed —
but every Argos deployable now ships with a first-class Helm chart per
ADR-0018. Two new charts join the family: `charts/argos-collector` (the
push-mode Kubernetes collector for air-gapped clusters) and
`charts/argos-vm-collector` (the cloud-VM collector). The reference
Kustomize manifests under `deploy/` are demoted to "examples / first
contact" — Helm is now the supported deployment path for every binary.

### Added

- **`charts/argos-collector`** — independent chart for the push-mode K8s
  collector (ADR-0009). One Helm release per source cluster. Surfaces
  `serverURL`, `clusterName`, operator-supplied `tokenSecret.existingSecret`,
  `kubeconfig.{mode=in-cluster|secret}`, polling cadence, mTLS-to-DMZ-gateway
  block, outbound proxy block, opt-in NetworkPolicy + PodDisruptionBudget,
  and the standard hardening defaults (UID 65532, `runAsNonRoot:true`,
  `readOnlyRootFilesystem`, drop ALL capabilities, seccomp `RuntimeDefault`).
  ClusterRole is genuinely minimal: `list` only, on the eleven resource
  types the collector polls.
- **`charts/argos-vm-collector`** — independent chart for the cloud-VM
  collector (ADR-0015). One Helm release per cloud account. Surfaces the
  same operator-supplied PAT pattern, `account.{provider, name, region}`,
  the credential-refresh cadence, mTLS + proxy blocks, optional Service
  + ServiceMonitor for Prometheus scraping of `:9090/metrics`, and opt-in
  NetworkPolicy + PodDisruptionBudget. Creates no ClusterRole — the
  vm-collector never calls the Kubernetes API.
- **`docs/adr/adr-0018-helm-chart-per-deployable-binary.md`** — records
  the chart-per-binary policy: every deployable Argos binary ships with a
  Helm chart of its own, sibling to (not subchart of) `charts/argos`.
  Independent chart versions, shared layout / labelling / hardening
  conventions copied from `charts/argos-ingest-gw`.

### Security

- **`automountServiceAccountToken` is gated** in both new charts. The
  `argos-vm-collector` pod hardcodes it to `false` (no K8s API access
  needed); `argos-collector` ties it to `kubeconfig.mode == in-cluster`
  so the `kubeconfig.mode=secret` path doesn't gratuitously expose the
  projected SA token.
- **NetworkPolicy egress is scoped** when `networkPolicy.egressCIDRs` is
  set. Previously the unrestricted "any 443" rule sat alongside the
  CIDR-list rule, defeating the lockdown; now the 443 rule is suppressed
  when CIDRs are supplied and the egress is restricted to the listed
  ranges only.

### Migration

No DB migration. Operationally:

- Existing `deploy/collector/` and `deploy/vm-collector/` Kustomize
  manifests still work — they're now positioned as quick-start examples,
  not the supported production path. The Helm charts replace them as the
  recommended deployment surface and make air-gap, mTLS-to-DMZ-gateway,
  and one-release-per-cluster topologies first-class.
- Operators on Kustomize can migrate at their own pace: `helm template`
  the new chart, diff against the existing manifest set, and adopt
  release by release.

## [0.11.1] — 2026-04-29

Helm chart 0.14.0 / appVersion 0.11.1. Hardening release driven by the
2026-04-28 penetration test. Closes three P0 findings — plaintext-HTTP
credential transit (AUTH-VULN-01/02/03), forgeable `X-Forwarded-For`
rate-limit bypass (AUTH-VULN-04), and admin-account orphaning via the
admin-user lifecycle endpoints (AUTHZ-VULN-01/02). New ADR-0017 documents
the public-listener TLS posture and proxy-trust contract introduced here.

### Security

- **Native TLS termination on the public listener** (ADR-0017 §4) —
  argosd can now serve HTTPS directly when `LONGUE_VUE_PUBLIC_LISTEN_TLS_CERT`
  and `LONGUE_VUE_PUBLIC_LISTEN_TLS_KEY` are set. Cert + key are loaded at
  startup, hot-reloaded via fsnotify on file change (works with cert-manager,
  Vault Agent atomic-rename, manual file writes), and pinned to TLS 1.3 with
  session tickets disabled. Refuses to start on parse error rather than
  falling through to plain HTTP.
- **Trust-aware Secure cookie + HSTS + client IP resolution** (ADR-0017 §5)
  — `X-Forwarded-For` and `X-Forwarded-Proto` are honored only when the
  immediate TCP peer's IP falls inside `LONGUE_VUE_TRUSTED_PROXIES` (a
  comma-separated CIDR list). Empty list = ignore both headers entirely
  (the secure default). Fixes AUTH-VULN-04: a remote client sending
  `X-Forwarded-For: <victim-ip>` could previously bypass per-IP rate
  limits on `/v1/auth/login`. Fixes AUTH-VULN-02: the session cookie's
  `Secure` flag now reflects the resolved transport, not a forgeable
  XFP header. Fixes AUTH-VULN-03: HSTS is emitted only over a verified
  HTTPS request, with a force-emit override for operators declaring the
  full deployment HTTPS-only.
- **Startup posture guard** (ADR-0017 §7) — `LONGUE_VUE_REQUIRE_HTTPS=true`
  refuses to boot unless either native TLS is configured (cert + key
  present) or both `LONGUE_VUE_TRUSTED_PROXIES` is non-empty AND
  `LONGUE_VUE_SESSION_SECURE_COOKIE=always`. Fails closed: "warn and serve
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
- **Public-listener cert hot-reload** — `cmd/argosd/main.go:newCertReloader`
  reloads the public listener's cert + key on every TLS handshake when the
  on-disk file mtime advances (compatible with cert-manager rotations,
  Vault Agent atomic renames, and manual file writes). Sibling to the
  fsnotify-driven `internal/ingestgw/tls_reload.go` used by the DMZ
  gateway; the two listeners use mechanism-appropriate reload paths
  rather than a shared package.
- **Helm chart `argosd.tls` block** — `existingSecret` references a
  `kubernetes.io/tls` Secret; the chart mounts it at
  `/etc/argos/tls` and wires `LONGUE_VUE_PUBLIC_LISTEN_TLS_CERT/_KEY`
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
   `LONGUE_VUE_TRUSTED_PROXIES` to the proxy's CIDR(s) and pin
   `LONGUE_VUE_SESSION_SECURE_COOKIE=always`. Without trust, the upgrade
   silently downgrades cookie security and rate-limit accuracy — the
   defaults are the safest possible state, not the most useful.

2. **If you want HSTS / require HTTPS**, set `LONGUE_VUE_REQUIRE_HTTPS=true`.
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
  `*http.Server` starts when `LONGUE_VUE_INGEST_LISTEN_ADDR` is set (disabled
  when empty; existing deployments unaffected). The listener requires
  `RequireAndVerifyClientCert` (TLS 1.3 floor, session tickets disabled)
  and is wired by `api.NewIngestMux`, which registers exactly 19 routes:
  the 18 collector writes plus `POST /v1/auth/verify`. New env vars:
  `LONGUE_VUE_INGEST_LISTEN_ADDR`, `LONGUE_VUE_INGEST_LISTEN_TLS_CERT`,
  `LONGUE_VUE_INGEST_LISTEN_TLS_KEY`, `LONGUE_VUE_INGEST_LISTEN_CLIENT_CA_FILE`,
  `LONGUE_VUE_INGEST_LISTEN_CLIENT_CN_ALLOW`.
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
to `audit_events`. The `LONGUE_VUE_AUTO_MIGRATE=true` default applies it on
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
  `LONGUE_VUE_SECRETS_MASTER_KEY` (base64-encoded 32 bytes; rejected at
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
  up the new SK within `LONGUE_VUE_VM_COLLECTOR_CREDENTIAL_REFRESH`
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
`LONGUE_VUE_AUTO_MIGRATE=true` default applies them on startup. Existing
deployments without any `cloud_accounts` row do not need
`LONGUE_VUE_SECRETS_MASTER_KEY`; argosd only refuses to start when the
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
→ `v0.1.1` is a straight `LONGUE_VUE_AUTO_MIGRATE=true` bump, no data
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
# From v0.1.0. Keep your existing LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD — the
# bootstrap only fires when no admin exists, so it's a no-op here.
make build VERSION=0.1.1
# Point at the same DSN as v0.1.0; LONGUE_VUE_AUTO_MIGRATE=true (default)
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
  (`LONGUE_VUE_COLLECTOR_CLUSTERS`).
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
- Reconciliation (`LONGUE_VUE_COLLECTOR_RECONCILE=true`, default on) deletes
  rows that disappear from the live listing so the CMDB mirrors
  ground truth — required for ANSSI cartography fidelity. Runs only
  after a successful list so a transient Kubernetes error never wipes
  the store.
- Multi-cluster via `LONGUE_VUE_COLLECTOR_CLUSTERS` (JSON array of
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
  exists; password comes from `LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD` or a
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
