---
title: "ADR-0019: VM applications inventory, EOL enrichment for platform software, and VM list search filters"
status: "Accepted"
date: "2026-04-29"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "vm", "eol", "search", "secnumcloud", "asset-management"]
supersedes: ""
superseded_by: ""
---

# ADR-0019: VM applications inventory, EOL enrichment for platform software, and VM list search filters

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

## Context

Three related gaps surfaced after ADR-0015 shipped the `virtual_machines` table:

1. **The CMDB knows what platform VM exists, but not what runs on it.** A row like `bastion-prod-eu-west-2` carries cloud-provider metadata (image, instance type, IPs, VPC, security groups) but says nothing about the actual *workload* running inside the guest — Vault, Cyberwatch, BIND, Nginx, an internal STUN relay, etc. Operators answer "what version of Vault does this VM run?" today by SSH-ing in or asking the Ansible inventory; SecNumCloud auditors have to take the operator's word for it. SNC chapter 8 (asset management — ADR-0008) requires a complete inventory of *software* running on platform infrastructure, not just hardware.

2. **The EOL enricher is a no-op for VMs.** ADR-0012 (EOL enrichment via endoflife.date) and ADR-0015 §NEG-002 explicitly flagged this: without guest-OS information, there is nothing on a `virtual_machines` row that the enricher can match against an endoflife.date product. The `kernel_version` and `operating_system` columns are NULL for every VM (no in-guest agent — FUT-002 from ADR-0015 is still open). Meanwhile, the **applications** running on those VMs (Vault 1.13.4, BIND 9.18, etc.) *are* tracked by endoflife.date — and operators *do* know which versions they deploy, because they deployed them. The signal is there; we just have nowhere to record it.

3. **The VM list page can't answer common operational questions.** The current `GET /v1/virtual-machines` server-side filter set is `cloud_account_id`, `region`, `role`, `power_state`, `include_terminated`. Operators routinely ask:

   - "Which VMs are named `*-bastion-*`?" (incident response — find the bastion in cloud account X)
   - "Which VMs run image `ami-75374985`?" (an old AMI is being decommissioned — who's still on it?)
   - "Which VMs are in cloud account `acme-prod`?" (UI currently requires the operator to know the UUID)
   - "Which VMs run Vault?" (impossible today — applications aren't a column)

   All four are routine asset-management questions. The first three are pure listing-API gaps; the fourth depends on (1) being solved first.

The user directive: **operators must be able to record applications running on each VM, the EOL enricher must scan those applications' lifecycles, and the VM list must be searchable by name / region / cloud account / image / role.**

This ADR resolves the data model, the API surface, the enricher extension, the search semantics, and the UI surface. It does **not** introduce an in-guest agent — application data is operator-curated in v1, exactly like `owner` / `criticality` / `notes` already are. Auto-discovery via an agent (or an SSH probe) remains the FUT-002 path from ADR-0015 and can use the same column when it lands.

## Decision

**Add an operator-curated `applications` JSONB column on `virtual_machines`, extend the EOL enricher to walk that column and write `longue-vue.io/eol.<product>` annotations against the same endoflife.date API used for clusters and nodes, and grow the `GET /v1/virtual-machines` filter set to cover name (substring), image (substring on `image_id` OR `image_name`), cloud account (by name *or* UUID), region (exact), role (exact), and the new `application` (JSONB containment).**

### 1. Data model — `virtual_machines.applications`

A new JSONB column on the existing table. No child table.

```sql
ALTER TABLE virtual_machines
    ADD COLUMN applications JSONB NOT NULL DEFAULT '[]'::jsonb;

CREATE INDEX virtual_machines_applications_gin_idx
    ON virtual_machines USING GIN (applications jsonb_path_ops);

CREATE INDEX virtual_machines_name_lower_idx
    ON virtual_machines (LOWER(name));

CREATE INDEX virtual_machines_image_id_idx
    ON virtual_machines (image_id);
```

The element shape:

```json
[
  {
    "product": "vault",
    "version": "1.15.4",
    "name": "vault-prod-eu",
    "notes": "production secrets backend, autounseal via OSC KMS",
    "added_at": "2026-04-29T10:00:00Z",
    "added_by": "alice@example.com"
  },
  {
    "product": "cyberwatch",
    "version": "12.4",
    "notes": "vulnerability scanner — not on endoflife.date"
  },
  {
    "product": "bind",
    "version": "9.18.30",
    "name": "primary-dns"
  }
]
```

| Field      | Type   | Required | Source                                                    |
|------------|--------|----------|-----------------------------------------------------------|
| `product`  | string | yes      | Operator-typed. Should match an endoflife.date product id when one exists. **Normalized at write time:** trimmed + lowercased + spaces collapsed to hyphens (so `"Hashicorp Vault"` and `"hashicorp-vault"` deduplicate, and the same `product` becomes a stable lookup key for the enricher and the `?application=` filter). Free-form: products not tracked by endoflife.date (Cyberwatch, internal tools) are still recorded, just not enriched. |
| `version`  | string | no       | Operator-typed. Free-form; the enricher extracts a major.minor cycle when present. |
| `name`     | string | no       | Operator-friendly label, e.g. `vault-prod-eu` for cases where the operator wants to disambiguate two instances of the same product on the same VM. |
| `notes`    | string | no       | Free-form context.                                        |
| `added_at` | string | no       | Server-stamped on `PATCH` when the entry is new (RFC 3339). |
| `added_by` | string | no       | Server-stamped from the auth caller (`username` for sessions, token name for PATs). |

**Why JSONB, not a child table.** A VM carries a small fixed-cardinality list (typical: 1–5 apps; outliers: ~20 on a multi-tenant utility box). Cross-VM queries we care about ("which VMs run Vault?") are `WHERE applications @> '[{"product":"vault"}]'::jsonb` — the GIN index makes that O(log n) without join overhead. We have **no** ad-hoc cross-VM queries that need a normalized schema (e.g. "list every app version across the fleet sorted by EOL distance" can be served by the EOL Inventory page reading annotations, not by joining a child table). The JSONB shape mirrors the existing `containers` JSONB on pods/workloads (ADR-0001 family pattern) — operators reading the schema find the same shape twice.

**Why GIN with `jsonb_path_ops`.** It's the cheapest GIN variant for the `@>` containment operator we use, ~40% smaller and ~2× faster than the default `jsonb_ops` variant, at the cost of dropping operators we don't query (`?` / `?&` / `?|` for top-level keys). We never key-match the `applications` column directly.

**Why a `name_lower` functional index.** Server-side name search uses `LOWER(name) LIKE LOWER($1) || '%'` for prefix queries (`name=bast` → `bast%`). A functional index makes that index-only. We *don't* index a substring search (`%bast%` can't use a btree); for very large fleets a `pg_trgm` index would be the next step (FUT-001), but at the expected scale (low thousands of platform VMs per deployment) a sequential scan with the prefix-index fallback is fine.

### 2. EOL enricher extension

The `Enricher` (`internal/eol/enricher.go`) gains a third pass after `enrichCluster` / `enrichClusterNodes`:

```go
func (e *Enricher) enrichVirtualMachines(ctx context.Context) {
    cursor := ""
    for {
        vms, next, err := e.store.ListVirtualMachines(
            ctx,
            api.VirtualMachineListFilter{IncludeTerminated: false},
            100,
            cursor,
        )
        if err != nil { /* log + metric, return */ }
        for i := range vms {
            e.enrichVirtualMachine(ctx, &vms[i])
        }
        if next == "" { return }
        cursor = next
    }
}

func (e *Enricher) enrichVirtualMachine(ctx context.Context, vm *api.VirtualMachine) {
    matcher := GenericVersionMatcher{}
    products := make(map[string]struct{}, len(vm.Applications))
    for _, app := range vm.Applications {
        if app.Product == "" || app.Version == "" {
            continue
        }
        mr, ok := matcher.Match(app.Product, app.Version)
        if !ok { continue }
        // Only enrich first occurrence of a product; duplicates would
        // fight over the same annotation key.
        if _, seen := products[mr.Product]; seen { continue }
        products[mr.Product] = struct{}{}

        ann, err := e.resolveAnnotation(ctx, mr)
        if err != nil { /* log + metric, continue */ }
        if ann == nil {
            // Product not in endoflife.date — write a stub so the UI
            // can show "lifecycle unknown" with the operator's
            // declared version.
            ann = &Annotation{
                Product:   mr.Product,
                Cycle:     mr.Cycle,
                EOLStatus: StatusUnknown,
                CheckedAt: time.Now().UTC().Format(time.RFC3339),
            }
        }
        e.mergeVMAnnotation(ctx, *vm.ID, mr.Product, ann)
    }
}
```

A new matcher `GenericVersionMatcher` lives in `internal/eol/matcher.go`. It takes the operator-declared `product` (already normalized) and the operator-declared `version`, runs the same `^v?(\d+\.\d+)` regex used by `KubernetesMatcher` against the version, and returns `MatchResult{Product: product, Cycle: matchedCycle}`. **No tag-stripping, no distro detection, no runtime-prefix parsing** — those are handled by the existing field-specific matchers (`KubernetesMatcher`, `ContainerRuntimeMatcher`, `OSImageMatcher`, `KernelMatcher`). Operator-declared versions are *expected* to be already-clean strings like `1.15.4` or `1.18`.

**Storage location.** Annotations land on the existing `virtual_machines.annotations` JSONB column under `longue-vue.io/eol.<product>` keys — same namespace, same shape, same merge semantics as clusters and nodes. The EOL Inventory page (`/ui/eol`) discovers them by reading the annotations (not by joining `applications`), so the existing aggregation logic extends naturally.

**Stub annotations for unknown products.** When endoflife.date returns 404 for a product the operator declared (Cyberwatch, internal tools), the enricher writes a stub annotation with `eol_status=unknown` rather than nothing. Why: the UI then shows "Cyberwatch 12.4 — lifecycle unknown" instead of silently dropping the row. Operators see that the data was *evaluated* — the absence of EOL info is a signal, not a bug.

**EnricherStore additions:**

```go
type EnricherStore interface {
    // existing methods...
    ListVirtualMachines(ctx context.Context, filter api.VirtualMachineListFilter,
        limit int, cursor string) ([]api.VirtualMachine, string, error)
    GetVirtualMachine(ctx context.Context, id uuid.UUID) (api.VirtualMachine, error)
    UpdateVirtualMachine(ctx context.Context, id uuid.UUID,
        in api.VirtualMachinePatch) (api.VirtualMachine, error)
}
```

**EOL Inventory UI.** The existing `/ui/eol` page already aggregates `longue-vue.io/eol.*` annotations across clusters and nodes. The aggregation gains a third entity source (VMs). The "Entity" column gains a "type" badge (cluster / node / vm) so operators distinguish a bastion VM from a kube node both running outdated kernels. No new page.

### 3. API surface — list filters

`GET /v1/virtual-machines` grows four query parameters; existing parameters keep current semantics.

| Param                | Type   | Match            | Notes |
|----------------------|--------|------------------|-------|
| `cloud_account_id`   | UUID   | exact            | Existing. |
| **`cloud_account_name`** | string | exact (with provider scope) | **New.** Resolves to the cloud account's UUID server-side. When both `cloud_account_id` and `cloud_account_name` are set, `cloud_account_id` wins (consistency with the cloud-accounts API surface). |
| `region`             | string | exact            | Existing. |
| `role`               | string | exact            | Existing. The UI's comma-split client-side fallback is unchanged. |
| `power_state`        | string | exact            | Existing. |
| `include_terminated` | bool   | flag             | Existing. |
| **`name`**           | string | case-insensitive substring on `name` OR `display_name` | **New.** Trimmed + lower-cased server-side; bounded to 100 chars; rejected with 400 on a longer string. The query is `(LOWER(name) LIKE '%' || $1 || '%' OR LOWER(display_name) LIKE '%' || $1 || '%')`. The `%` and `_` characters in the user-supplied value are escaped before interpolation (LIKE injection — same hazard the VM-dedup query already mitigates). |
| **`image`**          | string | case-insensitive substring on `image_id` OR `image_name` | **New.** Same length / escape rules as `name`. The query is `(LOWER(image_id) LIKE '%' || $1 || '%' OR LOWER(image_name) LIKE '%' || $1 || '%')`. |
| **`application`**    | string | JSONB containment on `applications[].product` | **New.** Normalized server-side (same rules as the write path). The query is `applications @> $1::jsonb` with `$1` set to `[{"product":"<normalized>"}]`. The GIN index serves it. |
| `cursor`             | string | opaque           | Existing cursor pagination. |
| `limit`              | int    | 1..200           | Existing. |

All filters AND together. None of them mutate state. All require the `read` scope (already enforced by `HandleListVirtualMachines`).

**Why substring for `name` and `image`, not exact.** Operators rarely know the full name or AMI; they know a fragment ("the bastions" / "the old AMI"). Substring matches are the natural ergonomics. The bounded length + escaped LIKE wildcards keep the security shape identical to the existing VM-dedup query — no new injection class is introduced.

**Why exact for `region` and `role`.** They're enumerated values (a handful of regions per provider, dozens of roles per fleet). The UI exposes them as dropdowns; the API mirrors that. Substring would be over-engineering.

**Why a separate `cloud_account_name` parameter.** UUIDs are not memorable; operators using the API directly (curl, scripts, the MCP server) want to type `cloud_account_name=acme-prod`. The handler does a single `SELECT id FROM cloud_accounts WHERE provider=$1 AND name=$2` — O(1) on the existing UNIQUE index, no expensive fallback. Provider defaults to `outscale` (the only v1 value); the parameter `cloud_account_provider` is reserved for FUT.

**A note on caller-bound results for vm-collector PATs.** `auth.Caller.EnforceCloudAccountBinding` already restricts what a `vm-collector`-scope token can read. The new filters don't change that — a vm-collector PAT bound to account A still sees only account A's VMs, regardless of `?cloud_account_id=B`.

### 4. API surface — applications PATCH

`PATCH /v1/virtual-machines/{id}` grows one field on `VirtualMachinePatch`:

```go
type VirtualMachinePatch struct {
    DisplayName  *string
    Role         *string
    Owner        *string
    Criticality  *string
    Notes        *string
    RunbookURL   *string
    Annotations  *map[string]string
    Applications *[]VMApplication // NEW
}

type VMApplication struct {
    Product string `json:"product"`
    Version string `json:"version,omitempty"`
    Name    string `json:"name,omitempty"`
    Notes   string `json:"notes,omitempty"`
    AddedAt string `json:"added_at,omitempty"`
    AddedBy string `json:"added_by,omitempty"`
}
```

Semantics:

- **Replace, not merge.** `Applications: &newList` sets the entire list. There is no per-row CRUD — the operator submits the canonical list every time, like Kubernetes' replace semantics on lists. Why: a merge model needs identifiers (`(product,name)` tuples) and a UI that tracks them; replace is simpler, deterministic, and the audit row captures the full before/after.
- **Server-side normalization.** `product` is trimmed, lower-cased, internal whitespace collapsed to single hyphens. Empty `product` is rejected with 400. Duplicate `(product, name)` tuples within the same submission are rejected with 400 (`"duplicate application entry"`).
- **Validation cap.** Maximum 50 applications per VM (rejected with 400 on 51+). Per-field length caps: `product` 64 chars, `version` 64, `name` 128, `notes` 1024. Why caps: prevents a write-scope token from inflating a single row to MB-scale and tipping the JSONB GIN index over.
- **Server stamps `added_at` and `added_by`** on entries that are new compared to the current row's list (matched on `(product, name)` after normalization). Existing entries keep their original stamps so the audit history is preserved.
- **Audit.** Existing `AuditMiddleware` captures the request body. The new fields don't carry secrets; no scrubber additions are needed.
- **Scope.** `write` scope (already enforced for `PATCH /v1/virtual-machines/{id}`). Editor and admin can write; viewer / auditor / vm-collector cannot.

`POST /v1/virtual-machines` (the collector path) **does not** carry `applications`. The collector is read-only against the cloud-provider API and has no view of guest software; applications are exclusively a curated-metadata field.

### 5. UI surface

Three changes to existing pages, no new pages:

**VM detail page (`/ui/virtual-machines/:id`).** A new **Applications** card sits between the existing Tags / Labels / Annotations card and the Curated-metadata card. Read mode shows a table: product, version, name, notes, EOL status badge, latest available, added by, added at. Edit mode flips to a per-row editor with add / remove buttons; submission PATCHes the full new list. Editor + admin see the Edit button; viewer / auditor see the read-only table.

**VM list page (`/ui/virtual-machines`).** Three new filter inputs sit above the existing toolbar:

- **Name** — text input, debounced 250 ms, fires `?name=<value>`.
- **Image** — text input, debounced 250 ms, fires `?image=<value>`.
- **Application** — text input, autocompleted from the union of all distinct `applications[].product` values currently in the fleet (one extra `GET /v1/virtual-machines/applications/distinct` admin-friendly endpoint, paginated; `read` scope; LIMIT 200 distinct products).

Filters compose with the existing region / cloud-account / power-state dropdowns. Any active filter shows a pill chip below the toolbar; clicking the pill clears that filter.

**EOL Inventory page (`/ui/eol`).** The existing aggregator gains a third entity source (`virtual_machines`). A new **Type** column (cluster / node / vm) and a corresponding filter chip in the summary card row. Row-level red/orange highlighting and the existing two-column-group layout ("What we run" / "What's available") are unchanged.

### 6. Configuration

No new env vars on longue-vue. The enricher's existing `LONGUE_VUE_EOL_*` variables (interval, approaching-days, base URL, enabled) and the runtime `eol_enabled` setting toggle apply identically — the new VM pass runs in the same goroutine, gated by the same flag. The collector binaries (`longue-vue-collector`, `longue-vue-vm-collector`) are unchanged.

### 7. Metrics

`internal/metrics/` gains:

- `longue_vue_eol_enrichments_total{entity_type, status}` — re-labelled with `entity_type` covering `cluster`, `node`, **`vm`**. Existing metric grows a new label value; back-compatible.
- `longue_vue_eol_errors_total{entity_type, phase}` — same shape extension.

`longue_vue_virtual_machines_total{cloud_account, terminated}` (existing) is unchanged.

No new metric for the search filters — the existing `longue_vue_http_requests_total{method, route, status}` already tracks `GET /v1/virtual-machines` with all parameter combinations folded into the same series.

## Consequences

### Positive

- **POS-001** Closes ADR-0015 §NEG-002 ("EOL enricher is a no-op for VMs"). Vault, BIND, Nginx, OpenSSH, Postgres, etc. — all the platform-software endoflife.date already tracks — get lifecycle status the day operators record their version.
- **POS-002** SecNumCloud chapter 8 inventory becomes complete: the CMDB now answers "what software is running on each platform VM, at what version, and is it EOL?" without operators reaching for SSH, Ansible, or a spreadsheet.
- **POS-003** SecNumCloud chapter 12 (vulnerability management) gets a structured input. An operator looking at the EOL Inventory page sees Vault 1.13 (EOL since 2024-12-09, approaching the 90-day window) on the Vault VMs *and* on any kube node running an in-cluster Vault — one page, two entity types.
- **POS-004** No agent dependency. Application data is operator-curated from day one. When FUT-002 (in-guest agent) lands, it writes to the same column — no schema change.
- **POS-005** The new search filters answer routine questions today (find by name / image / cloud account / role) that previously required multi-page UI scrolling or out-of-band scripts hitting the database.
- **POS-006** GIN containment query (`?application=vault`) gives operators a one-click answer to "show me every VM running Vault" without joining tables. The same shape can drive future "find every node + VM running Vault" cross-entity views.
- **POS-007** Stub annotations for unknown products (Cyberwatch, internal tools) make absence of data visible — auditors see the row was *evaluated* and not silently dropped.
- **POS-008** Backwards-compatible. Existing VM rows have `applications = '[]'`; existing `GET /v1/virtual-machines` calls without the new filters return identical results.

### Negative

- **NEG-001** **Operator-curated data drifts.** A VM's `applications` list is only as accurate as the last operator who edited it. When the Vault team upgrades Vault 1.15 → 1.18 and forgets to update the CMDB, the EOL row stays correct against 1.15 — wrong picture. Mitigation: server-stamped `added_at` is shown in the UI ("recorded 2025-09-12, 230 days ago") so operators see staleness; FUT-002 (in-guest agent) is the long-term fix.
- **NEG-002** **Free-form `product` field is fragile.** Two operators can record the same software as `vault` and `Vault Enterprise`; the enricher matches the first, misses the second. Mitigation: the autocompleted UI input shows existing distinct products in the fleet, nudging operators toward consistency. A future migration pass could canonicalise `vault-enterprise` ↔ `vault` if the divergence proves real.
- **NEG-003** **GIN index storage cost.** A GIN index on `applications` adds ~30–50% of the column's size. At an expected fleet size of 10K platform VMs × ~3 apps each × ~200 bytes per entry ≈ ~6 MB column + ~3 MB index. Negligible.
- **NEG-004** **Substring `name` / `image` queries cannot use a btree index.** A sequential scan kicks in when the filter is set without other conds. At 10K VM rows the cost is sub-100 ms (well below the listing endpoint's existing latency budget). FUT-001 (`pg_trgm` index) is the upgrade path if a deployment grows past 100K platform VMs — outside the SNC target sizing.
- **NEG-005** **Replace semantics on the `applications` PATCH require the UI to ship the full list every time.** A noisy network can fail mid-edit and lose the operator's typed list. Mitigation: optimistic UI with local draft persistence (existing pattern on the curated-metadata cards) + the audit log captures every PATCH so the previous list is recoverable.
- **NEG-006** **Stub annotations for unknown products grow the annotations JSONB.** A VM with 5 unknown apps adds ~5 stub keys. Mitigation: stub annotations are bounded by the per-VM application cap (50). Worst-case ~10 KB extra annotations payload — within noise floor of the existing entity response sizes.

## Alternatives Considered

### Child table `vm_applications`

- **ALT-001 Description:** Create `vm_applications (vm_id, product, version, name, notes, added_at, added_by)` with FK to `virtual_machines.id`. CRUD via `POST/PATCH/DELETE /v1/virtual-machines/{id}/applications/...`.
- **ALT-002 Rejection Reason:** Heavyweight for what is a small fixed-cardinality list. We have no cross-VM queries that benefit from normalization (we don't aggregate per-app statistics; we read each VM's apps individually and the EOL inventory aggregates *annotations*, not apps). Per-row CRUD complicates the UI without buying a feature operators asked for. The JSONB shape mirrors the existing `containers` JSONB on pods/workloads — same idiom, same place. If cross-VM queries become a real need (FUT-001), JSONB → child-table is a backward-compatible migration.

### Free-form `applications` as `text` / `notes` extension

- **ALT-003 Description:** Don't add a structured column. Operators just write "Vault 1.15.4, Cyberwatch 12.4" into the existing `notes` column.
- **ALT-004 Rejection Reason:** No EOL enrichment possible (no structured product/version split). No `?application=vault` filter. No autocomplete. The whole point of (1) is to make the data machine-readable — `notes` is the status quo we're trying to improve on.

### Auto-discover via in-guest agent in v1

- **ALT-005 Description:** Ship a small agent (or an SSH probe) as part of this ADR; the agent populates `applications` from `dpkg -l` / `rpm -qa` / running-process scan; the operator never types anything.
- **ALT-006 Rejection Reason:** Agent design is a substantial undertaking (auth, lifecycle, packaging, distro coverage, sandboxing) — out of scope for a feature whose primary value is curated inventory + EOL signal. ADR-0015 already lists this as FUT-002. Once it lands, it writes to the same column — the data model in this ADR is the contract the agent will implement against.

### Keep filters client-side in the SPA

- **ALT-007 Description:** The SPA already does some client-side filtering (role splitting). Just fetch all VMs and filter in the browser.
- **ALT-008 Rejection Reason:** Doesn't scale past one page (default 50 VMs) and breaks the cursor-pagination contract. Operators with a 5K-VM fleet would never see the bastion they're searching for. Server-side filtering is the right layer; the SPA already calls the existing server-side filters (region, cloud_account, power_state) and adding the new ones follows the same pattern.

### Substring filter on `role` (not just exact)

- **ALT-009 Description:** Make `role` substring like `name` and `image`.
- **ALT-010 Rejection Reason:** The `role` column carries an enumerated tag value; UIs (and dashboards) consume it as a category. Substring matching encourages typos becoming queries — `?role=dn` matching both `dns` and `dns-secondary` is a footgun for "show me all primary DNS hosts". Operators wanting "all DNS-flavoured roles" use the current comma-split client-side fallback or the autocomplete. Keep `role` exact; reconsider in FUT if the use case proves real.

### Unify nodes + virtual_machines into one inventory table

- **ALT-011 Description:** Run the new EOL pass over a unified "logical_servers" view. Drop the kube-node / VM split.
- **ALT-012 Rejection Reason:** ADR-0015 ALT-001/002 already considered and rejected this. Re-litigating it here would be scope creep. The EOL enricher just adds a third source; the existing two stay independent.

### Per-application JSONB key-style annotation

- **ALT-013 Description:** Instead of `longue-vue.io/eol.<product>`, store the EOL signal *inside* each application entry: `applications[].eol = {...}`.
- **ALT-014 Rejection Reason:** Splits the EOL UI by entity type — clusters/nodes use `longue-vue.io/eol.*` annotations, VMs use a different shape. The existing aggregator can keep one read path. Symmetry wins; the storage cost is identical.

## Implementation Notes

- **IMP-001** Migration `00028_add_vm_applications.sql`:
  ```sql
  ALTER TABLE virtual_machines
      ADD COLUMN applications JSONB NOT NULL DEFAULT '[]'::jsonb;

  CREATE INDEX virtual_machines_applications_gin_idx
      ON virtual_machines USING GIN (applications jsonb_path_ops);

  CREATE INDEX virtual_machines_name_lower_idx
      ON virtual_machines (LOWER(name));

  CREATE INDEX virtual_machines_image_id_idx
      ON virtual_machines (image_id);
  ```
  No migration of existing data (default `'[]'` is the correct empty starting point).

- **IMP-002** `internal/api/cloud_types.go`:
  - Add `Applications []VMApplication` to `VirtualMachine` and `Applications *[]VMApplication` to `VirtualMachinePatch`.
  - Define `VMApplication` struct (fields per §1).
  - Extend `VirtualMachineListFilter` with `Name *string`, `Image *string`, `CloudAccountName *string`, `Application *string`.

- **IMP-003** `internal/store/pg_virtual_machines.go`:
  - Update `vmColumns` to include `applications`.
  - Update `scanVirtualMachine` to scan the new JSONB column.
  - Update `UpsertVirtualMachine` to NOT write `applications` (preserve existing list across collector ticks; the collector path doesn't carry applications).
  - Update `UpdateVirtualMachine` to set `applications` from the patch when present. Validate the list (cap at 50, reject duplicates, normalize products) before serializing.
  - Extend `ListVirtualMachines` to wire the four new filters. Use the LIKE-escape helper from `pg_virtual_machines.go` (already in place for the dedup query).
  - When `cloud_account_name` is set without `cloud_account_id`, run an inner query `(SELECT id FROM cloud_accounts WHERE provider='outscale' AND name=$N)` rather than two round-trips.

- **IMP-004** `internal/api/virtual_machine_handlers.go`:
  - `HandleListVirtualMachines`: parse + validate the four new query params (length cap 100 chars, normalize the `application` param).
  - `HandlePatchVirtualMachine`: validate the `applications` array (cap, duplicate check, per-field length caps), normalize products, stamp `added_at` / `added_by` on new entries by diffing against `GetVirtualMachine` first.
  - New handler `HandleListVirtualMachineApplicationsDistinct` for the autocomplete: `GET /v1/virtual-machines/applications/distinct` with `read` scope, returns up to 200 distinct lower-cased products.

- **IMP-005** `internal/eol/`:
  - New matcher `GenericVersionMatcher` in `matcher.go`. Method `Match(product, version string) (MatchResult, bool)` — applies the same `^v?(\d+\.\d+)` regex to `version`, returns `MatchResult{Product: product, Cycle: matchedCycle}`.
  - New methods on `Enricher`: `enrichVirtualMachines(ctx)`, `enrichVirtualMachine(ctx, vm)`, `mergeVMAnnotation(ctx, id, product, ann)`.
  - Extend `EnricherStore` interface with the three new methods.
  - Wire `enrichVirtualMachines(ctx)` into the existing `enrich(ctx)` after the cluster/node passes.

- **IMP-006** OpenAPI spec — `api/openapi/openapi.yaml` is the source of truth for codegen. The VM endpoints are currently mounted as hand-written routes (not codegen) but the spec must still document them so consumers (MCP server, external clients, the doc generator) stay coherent. This ADR's implementation MUST add:
  - `paths./v1/virtual-machines.get` parameters: `name`, `image`, `cloud_account_name`, `application` (in addition to existing).
  - `paths./v1/virtual-machines/{id}.patch` request body schema: extend `VirtualMachinePatch` with `applications`.
  - `components.schemas.VirtualMachine`: add `applications`.
  - `components.schemas.VMApplication`: new schema.
  - Per the workflow rules, an OpenAPI validation test built on `pb33f/libopenapi-validator` MUST cover spec validity and request/response payloads for the new shapes.

- **IMP-007** UI (`ui/src/`):
  - New component `ApplicationsCard` in `ui/src/components/inventory/`. Read mode: read-only table. Edit mode: per-row editor with add/remove buttons; submits the full list via `api.patchVirtualMachine`.
  - Extend `VirtualMachineDetail.tsx` to mount `ApplicationsCard` between the labels card and the curated-metadata card.
  - Extend `VirtualMachines.tsx` with name / image / application filter inputs and a debounced `useResource` dependency list. Add the application autocomplete fetched once on mount.
  - Extend `EolDashboard.tsx` to consume VM annotations alongside cluster + node annotations and add a Type column + filter chip.
  - Extend `api.ts` with `VirtualMachineListFilter` field additions, the `applications` field on `VirtualMachine` and `VirtualMachinePatch`, and `listVirtualMachineApplicationsDistinct()`.

- **IMP-008** Tests:
  - **Unit (matcher):** `internal/eol/matcher_test.go` — `GenericVersionMatcher` table-driven cases (clean versions, version with leading `v`, malformed, empty).
  - **Unit (handlers):** `internal/api/virtual_machine_handlers_test.go` — name length cap (101 chars → 400), LIKE-escape (`name=%abc%` does not match every row), application normalization on PATCH, duplicate `(product,name)` rejection, max-50 cap, `added_at` / `added_by` stamping on new entries.
  - **Unit (store / pg fake):** the existing `cloudFake` in `server_cloud_fake_test.go` gains the new filter cases.
  - **Integration (EOL enricher):** extend `internal/eol/enricher_test.go` with a fake `endoflife.date` server returning canned cycles for `vault`, a VM with one app (`vault 1.13.4`), and assert `longue-vue.io/eol.vault` ends up on the VM's annotations with `eol_status=eol`. Cover the unknown-product stub case.
  - **Integration (store):** extend `internal/integration/integration_test.go` with VM application CRUD via PATCH + GIN-containment list filter.
  - **OpenAPI validation:** spec-level + request/response payload validation for `GET /v1/virtual-machines` (with new params) and `PATCH /v1/virtual-machines/{id}` (with `applications`).

- **IMP-009** Documentation deliverables (Phase 5):
  - New section in `docs/vm-collector.md` documenting the curated-applications field (operator workflow: PATCH the list, autocomplete behaviour, EOL enrichment).
  - `docs/api-reference.md`: extend the VM section with the new filters, the PATCH `applications` field, and the autocomplete endpoint.
  - `docs/eol-enrichment.md`: add a "VM applications" subsection alongside the existing cluster/node sections.
  - `CLAUDE.md`: extend the `virtual_machines` schema bullet with the `applications` JSONB column; extend the `internal/eol/` bullet with the VM pass.
  - `README.md`: ADR-0019 row in the index; small features-list addition ("Application inventory and EOL enrichment for platform VMs").
  - `CHANGELOG.md`: `Added` entry under the next minor version.

- **IMP-010** Helm charts: no chart-version changes for the collector / vm-collector / ingest-gw charts (they are unaffected). `charts/longue-vue/Chart.yaml` `version` and `appVersion` bump as part of the release.

## Future work

- **FUT-001** `pg_trgm` index on `name`, `display_name`, `image_id`, `image_name` if a deployment passes ~100K platform VMs.
- **FUT-002** In-guest agent / SSH probe that auto-populates `applications` (already listed in ADR-0015 FUT-002). The data model in this ADR is the contract.
- **FUT-003** Cross-entity EOL Inventory aggregation — a single page that shows kube nodes *and* platform VMs running the same outdated software (e.g. "Vault 1.13 — running on 2 nodes and 4 VMs").
- **FUT-004** CVE enrichment alongside EOL (referenced in ADR-0012 ALT). The same `applications[]` shape would carry CVE counts under a sibling annotation namespace (`longue-vue.io/cve.<product>`).
- **FUT-005** Curated `depends_on` edges from VMs to clusters / services (already listed in ADR-0015 FUT-003). Knowing "the bastion VM runs OpenSSH 8.4" + "the bastion is upstream of cluster X" is a natural input to impact analysis.
- **FUT-006** Bulk-edit applications across many VMs ("rename `hashicorp-vault` → `vault` everywhere") via an admin endpoint.

## References

- **REF-001** ADR-0001 — CMDB for SNC using Kubernetes — `docs/adr/adr-0001-cmdb-for-snc-using-kube.md`
- **REF-002** ADR-0008 — SecNumCloud chapter 8 asset management — `docs/adr/adr-0008-secnumcloud-chapter-8-asset-management.md`
- **REF-003** ADR-0012 — End-of-life enrichment via endoflife.date — `docs/adr/adr-0012-eol-enrichment-via-endoflife-date.md`
- **REF-004** ADR-0015 — VM collector for non-Kubernetes platform VMs (the `virtual_machines` schema, the `cloud_accounts` table, the `vm-collector` token scope, NEG-002 "EOL enricher is a no-op for VMs", FUT-002 in-guest agent) — `docs/adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md`
- **REF-005** endoflife.date API — https://endoflife.date/docs/api
- **REF-006** PostgreSQL JSONB indexing (`jsonb_path_ops` for `@>`) — https://www.postgresql.org/docs/current/datatype-json.html#JSON-INDEXING
- **REF-007** PostgreSQL `pg_trgm` (FUT-001) — https://www.postgresql.org/docs/current/pgtrgm.html
