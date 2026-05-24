---
title: "ADR-0029: First-class Application entity for SecNumCloud applicative-layer inventory"
status: "Proposed"
date: "2026-05-24"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "datamodel", "secnumcloud", "anssi", "cmdb", "application"]
supersedes: ""
superseded_by: ""
---

# ADR-0029: First-class Application entity for SecNumCloud applicative-layer inventory

## Status

Proposed | Accepted | Rejected | Superseded | Deprecated

- **Date:** 2026-05-24
- **Supersedes:** none (refines ADR-0008 §8.3 DICT placement; does not remove the
  existing Namespace/Workload DICT columns)
- **Superseded by:** none

## Context

ANSSI cartography (ADR-0002) lays out six layers; one of them is **applicative**
— "applications, services, software assets". The SNC v3.2 référentiel
chapter 8 (asset management — ADR-0008) and the broader EBIOS-RM convention
both treat the *Application* as the unit at which **security needs (DICT —
Disponibilité / Intégrité / Confidentialité / Traçabilité)** are declared. An
auditor walking the SNC §8.1.a + §8.3 evidence package expects to point at an
"application" row and read its owner, criticality, security classification,
and the list of technical assets that implement it.

longue-vue today does not have an Application entity. ADR-0008 sidestepped the
gap by putting DICT and curated metadata on **Namespace** and **Workload**, on
the rationale that those were the two abstractions closest to "application"
already present in the data model. That worked as a v1 — it closed the
schema-level part of §8.3 — but it left three structural problems unresolved:

1. **An application that spans multiple workloads has no canonical row.**
   "Vault" in a typical SNC deployment is one workload in `kube-prod`, another
   in `kube-staging`, and a daemon on a bastion VM. ADR-0008's model forces
   the operator to classify each of the three identically. The drift
   probability is high; the audit story ("show me Vault's confidentiality
   rating") requires three lookups and a reconciliation step.

2. **An application that crosses Kubernetes and VMs has no shared row at
   all.** ADR-0019 added a `virtual_machines.applications` JSONB to record
   that a VM runs Vault 1.15.4, so the EOL enricher can score it. But that
   JSONB entry shares nothing with the same product running in a Kubernetes
   workload — no owner, no DICT, no runbook. The cross-substrate "this is one
   application" view is missing.

3. **ApplicationBlock — SNC's second-level grouping — has no representation
   at all.** The user prompt for this ADR quoted SNC directly:
   *"An application block represents a set of applications. An application
   block can be: office applications, management applications, analysis
   applications, development applications, etc."* The two-level inventory
   (Block → Application → assets) is a recognised SNC framing that longue-vue
   cannot currently express.

The user directive: **operators must be able to declare Applications as
first-class entities, link Workloads and VMs (and the per-VM application
entries from ADR-0019) to them, group those Applications into
ApplicationBlocks, classify security needs at the Application level, and have
the EOL signal aggregate at that level.**

This ADR resolves the data model, the API surface, the DICT-precedence rules
(because we elected not to remove the existing Workload/Namespace DICT
columns), the MCP exposure, the UI surface, and the operator workflow. It
does **not** introduce auto-derivation of Applications from labels or naming
conventions — that is a known follow-up the operator-curated invariant
deliberately defers.

## Decision

**Add two new top-level entities, `application_blocks` and `applications`,
both operator-curated; link each Workload and each Virtual Machine to at most
one Application via a nullable FK; extend the existing
`virtual_machines.applications` JSONB entries (ADR-0019) with an optional
per-entry `application_id` so a multi-product VM can route its rows to
different Applications; add DICT (per ADR-0008's EBIOS-RM convention) to the
Application row; expose the Application via REST, MCP, the SPA, an aggregated
EOL view, and a classification heat-map admin page; preserve the existing
Workload/Namespace DICT columns as the fallback source until coverage
telemetry justifies their removal in a follow-up ADR.**

### 1. Data model

Three migrations, ordered so a half-applied state still compiles. No
backfill — there is no existing Application data to migrate, and the
Workload/Namespace DICT columns are preserved unchanged.

```sql
-- 00045_create_application_blocks.sql
CREATE TABLE application_blocks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL UNIQUE,
    display_name TEXT,
    description  TEXT,
    owner        TEXT,
    notes        TEXT,
    annotations  JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 00046_create_applications.sql
CREATE TABLE applications (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 TEXT NOT NULL UNIQUE,
    display_name         TEXT,
    description          TEXT,
    application_block_id UUID REFERENCES application_blocks(id) ON DELETE SET NULL,

    -- Curated metadata (mirrors cluster / namespace / workload pattern)
    owner        TEXT,
    criticality  TEXT,
    notes        TEXT,
    runbook_url  TEXT,
    annotations  JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- DICT per ADR-0008 EBIOS-RM convention, now on Application
    sec_disponibilite    SMALLINT CHECK (sec_disponibilite    BETWEEN 0 AND 4),
    sec_integrite        SMALLINT CHECK (sec_integrite        BETWEEN 0 AND 4),
    sec_confidentialite  SMALLINT CHECK (sec_confidentialite  BETWEEN 0 AND 4),
    sec_tracabilite      SMALLINT CHECK (sec_tracabilite      BETWEEN 0 AND 4),
    sec_notes            TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX applications_block_idx       ON applications(application_block_id);
CREATE INDEX applications_name_lower_idx  ON applications(LOWER(name));

-- 00047_add_application_id_to_workloads_and_vms.sql
ALTER TABLE workloads
    ADD COLUMN application_id UUID REFERENCES applications(id) ON DELETE SET NULL;
CREATE INDEX workloads_application_id_idx ON workloads(application_id);

ALTER TABLE virtual_machines
    ADD COLUMN application_id UUID REFERENCES applications(id) ON DELETE SET NULL;
CREATE INDEX virtual_machines_application_id_idx ON virtual_machines(application_id);
```

**Naming.** The `name` column is normalised at write time: trim → lowercase →
collapse internal whitespace to single hyphens. Same rule already in place
for `virtual_machines.applications[].product` (ADR-0019). `display_name` is
free-form for the UI. The lower-cased functional index serves substring
search on the list page.

**VM-application JSONB extension.** The existing `VMApplication` shape from
ADR-0019 gains one optional field:

```json
{
  "product": "vault",
  "version": "1.15.4",
  "name": "vault-prod-eu",
  "application_id": "b9c1...-uuid",   // NEW, optional
  "added_at": "...",
  "added_by": "..."
}
```

Server validation on `PATCH /v1/virtual-machines/{id}` rejects an
`application_id` that does not reference an existing row. The product/name
fields are unchanged — the link is purely additive and does not replace the
declarative "what product runs here" information. A VM hosting Vault and BIND
can route the Vault row to the Vault application and the BIND row to the
BIND application independently.

**`ON DELETE SET NULL` everywhere.** Deleting an Application unlinks its
members but does not destroy them. Same soft-pointer convention as
`pods.workload_id` and `pvcs.bound_volume_id` (CLAUDE.md "FK chain"
section).

**Why no junction table for membership.** The membership cardinality is 1:N
(an asset belongs to at most one Application — explicit design choice).
Three FK columns are smaller, join-free for the common read, and don't
require a separate audit trail for membership changes (audit is already
captured by the PATCH on the child row). If many-to-many ever becomes a real
need (shared-sidecar accounting), an additive `application_members` junction
is a backward-compatible extension; the existing FKs become "primary
application" hints.

### 2. API surface

The REST contract follows the established longue-vue patterns: cursor
pagination, RFC 7807 errors, merge-patch on PATCH, idempotent POST on name,
admin scope for DELETE.

#### 2.1 Application CRUD

| Verb | Path | Scope | Notes |
|---|---|---|---|
| `GET` | `/v1/applications` | read | Filters: `name` (case-insensitive substring), `application_block_id`, `application_block_name` (resolved server-side), `criticality` (exact), `has_dict` (bool — any axis set), `dict_min` (smallint — MAX axis ≥ N), `cursor`, `limit` (1..200). Returns `member_counts` per row. |
| `POST` | `/v1/applications` | write | Idempotent on `name` (200 on hit, 201 on insert). Body accepts `application_block_id` OR `application_block_name` (id wins on conflict). |
| `GET` | `/v1/applications/{id}` | read | Includes `member_counts: {workloads, virtual_machines, vm_applications}` and `block` (denormalised name per ADR-0027). |
| `GET` | `/v1/applications/by-name/{name}` | read | Convenience lookup. |
| `GET` | `/v1/applications/{id}/members` | read | Paginated union: `[{kind: "workload"\|"virtual_machine"\|"vm_application", id, name, parent: {kind, name}, summary_fields}…]`. Sort: `(kind, name)`. |
| `PATCH` | `/v1/applications/{id}` | write | Merge-patch on curated + DICT + block. |
| `DELETE` | `/v1/applications/{id}` | admin | FK cascade sets `application_id = NULL` on children + on JSONB rows (the cascade for JSONB is enforced in the store layer because PostgreSQL FKs do not reach into JSONB). |
| `GET` | `/v1/applications/{id}/eol` | read | Aggregated EOL annotations across members (§5). |
| `GET` | `/v1/applications/extract.{csv,json}` | read | Bulk export per the ADR-0019 pattern. Capped by `LONGUE_VUE_EXTRACT_MAX_ROWS`. Audit-logged via `shouldAudit` allowlist. Supports `dict_min=` to filter for the SNC evidence package. |

#### 2.2 ApplicationBlock CRUD

Standard shape: `GET` list (cursor-paginated, filters: `name`, `owner`),
`POST` (idempotent on name), `GET /{id}`, `PATCH /{id}`, `DELETE /{id}`
(admin scope; sets `application_block_id = NULL` on member applications).
The block detail returns `application_count`.

#### 2.3 Linking — extensions to existing PATCH endpoints

`PATCH /v1/workloads/{id}` and `PATCH /v1/virtual-machines/{id}` both accept:

- `application_id` — UUID, nullable (set to `null` to unlink).
- `application_name` — convenience; resolved server-side. If both are set,
  `application_id` wins (mirrors the `cloud_account_id` / `cloud_account_name`
  precedence from ADR-0019).

`PATCH /v1/virtual-machines/{id}`'s existing `applications: []VMApplication`
array gains `application_id` per entry (validated against the
`applications` table).

#### 2.4 New filters on existing list endpoints

| Endpoint | New filter | Semantics |
|---|---|---|
| `GET /v1/workloads` | `application_id`, `application_name`, `unlinked=true` | Exact match; AND with existing filters. |
| `GET /v1/virtual-machines` | `application_id`, `application_name`, `unlinked=true` | Symmetric. |
| `GET /v1/search` | `application` | Substring against the linked application's name; folds into the existing extract path. |

#### 2.5 RBAC matrix

| Role | Apps GET | Apps PATCH | Apps POST | Apps DELETE | Blocks DELETE | Link/unlink |
|---|---|---|---|---|---|---|
| admin | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| editor | ✓ | ✓ | ✓ | ✗ | ✗ | ✓ |
| viewer / auditor | ✓ | ✗ | ✗ | ✗ | ✗ | ✗ |
| vm-collector | (own account scope only on reads) | ✗ | ✗ | ✗ | ✗ | ✗ |

#### 2.6 OpenAPI

`api/openapi/openapi.yaml` is the source of truth. New schemas
(`Application`, `ApplicationPatch`, `ApplicationBlock`, `ApplicationBlockPatch`,
`ApplicationMember`, `ApplicationEOLSummary`) and new endpoints land there.
`WorkloadPatch`, `VirtualMachinePatch`, and `VMApplication` are extended.
`make swagger-sync-check` pins the embedded copy. New
`openapi_validation_test.go` cases cover request/response payloads with
`pb33f/libopenapi-validator`.

### 3. UI surface

Two new top-level routes, one admin route, plus inline pickers on existing
detail pages. Follows established conventions (column-filtered list pages
mirroring `VirtualMachines.tsx`; stacked-card detail pages mirroring
`VirtualMachineDetail.tsx`; role-gated edit modes via the existing
curated-metadata card family).

**`/ui/applications` — list page.** Toolbar: `name`, `application_block`
(autocompleted from block list), `criticality` dropdown, `has_dict` toggle.
Default group-by is **ApplicationBlock** (collapsible sections; "Unblocked"
last). Row columns: name, block chip, owner, criticality, DICT badge
(max axis), member counts (`5 workloads · 2 VMs · 3 VM-apps`), runbook icon.
ExtractButton + TruncationBanner reused.

**`/ui/applications/:id` — detail page.** Header card (name, block chip,
description, edit). Stacked cards:

1. **Ownership & context** — reuses the existing curated-metadata card
   family (owner / criticality / notes / runbook_url / annotations).
2. **Classification (DICT)** — reuses the existing four-axis selector +
   `sec_notes` component from ADR-0008.
3. **Members** — three sub-tables (workloads / VMs / VM-applications). Per
   row: link to detail, plus an "unlink" button (admin/editor only). Footer:
   "+ Add member" search box that picks an unlinked workload/VM and PATCHes
   it.
4. **End-of-life summary** — aggregated EOL rows from `internal/eolagg`
   (§5), coloured red / orange / green / grey by status. Each row shows a
   `sources` chip listing which member assets contribute (resolves the
   "where does this signal come from?" question without a click).

**`/ui/applications/blocks` — admin page** (admin scope). CRUD list. Block
detail is light: name, description, owner, notes, member-application count.

**`/ui/admin/classification` — DICT heat-map** (admin scope; closes
ADR-0008 IMP-007). One table: every Application with `MAX(DICT) ≥
threshold` (default 3, slider-adjustable). Columns: app name, block, D / I /
C / T (heat-coloured), `sec_notes` excerpt. CSV extract via
`/v1/applications/extract.csv?dict_min=3`.

**Workload detail page.** New compact card "Application" between the labels
card and the curated-metadata card. Read mode: chip + link, or "Not linked"
+ "Link…" button. Edit mode: searchable single-select (autocompleted from
`GET /v1/applications?name=`) with inline "Create new application…" modal.
The existing Workload-DICT card gains an inheritance banner when the
workload is linked AND the linked Application has any DICT set:
*"DICT inherited from application `vault`. Edit on the application."* The
card flips to read-only with an "Override on this workload" escape hatch for
the rare case where a workload genuinely needs a per-instance classification
(rendered audit-visible).

**Namespace detail page.** Same inheritance banner *when every workload in
the namespace is linked to the same application*. Otherwise the existing
namespace-DICT card stays writable. No `application_id` column on namespaces
— the inheritance is computed; the API never advertises it as a stored
field.

**VM detail page.** Same Application card as for workloads, sitting between
the Tags/Labels card and the existing Applications card from ADR-0019. The
ADR-0019 per-VM-app row editor gains a "linked application" picker column;
if a product name matches an existing application name exactly, the picker
pre-selects it for the operator to confirm (the server does not auto-link).

**EOL dashboard (`/ui/eol`).** The existing Type column (cluster/node/vm)
gains a sibling Application chip-filter and column. Unlinked assets show
"—".

**Top navigation.** New "Applications" link between "Workloads" and
"Virtual Machines". Admin sub-menu gains "Application blocks" and
"Classification heat-map".

### 4. MCP server exposure

`internal/mcp` (ADR-0014) gains three **read-only** tools — operators edit
via UI/API; MCP is for asking questions. Gated by the existing
`mcp_enabled` setting and the caller's `read` scope.

| Tool | Purpose |
|---|---|
| `list_applications` | Cursor-paginated. Same filters as `GET /v1/applications`. |
| `get_application` | By id OR name. Returns full detail + member list + aggregated EOL summary. Member list bounded to ~500 per call; paginates beyond. |
| `list_application_blocks` | Cursor-paginated with member-application counts. |

No write tools — LLM-driven misclassification of SNC-relevant data is a risk
class we explicitly avoid. Existing `list_workloads` / `get_workload` /
`list_virtual_machines` / `get_virtual_machine` payloads gain an
`application` field — additive, doesn't break existing schemas.

### 5. EOL aggregation per Application

The Application detail page's End-of-life card and `GET
/v1/applications/{id}/eol` call into a new aggregator in `internal/eolagg/`
(the package already powering `/v1/eol/extract` + dashboard fixtures).

**Sources.** For an application:

1. Linked workloads → their `annotations` JSONB for `longue-vue.io/eol.*`
   keys, plus per-container `image_versions` enrichment (ADR-0022).
2. Linked VMs → their `annotations` JSONB for `longue-vue.io/eol.*` keys
   (per ADR-0019, the EOL enricher writes one annotation per VM-application
   product).
3. Linked VM-application entries — transitively read through the parent VM's
   annotations. A VM-app row with `application_id` linked routes the
   matching `longue-vue.io/eol.<product>` annotation to the application,
   even when the parent VM as a whole is not linked.

**Output shape.** Flat list of `{product, cycle, eol_status, latest_available,
evaluated_at, sources: [{kind, id, name}…]}` rows. Same shape the EOL
dashboard already renders; the **`sources`** array is the new field, so a row
"Vault 1.13 — EOL since 2024-12-09" lists "2 workloads (kube-prod/vault,
kube-staging/vault) + 1 VM-app (bastion-eu-west-2 / vault)".

**Read-time aggregation.** The enricher is unchanged — no new background
pass, no extra DB writes per tick, no rebuild step on re-linking. The query
cost is one `SELECT annotations FROM workloads WHERE application_id=$1
UNION ALL ... FROM virtual_machines ...` returning tens of rows; sub-50ms on
the expected fleet sizes. No caching in v1; the heat-map page does not use
this path — it filters the `applications` table directly with
`GREATEST(sec_*) >= $threshold`, a small-table scan at the expected
hundreds-of-applications size (no per-axis index needed; FUT-004 covers the
materialised-view escape hatch if it tips over).

### 6. DICT inheritance, write rules, audit

We elected to **preserve** the existing Workload/Namespace DICT columns
alongside the new Application DICT (deliberate Q4 choice during
brainstorming — backwards-compatible). The cost is two writable sources for
the same conceptual value; the rules below pin precedence so audit reads
stay deterministic.

**Read precedence.** Computed at read time on Workload (and symmetrically on
Namespace, with the caveat that namespaces inherit only when *every*
workload in the namespace links to the *same* application):

```
if workload.application_id IS NOT NULL
   AND any DICT axis is set on the linked Application:
       effective_dict = Application.dict
       source         = "application"
else if any DICT axis is set on the Workload row:
       effective_dict = Workload.dict
       source         = "workload"
else:
       effective_dict = null
       source         = "none"
```

Workload and VM list/detail responses gain a read-only sibling field
`effective_dict: {disponibilite, integrite, confidentialite, tracabilite,
notes, source}` so MCP, extracts, and downstream reporting never
re-implement the precedence.

**Write rules.**

- Editor/admin can still PATCH the Workload's own DICT columns while linked;
  the data is preserved as the unlink-fallback and never silently
  disappears.
- The UI's Workload-DICT card switches to read-only with an inheritance
  banner when linked. An "Override on this workload" toggle re-enables
  per-workload classification for the rare case where it's genuinely needed.
  The override is audit-visible.
- The Application's DICT is the **only** path that affects aggregated
  reports (heat-map, `dict_min=` filter, extract). Workload-level overrides
  do not bubble up.
- Validation: 0..4 per axis (all nullable), `sec_notes` ≤ 4096 chars.
  Reuses the validators from ADR-0008's existing namespace/workload
  handlers.

**Audit.** `AuditMiddleware` records DICT writes wherever they happen —
PATCH on Application creates a row with `resource_type="application"`, PATCH
on Workload creates a row with `resource_type="workload"`. A linked
workload's effective DICT changing because the Application's DICT changed
produces **one** audit row (on the Application); the dependent workload
rows do not get synthetic entries (would multiply audit volume by member
count without adding signal). SNC auditors trace effective classification
by joining `workloads.application_id → applications.audit_events` at query
time.

**No data migration.** Existing Workload/Namespace DICT data is not moved
into Applications. There are no Applications yet to migrate into; auto-
creating one per classified workload would generate junk inventory the
operator then has to clean up; the operator-curated invariant (deliberate
design choice) says humans create applications. Operators classify
Applications as they create them, and the workload DICT becomes redundant
naturally. A follow-up ADR (FUT-001) will deprecate the workload/namespace
DICT columns once coverage telemetry justifies removal.

**Coverage telemetry.** A new gauge
`longue_vue_dict_coverage{source="application"|"workload"|"none"}` exposes
the count of workloads in each effective-DICT state. Refreshed every 60s by
the existing metrics goroutine. Drives the deprecation decision.

### 7. Collector invariants (pinned by integration tests)

- K8s collector `UpsertWorkload` SET list excludes `application_id` and all
  DICT columns. Per-tick patches preserve them by omission (same invariant
  ADR-0008 IMP-002 introduced for the curated columns).
- VM collector `UpsertVirtualMachine` SET list excludes `application_id`,
  and on the existing `applications` JSONB write path excludes the
  per-entry `application_id` field (the collector does not curate
  application linkage; only the operator does).
- Both invariants pinned by integration tests mirroring
  `TestPGClusterCuratedMetadata`: link a workload to an application, run a
  reconcile, assert `application_id` survives.

### 8. Configuration

No new env vars on longue-vue. No new chart values. The MCP exposure
inherits the existing `mcp_enabled` setting; the EOL aggregation inherits
the existing `LONGUE_VUE_EOL_APPROACHING_DAYS` window. The collector and
vm-collector binaries are unchanged.

### 9. Metrics

| Metric | Labels | Source |
|---|---|---|
| `longue_vue_applications_total` | `has_block`, `has_dict` | New, refreshed 60s. |
| `longue_vue_application_blocks_total` | — | New, refreshed 60s. |
| `longue_vue_dict_coverage` | `source` (= `application`/`workload`/`none`) | New (§6). |
| `longue_vue_http_requests_total` | `method, route, status` | Existing; new routes fold in automatically. |
| `longue_vue_eol_enrichments_total` | `entity_type` | Existing; unchanged (aggregation is read-time, no new enricher pass). |

## Consequences

### Positive

- **POS-001** Closes the SNC chapter 8 framing properly. An SNC assessor
  walking the evidence package can point at an Application row and read
  owner, criticality, DICT, member assets, and EOL exposure in one place —
  no per-workload reconciliation.
- **POS-002** DICT lands on the EBIOS-RM Application abstraction, matching
  the standard's intent. ADR-0008 acknowledged this was the right home; the
  reason it landed on Namespace/Workload then was the absence of an
  Application entity. That dependency is now resolved.
- **POS-003** Cross-substrate inventory works. "Vault" as one Application
  spans Kubernetes workloads in N clusters AND the bastion VM running it
  natively — one row, one DICT classification, one EOL summary, one
  runbook.
- **POS-004** Two-level inventory (ApplicationBlock → Application → assets)
  matches the SNC framing the user prompt quoted directly. The CMDB now has
  the vocabulary the standard uses.
- **POS-005** Aggregated EOL view per Application turns the EOL enricher's
  per-asset annotations into actionable per-application signal. Operators
  see "Vault application has 4 EOL cycles" instead of having to mentally
  union the per-asset rows.
- **POS-006** Classification heat-map admin page closes ADR-0008 IMP-007,
  which was explicitly deferred at that time waiting for an Application
  entity to exist.
- **POS-007** MCP exposure makes "what does the Vault application look
  like?" answerable conversationally — natural fit for an LLM tool, no
  write surface so no misclassification risk.
- **POS-008** CSV extract for SNC evidence packaging — auditors get a
  per-application export with DICT, members, and EOL exposure in the same
  shape they already consume from `/v1/eol/extract` and `/v1/search/extract`.
- **POS-009** Backwards-compatible. Existing workloads / VMs continue to
  function unchanged; `application_id` is nullable. ADR-0008's
  Workload/Namespace DICT columns are preserved and remain the fallback;
  no operator workflow breaks on day one.
- **POS-010** Collector invariant identical to the curated-metadata pattern
  (ADR-0008 IMP-002, ADR-0019 §4). Reviewers and contributors reading the
  new code see the same shape they already know.

### Negative

- **NEG-001** Two writable DICT sources (Application + Workload/Namespace)
  require the disambiguation rules in §6. The rules are deterministic, but
  the *concept* is more complex than "DICT lives on workload" alone.
  Mitigation: the `effective_dict.source` field, the inheritance banner in
  the UI, and the `longue_vue_dict_coverage` metric all make the situation
  legible. The FUT-001 deprecation of the legacy columns is the cure.
- **NEG-002** Cold-start work. An empty `applications` table on
  installation means operators have to populate it before the new UI is
  useful. Mitigation: existing workloads keep working as before; the
  Application surface is additive, not load-bearing on day one. We
  deliberately did not auto-create Applications from labels or name
  matches (deliberate design choice — auto-derivation produces junk
  inventory that's harder to clean up than to skip).
- **NEG-003** Three new tables / columns + four new top-level UI routes is
  a substantial single PR series. Mitigation: implementation phases are
  separable (data model → API → core UI → MCP → EOL aggregation →
  heat-map), each shippable independently behind the others. The
  implementation plan (FUT-006 below) will sequence them so reviewable
  chunks land progressively.
- **NEG-004** ApplicationBlock is itself a fairly thin abstraction — one
  CRUD page, one optional FK on Application. Risk of "yet another empty
  table" if operators never adopt blocks. Mitigation: the SNC framing
  asks for it explicitly; the UI tolerates "Unblocked" as a valid state.
  If post-adoption telemetry shows zero blocks at six months, deprecation
  is a small ADR.
- **NEG-005** `application_id` on `virtual_machines.applications` JSONB
  entries is a "soft" FK (PostgreSQL does not enforce JSONB FKs).
  Validation happens in the API handler; a hostile direct SQL writer could
  insert a dangling UUID. Mitigation: the same shape exists in
  `containers[]` JSONB elsewhere in the schema; the threat model relies on
  the API as the only writer (collectors + admins). A periodic
  consistency-check task (FUT-005) can sweep for orphans if needed.
- **NEG-006** Read-time EOL aggregation means a slow page-load if a single
  Application has thousands of linked workloads. The query is paginated
  at 500 members; expected fleet sizes (low hundreds of workloads per
  application even in the largest SNC deployments) keep this well under
  the budget. FUT-004 (materialised view) is the upgrade path if needed.

## Alternatives Considered

### Application = Namespace alias (relabeling)

- **ALT-001 Description:** Keep the current model; "Application" is a UI
  rename of Namespace, ApplicationBlock is a curated tag on namespaces.
- **ALT-002 Rejection Reason:** The SNC framing the user prompt quoted —
  "an application is a coherent set of IT objects, a grouping of
  application services" — explicitly transcends namespace boundaries.
  Single-namespace deployments are a degenerate case; prod/staging splits
  are the norm. Renaming Namespace would force every multi-namespace
  application back into the per-namespace reconciliation problem ADR-0008
  already lives with.

### Application as a tag on workloads

- **ALT-003 Description:** Add an `application` string field on Workload
  and on VM-application JSONB; no new entity, no detail page.
- **ALT-004 Rejection Reason:** No detail page means no place for the
  Application-level owner, criticality, DICT, runbook, EOL summary — the
  point of the SNC §8.3 framing. A tag answers "which workloads are part of
  X" but not "what is X".

### Workloads-only membership (no VMs)

- **ALT-005 Description:** First-class Application, but membership is
  workloads only. VM-applications and VMs stay outside.
- **ALT-006 Rejection Reason:** The "Vault on a bastion VM" use case is
  precisely why ADR-0019 added VM-applications in the first place. Cutting
  it out here would force a second ADR within weeks, and the migration
  cost (a new FK on `virtual_machines`) would be paid then anyway. Cheaper
  to do once.

### Many-to-many membership via junction table

- **ALT-007 Description:** `application_members(application_id, kind,
  child_id)` join table from day one. A workload could belong to N
  applications.
- **ALT-008 Rejection Reason:** Cardinality 1 is the deliberate Q5 choice.
  The schema is smaller, the UI is simpler ("this workload belongs to:
  Vault"), and reporting is unambiguous. A shared sidecar that genuinely
  serves two apps is rare enough to belong to the application that owns
  it. If the use case becomes real, an additive junction is a backward-
  compatible extension; the existing FK becomes the "primary" hint.

### Auto-derive Applications from labels (`app.longue-vue.io/application=…`)

- **ALT-009 Description:** The K8s collector reads a configured label on
  workloads and auto-creates / auto-links Applications without operator
  involvement.
- **ALT-010 Rejection Reason:** Breaks the operator-curated invariant
  shared by every curated entity in the codebase. SNC audit
  reproducibility requires every link to carry `added_by` + `added_at`;
  collector-driven links cannot honestly carry an operator identity. The
  label convention is a fine **suggestion source** for a future UI helper
  (FUT-002), but the link itself remains operator-confirmed.

### Move all DICT to Application (no fallback on Workload/Namespace)

- **ALT-011 Description:** This ADR also drops the DICT columns from
  Workload and Namespace at the same migration, supersedes ADR-0008's
  placement entirely.
- **ALT-012 Rejection Reason:** The deliberate Q4 design choice preserves
  the existing columns. The rationale is twofold: (a) workloads that are
  never linked to an Application would lose their classification on day
  one of the migration with no recovery path, and (b) the migration is
  reversible — operators classify Applications over time, the
  `longue_vue_dict_coverage` gauge confirms when ≥90% of classified
  workloads are linked, and a follow-up ADR drops the legacy columns
  with confidence. We pay one structural complexity cost (precedence
  rules) to avoid an irreversible data-loss risk.

### Skip ApplicationBlock entirely in v1

- **ALT-013 Description:** Ship Application only; defer Block to a future
  ADR.
- **ALT-014 Rejection Reason:** The two-level inventory framing is the
  user prompt's exact ask, and the Block table is genuinely tiny (six
  columns, no JSONB, no enricher coupling). The marginal cost of shipping
  Block alongside Application is negligible compared to running a
  follow-up ADR cycle for it.

### Write-time EOL aggregation (materialised summary on each enricher tick)

- **ALT-015 Description:** The EOL enricher computes and stores a
  `applications.eol_summary` JSONB on every tick so the detail page reads
  it in O(1).
- **ALT-016 Rejection Reason:** Adds write amplification proportional to
  application count × enricher frequency for a page that is operator-
  facing (not high-QPS). Re-linking would also require a forced rebuild.
  The read-time aggregation in §5 costs sub-50ms; trading that for
  background bookkeeping is premature. FUT-004 (materialised view) is the
  upgrade path if profiling later shows it's needed.

### Per-Application EOL annotations (write-through to a new namespace)

- **ALT-017 Description:** The EOL enricher writes per-application
  annotations under a new `longue-vue.io/app-eol.*` namespace, parallel to
  the existing `longue-vue.io/eol.*` per-entity annotations.
- **ALT-018 Rejection Reason:** Splits the EOL UI by entity type — the
  existing dashboard reads one namespace, the new application view would
  read another. Single read path (per ADR-0019 ALT-013/014) is the
  established symmetry; reuse it.

## Implementation Notes

- **IMP-001** Migrations: three new files, ordered as in §1
  (`00045_create_application_blocks.sql`, `00046_create_applications.sql`,
  `00047_add_application_id_to_workloads_and_vms.sql`). No data backfill.
  The existing `virtual_machines.applications` JSONB rows pick up the
  optional `application_id` field transparently (JSONB is schemaless; new
  rows include it, old rows do not).

- **IMP-002** `internal/store/`:
  - New file `pg_applications.go` with the standard CRUD shape
    (`CreateApplication`, `GetApplication`, `GetApplicationByName`,
    `ListApplications`, `UpdateApplication`, `DeleteApplication`,
    `ListApplicationMembers`). Mirror the structure of
    `pg_cloud_accounts.go`.
  - New file `pg_application_blocks.go` with the parallel CRUD set.
  - Extend `pg.go` `UpsertWorkload` / `UpdateWorkload` SET-list comments to
    name `application_id` explicitly as preserved-by-omission (collector
    invariant).
  - Extend `pg_virtual_machines.go` `UpsertVirtualMachine` /
    `UpdateVirtualMachine` similarly.
  - Extend `ListWorkloads` / `ListVirtualMachines` filters to wire the new
    `application_id`, `application_name`, `unlinked` parameters.
  - `application_name` resolution: a single sub-SELECT
    `(SELECT id FROM applications WHERE name = $N)` rather than a
    two-round-trip lookup. O(1) on the UNIQUE index.

- **IMP-003** `internal/api/`:
  - New file `application_handlers.go` with the standard handler shape.
  - New file `application_block_handlers.go` parallel to it.
  - Extend `workload_handlers.go` and `virtual_machine_handlers.go`
    `HandlePatch...` to accept `application_id` / `application_name`.
    Resolve names before validation. Reject unknown UUIDs with 400.
  - Extend `virtual_machine_handlers.go` `HandlePatchVirtualMachine` to
    validate per-entry `application_id` in the `applications` JSONB array.
  - Add `effective_dict` computation in the workload / VM list+get
    response builders. Centralise in a small `internal/api/dict_inherit.go`
    helper so the precedence is one function call from every callsite.

- **IMP-004** `internal/eolagg/`:
  - New file `application.go` exporting `AggregateForApplication(ctx,
    store, appID) ([]Row, error)`. Returns the
    `{product, cycle, eol_status, latest_available, evaluated_at,
    sources}` shape from §5.
  - Helper to walk linked VM-app entries even when the parent VM as a
    whole is not linked.

- **IMP-005** `internal/mcp/`:
  - Three new tools per §4. Each lands in its own file
    (`tool_list_applications.go`, etc.) following the existing per-tool
    file convention.
  - Existing `list_workloads` / `get_workload` / `list_virtual_machines` /
    `get_virtual_machine` payloads gain the `application` field — pure
    additive surface, no schema break.

- **IMP-006** OpenAPI:
  - `api/openapi/openapi.yaml` gets the new schemas and endpoints listed
    in §2.6.
  - `make swagger-sync` copies into `internal/api/swagger/openapi.yaml`;
    `make swagger-sync-check` enforced in CI.
  - `openapi_validation_test.go` cases for the new shapes.

- **IMP-007** UI (`ui/src/`):
  - New files `pages/Applications.tsx`, `pages/ApplicationDetail.tsx`,
    `pages/ApplicationBlocks.tsx` (admin), `pages/ClassificationHeatmap.tsx`
    (admin).
  - New components under `components/inventory/`:
    `ApplicationCard.tsx` (single-select picker + create modal, shared by
    Workload + VM detail), `ApplicationMembersTable.tsx`,
    `ApplicationEOLSummary.tsx`.
  - Reuse the existing four-axis `ClassificationCard` from ADR-0008.
  - Extend `WorkloadDetail.tsx`, `NamespaceDetail.tsx`,
    `VirtualMachineDetail.tsx` per §3.
  - Extend `EolDashboard.tsx` with the Application column + filter chip.
  - Extend `api.ts` with `Application`, `ApplicationBlock`, the new
    filters, the linking patches, and the EOL aggregation endpoint.
  - Add top-nav entries.

- **IMP-008** Tests:
  - **Store unit:** `pg_applications_test.go` and
    `pg_application_blocks_test.go` cover CRUD + idempotent POST + last-
    block deletion handling (FK NULL on member apps).
  - **Store integration:** extend the existing PG integration suite to
    link a workload to an application, run a collector reconcile, assert
    `application_id` survives.
  - **Handler unit:** `application_handlers_test.go` and
    `application_block_handlers_test.go` cover RBAC, validation,
    name-resolution precedence (`application_id` wins over
    `application_name`), 404 on unknown UUIDs.
  - **Inherit unit:** `dict_inherit_test.go` table-driven cases
    (linked+set on app, linked+unset on app, unlinked+set on workload,
    nothing set).
  - **Aggregator unit:** `eolagg/application_test.go` per §5 final
    paragraph.
  - **OpenAPI validation:** §2.6.
  - **UI:** `Applications.test.tsx`, `ApplicationDetail.test.tsx`,
    `Workloads.test.tsx` (extended), `VirtualMachineDetail.test.tsx`
    (extended), `EolDashboard.test.tsx` (extended).

- **IMP-009** Documentation deliverables:
  - New section in `docs/applications.md` documenting the operator
    workflow (create blocks, create applications, link workloads/VMs,
    classify DICT, read EOL summary).
  - `docs/compliance/snc-chapter-8.md`: update the §8.3 row to point at
    Application (with the fallback note covering the transition window).
  - `docs/api-reference.md`: new Application + ApplicationBlock sections.
  - `CLAUDE.md`: new bullet under "Layout" mentioning the
    `applications` / `application_blocks` tables; new bullet under
    "Curated metadata pattern" listing Application as the new home for
    DICT; small extension to the EOL section noting the aggregated view.
  - `README.md`: ADR-0029 row; features list addition ("Application and
    ApplicationBlock inventory aligned with ANSSI applicative layer").
  - `CHANGELOG.md`: `Added` entry under the next minor.

- **IMP-010** Helm: no chart-version bumps for collector / vm-collector /
  ingest-gw (unaffected). `charts/longue-vue/Chart.yaml` `appVersion` bumps
  at release time. No new chart values.

- **IMP-011** Phasing for the implementation plan (writing-plans skill
  follow-up): six independently reviewable chunks —
  - **Phase 1:** Migrations + store CRUD + handlers + OpenAPI for
    Application / ApplicationBlock entities. No linking yet.
  - **Phase 2:** Workload + VM linking PATCH extensions; VM-application
    per-entry `application_id`. New list filters.
  - **Phase 3:** Application list + detail UI (members card, curated card,
    DICT card). Top-nav entry.
  - **Phase 4:** DICT inheritance (`effective_dict`, banner, override
    toggle). Coverage metric.
  - **Phase 5:** EOL aggregation backend + Application detail card +
    `GET /v1/applications/{id}/eol` endpoint.
  - **Phase 6:** Classification heat-map admin page + CSV extract +
    MCP tools.

## Future work

- **FUT-001** Deprecate Workload/Namespace DICT columns once
  `longue_vue_dict_coverage{source="application"}` exceeds ~90% of
  classified workloads. Single migration to drop the columns + a follow-up
  ADR supersedes ADR-0008's placement.
- **FUT-002** Label-convention helper: the K8s collector surfaces
  `app.longue-vue.io/application=<name>` workload labels as a *suggestion*
  in the UI's link picker. The operator still confirms the link
  (operator-curated invariant preserved).
- **FUT-003** Many-to-many membership via `application_members` junction
  table if shared-infrastructure accounting becomes a real need. Additive
  to the current FK shape.
- **FUT-004** Materialised view for read-heavy EOL aggregation if a
  deployment grows large enough that read-time aggregation tips over the
  latency budget. Not expected at SNC target sizing.
- **FUT-005** Periodic consistency-check task that sweeps
  `virtual_machines.applications[].application_id` for orphans (the
  PostgreSQL FK does not reach into JSONB).
- **FUT-006** Cross-Application `depends_on` edges (similar to the
  curated-edges work outlined in ADR-0015 FUT-003). Knowing "the WebApp
  Application depends on the Vault Application" feeds impact-analysis
  queries directly.
- **FUT-007** CVE enrichment per Application alongside EOL, mirroring the
  shape proposed in ADR-0019 FUT-004.

## Post-implementation notes

> **Correction (2026-05-24, Phase 7).** Two of this ADR's planning
> assumptions diverged from the codebase as it actually stood. The
> implementation reflects reality; these notes pin the divergence so the
> precedence rules above are not read as describing a code path that never
> fires.

- **PIN-001 — Workload/Namespace DICT columns never existed.** §6, the
  "preserve the fallback" framing in the Decision, ALT-011/012, NEG-001,
  POS-009, and FUT-001 all assume ADR-0008 had already shipped `sec_*`
  columns on `namespaces` and `workloads`. It had not. ADR-0008 IMP-001
  proposed those columns in migration slot `00023_application_security_
  classification.sql`, but that slot was taken by
  `00023_create_cloud_accounts.sql` (ADR-0015) and the DICT migration was
  never written. **Consequence:** DICT lives *only* on `applications`
  (added by this ADR). There is nothing to fall back to, so the
  `effective_dict.source` field resolves to `application` or `none` in
  practice; the `workload` and `namespace` source values remain in the
  enum for forward-compatibility but never fire today. The
  `longue_vue_dict_coverage{source="workload"}` bucket is therefore always
  `0`. The §6 precedence ladder is still implemented as written (the
  `workload` branch is dead-but-harmless), and the workload-DICT
  inheritance banner / override escape-hatch are not built — there is no
  per-workload DICT to override. FUT-001 (deprecate the legacy columns) is
  moot: there are no legacy columns to drop.

- **PIN-002 — Workload EOL signal comes from image-versions, not
  annotations.** §5 source (1) describes reading `longue-vue.io/eol.*`
  annotations off workloads. In practice the `Workload` type carries no
  `annotations` field and no per-workload EOL annotations; the workload
  EOL signal comes exclusively from per-container image-versions
  enrichment (ADR-0022). The implemented aggregator therefore derives
  workload rows from image-versions (`eol_status="outdated"` when a
  container image is behind the latest available tag, `unknown`
  otherwise) and VM rows from the `longue-vue.io/eol.*` endoflife
  annotations (ADR-0019). The `outdated` value is a new `eol_status`
  alongside the existing `eol` / `approaching` / `supported` / `unknown`.

- **PIN-003 — MCP `get_application` omits the EOL summary.** §4 specifies
  `get_application` returns the aggregated EOL summary. It does not in v1:
  the MCP `Store` interface does not satisfy `eolagg.ApplicationStore` and
  the adapter was larger than v1 warranted. Operators read the
  per-application EOL summary via `GET /v1/applications/{id}/eol`. Folding
  it into `get_application` is a deferred follow-up.

## References

- **REF-001** ADR-0001 — CMDB for SNC using Kubernetes —
  `docs/adr/adr-0001-cmdb-for-snc-using-kube.md`
- **REF-002** ADR-0002 — Kubernetes-to-ANSSI cartography layer mapping
  (places workloads in the `applicative` layer; defines the Layer enum) —
  `docs/adr/adr-0002-kubernetes-to-anssi-cartography-layers.md`
- **REF-003** ADR-0006 — Web UI for audit and curated metadata
  (established the curated-metadata card pattern reused on Application
  detail) — `docs/adr/adr-0006-ui-for-audit-and-curated-metadata.md`
- **REF-004** ADR-0008 — Asset-management data model for SNC v3.2
  chapter 8 (current DICT placement on Namespace/Workload; this ADR
  preserves those columns as fallback per §6) —
  `docs/adr/adr-0008-secnumcloud-chapter-8-asset-management.md`
- **REF-005** ADR-0012 — End-of-life enrichment via endoflife.date —
  `docs/adr/adr-0012-eol-enrichment-via-endoflife-date.md`
- **REF-006** ADR-0014 — MCP server — `docs/adr/adr-0014-mcp-server.md`
- **REF-007** ADR-0015 — VM collector for non-Kubernetes platform VMs —
  `docs/adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md`
- **REF-008** ADR-0019 — VM applications, EOL enrichment for platform
  software, VM list search filters (the existing
  `virtual_machines.applications` JSONB this ADR extends) —
  `docs/adr/adr-0019-vm-applications-and-eol-and-search.md`
- **REF-009** ADR-0022 — Image versions enrichment —
  `docs/adr/adr-0022-image-versions-enrichment.md`
- **REF-010** ADR-0024 — Audit no-op write filtering —
  `docs/adr/adr-0024-audit-no-op-write-filtering.md`
- **REF-011** ADR-0027 — Denormalize parent names on list responses
  (pattern reused for the `block` field on Application list rows) —
  `docs/adr/adr-0027-denormalize-parent-names-on-list-responses.md`
- **REF-012** ANSSI — *Prestataires de services d'informatique en nuage
  (SecNumCloud) — référentiel d'exigences*, v3.2, 2022-03-08, chapter 8
  "Gestion des actifs".
- **REF-013** ANSSI — *EBIOS Risk Manager* (source of DICT and the 0..4
  scale; locates DICT on the Application abstraction).
- **REF-014** ANSSI cartography — https://my-carto.com/blog/cartographie-anssi-cybersecurite/
