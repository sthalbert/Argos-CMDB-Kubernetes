# Changelog

All notable changes to Argos are recorded here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning
follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
— the REST and database contracts may still change incompatibly before
`v1.0.0`.

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
