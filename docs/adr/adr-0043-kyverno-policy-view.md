---
title: "ADR-0043: Kyverno policy inventory and Policies view"
status: "Accepted"
date: "2026-07-21"
authors: "Frederic CRABOUILLET"
tags: ["architecture", "decision", "collector", "ui", "kyverno", "policy"]
supersedes: ""
superseded_by: ""
---

# ADR-0043: Kyverno policy inventory and Policies view

## Status

**Accepted** | Proposed | Rejected | Superseded | Deprecated

- **Date:** 2026-07-21
- **Supersedes:** none
- **Superseded by:** none

## Context

Operators need visibility into Kyverno policy posture across all
catalogued clusters — which policies exist, where they apply, whether
they enforce or audit, and which resources pass or fail. Today,
longue-vue has no Kyverno awareness at all: the collector ignores
`ClusterPolicy`, `Policy`, `PolicyReport`, and `ClusterPolicyReport`
CRDs, and the UI has no page for them.

The SecNumCloud compliance evidence (SNC §8.3) increasingly requires
showing that admission-control and audit policies are consistently
deployed across environments. A dedicated Policies view lets the
operator compare policy posture per environment and per zone at a
glance. SNC evidence extracts are out of scope for this ADR; a
follow-up will add `GET /v1/cluster-policies/extract` (ADR-0037
precedent: cap, `X-Longue-Vue-Truncated`, audit-logged).

**Key constraints:**

- Kyverno is the sole admission/audit policy engine in scope (ADR-0001
  ecosystem). Other engines (OPA Gatekeeper, Kubewarden) are out of
  scope for v1.
- The collector lists resources cluster-wide (not per-namespace); the
  per-namespace fan-out was removed as a bugfix on 2026-06-10
  (rate-limiter exhaustion on large clusters, cf.
  `internal/collector/netpol_collector.go`). Kyverno collection follows
  the same cluster-wide pattern.
- The existing FK chain (`clusters` → `namespaces` → children) and
  reconcile pattern (ADR-0009, ADR-0033) apply directly: a policy is
  either cluster-scoped or namespace-scoped, and both live under a
  cluster.
- **Push-collector POST surface.** Two POST endpoints (`POST
/v1/cluster-policies`, `POST /v1/policy-reports`) are specified in
  §3b, gated by `requireScope(write)`. A `source` discriminator column
  (`'collector'` / `'api'`) on both tables allows the reconcile sweep
  (`DeleteClusterPoliciesByNamespace` / `DeletePolicyReportsByNamespace`)
  to target only collector-originated rows (`WHERE source=$3` with the
  `api.SourceCollector` constant), so that API-created rows survive
  reconcile ticks. This follows the ADR-0038 push-collector pattern.

## Decision

### 1. New database tables

Add two tables following the established pattern (cf. migration 00050
for `network_policies`):

**`cluster_policies`** — one row per collected Kyverno `ClusterPolicy`
or namespaced `Policy`. The table name is a historical artifact (it
predates this ADR's review); it stores both `ClusterPolicy` and
`Policy` rows, distinguished by `resource_type` / `scope`.

| Column              | Type                                         | Notes                                                                                                                                                                                                                                                                                 |
| ------------------- | -------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `id`                | UUID PK                                      | `gen_random_uuid()`                                                                                                                                                                                                                                                                   |
| `cluster_id`        | UUID FK → `clusters(id) ON DELETE CASCADE`   | owning cluster                                                                                                                                                                                                                                                                        |
| `namespace_id`      | UUID FK → `namespaces(id) ON DELETE CASCADE` | NULL for ClusterPolicy (cluster-scoped)                                                                                                                                                                                                                                               |
| `name`              | TEXT                                         | policy name                                                                                                                                                                                                                                                                           |
| `resource_type`     | TEXT                                         | `ClusterPolicy` or `Policy`                                                                                                                                                                                                                                                           |
| `scope`             | TEXT                                         | `cluster` or `namespace` (denormalised from `resource_type` for sort/filter)                                                                                                                                                                                                          |
| `description`       | TEXT                                         | from `metadata.annotations["policies.kyverno.io/description"]`; fallback: first non-empty line of `spec.rules[0].validate.message`                                                                                                                                                    |
| `category`          | TEXT                                         | from `metadata.annotations["policies.kyverno.io/category"]`                                                                                                                                                                                                                           |
| `severity`          | TEXT                                         | from `metadata.annotations["policies.kyverno.io/severity"]`; normalised to lowercase at ingestion (`critical` / `high` / `medium` / `low` / `info`)                                                                                                                                   |
| `action`            | TEXT                                         | `enforce` or `audit` — normalised to lowercase at ingestion (see §5)                                                                                                                                                                                                                  |
| `failure_policy`    | TEXT                                         | `Fail` or `Ignore` — from `spec.failurePolicy` (Kyverno ≤1.12) or `spec.webhookConfiguration.failurePolicy` (Kyverno ≥1.13); normalised to title-case at ingestion                                                                                                                    |
| `background`        | BOOLEAN                                      | from `spec.background` (default true)                                                                                                                                                                                                                                                 |
| `rule_types`        | TEXT[]                                       | distinct rule types across `spec.rules[]`: subset of `{validate, mutate, generate, verifyImages}`                                                                                                                                                                                     |
| `rules_count`       | INTEGER                                      | `len(spec.rules)`; NULL when 0 (absent spec.rules)                                                                                                                                                                                                                                    |
| `target_resources`  | TEXT[]                                       | distinct union of `spec.rules[].match.any[].resources.kinds[]` and `spec.rules[].match.all[].resources.kinds[]` — e.g. `{Pod, Deployment, Namespace}`                                                                                                                                 |
| `key_exclusions`    | TEXT[]                                       | distinct union of `spec.rules[].exclude.any[].resources.kinds[]`, `spec.rules[].exclude.all[].resources.kinds[]`, `spec.rules[].exclude.any[].resources.namespaces[]`, and `spec.rules[].exclude.all[].resources.namespaces[]` (capped at 10; prefixed: `kind:Pod`, `ns:kube-system`) |
| `ready`             | BOOLEAN                                      | tri-state: `true`/`false` from `status.conditions[type=="Ready"].status`; NULL when status or Ready condition absent                                                                                                                                                                  |
| `annotations`       | JSONB                                        | full annotations for drill-down (subject, scored, minversion, …)                                                                                                                                                                                                                      |
| `spec_raw`          | JSONB                                        | full spec for forensic access (mirrors `network_policies.spec_raw`)                                                                                                                                                                                                                   |
| `source`            | TEXT                                         | `'collector'` (set by in-process collector) or `'api'` (set by POST handler); NOT NULL, default `'collector'`; CHECK (`source IN ('collector', 'api')`)                                                                                                                               |
| `reconcile_seen_at` | TIMESTAMPTZ                                  | standard reconcile timestamp                                                                                                                                                                                                                                                          |

**FK behaviour:** `namespace_id` is nullable (NULL = cluster-scoped
policy) with `ON DELETE CASCADE`, matching the `network_policies`
migration 00050 pattern. On namespace soft-delete (the normal path in
longue-vue, per ADR-0021), `namespace_id` retains its value because the
row in `namespaces` is not physically removed. On hard-delete (cascade
from `DELETE /v1/clusters/{id}`), the policy rows are deleted with the
namespace — they have no meaning without their owning namespace.

UNIQUE on `(cluster_id, namespace_id, name)` — same composite key as
other namespaced entities; NULL `namespace_id` for cluster-scoped
policies. Because PostgreSQL treats NULL ≠ NULL in UNIQUE constraints
by default, a single expression-based unique index using COALESCE is
used to close that gap:

```sql
CREATE UNIQUE INDEX uq_cluster_policies_scope
  ON cluster_policies (cluster_id, COALESCE(namespace_id, '00000000-0000-0000-0000-000000000000'), name);
```

This treats NULL `namespace_id` as a sentinel UUID, ensuring that two
cluster-scoped policies with the same name in the same cluster violate
the constraint. Expression indexes can serve as `ON CONFLICT` arbiters
(unlike partial indexes with `WHERE`, which cannot).

**`policy_reports`** — one row per collected `PolicyReport` or
`ClusterPolicyReport`, carrying the summary counts and the results
array.

| Column              | Type                                         | Notes                                                                                               |
| ------------------- | -------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| `id`                | UUID PK                                      | `gen_random_uuid()`                                                                                 |
| `cluster_id`        | UUID FK → `clusters(id) ON DELETE CASCADE`   |                                                                                                     |
| `namespace_id`      | UUID FK → `namespaces(id) ON DELETE CASCADE` | NULL for ClusterPolicyReport                                                                        |
| `name`              | TEXT                                         | report object name                                                                                  |
| `scope_kind`        | TEXT                                         | `Namespace`, `Pod`, etc. from `scope.kind` (nullable)                                               |
| `scope_name`        | TEXT                                         | from `scope.name` (nullable)                                                                        |
| `summary_pass`      | INTEGER                                      |                                                                                                     |
| `summary_fail`      | INTEGER                                      |                                                                                                     |
| `summary_warn`      | INTEGER                                      |                                                                                                     |
| `summary_error`     | INTEGER                                      |                                                                                                     |
| `summary_skip`      | INTEGER                                      |                                                                                                     |
| `results_raw`       | JSONB                                        | full `results[]` array for drill-down                                                               |
| `source`            | TEXT                                         | `'collector'` or `'api'`; NOT NULL, default `'collector'`; CHECK (`source IN ('collector', 'api')`) |
| `reconcile_seen_at` | TIMESTAMPTZ                                  |                                                                                                     |

`policy_reports` uses the same COALESCE expression index pattern as
`cluster_policies` to handle NULL `namespace_id` for
ClusterPolicyReports:

```sql
CREATE UNIQUE INDEX uq_policy_reports_scope
  ON policy_reports (cluster_id, COALESCE(namespace_id, '00000000-0000-0000-0000-000000000000'), name);
```

### 2. Collector changes

Extend `internal/collector/` to list four Kyverno CRDs per tick, all
via **cluster-wide** list calls (not per-namespace, following the
current collector topology):

```text
ClusterPolicy           → cluster_policies   (cluster-wide list call)
Policy                  → cluster_policies   (cluster-wide list call; namespace_id set from .metadata.namespace)
PolicyReport            → policy_reports     (cluster-wide list call; namespace_id set from .metadata.namespace)
ClusterPolicyReport     → policy_reports     (cluster-wide list call; namespace_id NULL)
```

Collection is gated by a new setting `policies_enabled` (default
`false`), seeded from `LONGUE_VUE_POLICIES_ENABLED`. The gate is
checked **per tick** — the collector reads `GetSettings()` at the start
of each Kyverno ingestion cycle; when the setting is off, the tick is a
no-op and no Kubernetes API calls are made. Rationale: clusters without
Kyverno should not pay the API-round-trip cost; the setting also lets
operators opt in gradually. The setting is exposed at
`GET /v1/admin/settings` (same as all other feature toggles).

RBAC: add `clusterpolicies`, `policies`, `policyreports`,
`clusterpolicyreports` (all under `kyverno.io` and `wgpolicyk8s.io`
API groups) to the **in-process** ClusterRole only
(`charts/longue-vue`). The push-collector ClusterRole
(`charts/longue-vue-collector`) is **not** modified — push-collector
clusters send Kyverno data via the POST endpoints (§3b), not via
in-cluster K8s API calls, so they do not need Kyverno list RBAC.

Reconcile follows the established pattern (ADR-0009), adapted for
Kyverno's mixed scope (ADR-0033 per-namespace sweep for netpols is
the precedent). After each collection tick, two sweep phases run:

1. **Cluster-scoped sweep.**
   `DeleteClusterScopedPoliciesNotIn(cluster_id, seen_ids)` removes
   collector-originated `ClusterPolicy` / `ClusterPolicyReport` rows
   (`WHERE namespace_id IS NULL AND source='collector'`) not present
   in the latest tick.

2. **Per-namespace sweep.** For every namespace known to the cluster,
   `DeleteClusterPoliciesByNamespace(cluster_id, namespace_id, seen_ids)`
   and `DeletePolicyReportsByNamespace(cluster_id, namespace_id, seen_ids)`
   remove collector-originated namespaced rows not present in the latest
   tick. The `cluster_id` parameter is defense-in-depth — the PG FK chain
   already constrains rows to a single cluster, but including it in the
   WHERE clause (`AND cluster_id=$1`) prevents cross-cluster impact if a
   caller ever passes the wrong `namespace_id`. Namespaces that the
   collector did **not** visit (e.g., an unknown/orphan namespace from a
   previous tick) are **never swept**; their rows survive reconcile,
   preventing the collateral deletion bug where a cluster-wide sweep would
   delete namespaced policies in namespaces outside the `seen` set.

API-created rows (`source='api'`) are never deleted by reconcile.
The old `DeleteClusterPoliciesNotIn` / `DeletePolicyReportsNotIn`
methods have been removed from the `api.KyvernoStore` interface; the
collector's local `KyvernoStore` interface uses only the four-method
sweep surface (two cluster-scoped, two per-namespace). Source values
are referenced via the `api.SourceCollector` and `api.SourceAPI`
constants defined in `internal/api/store.go`.

### 3. REST API

New list and detail endpoints:

- **`GET /v1/cluster-policies`** — list with cursor pagination
- **`GET /v1/cluster-policies/{id}`** — single policy detail (needed for
  drill-down; mirrors `GET /v1/network-policies/{id}`)
- **`GET /v1/policy-reports`** — list with cursor pagination
- **`GET /v1/policy-reports/{id}`** — single report detail (critical for
  `policy_reports` given the large `results_raw` JSONB; the list
  endpoint omits `results_raw` from the response to bound row size)

Query parameters follow ADR-0042 (uniform list contract):

| Param            | Type   | Notes                                                                                                                                                      |
| ---------------- | ------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `limit`          | int    | cursor page size                                                                                                                                           |
| `cursor`         | string | tagged base64-JSON cursor                                                                                                                                  |
| `name`           | string | substring / glob filter on policy name (LIKE metacharacters literal per ADR-0042)                                                                          |
| `sort`           | string | allowlist: `name`, `resource_type`, `action`, `background`, `severity`, `rules_count`, `failure_policy`, `category`, `ready`, `scope`, `reconcile_seen_at` |
| `order`          | string | `asc` (default) / `desc`                                                                                                                                   |
| `cluster_id`     | UUID   | filter by cluster                                                                                                                                          |
| `namespace_id`   | UUID   | filter by namespace                                                                                                                                        |
| `resource_type`  | string | `ClusterPolicy` / `Policy`                                                                                                                                 |
| `action`         | string | `enforce` / `audit` (case-insensitive exact match; values normalised to lowercase at ingestion)                                                            |
| `failure_policy` | string | `Fail` / `Ignore` (case-insensitive exact match)                                                                                                           |
| `severity`       | string | `critical` / `high` / `medium` / `low` / `info` (case-insensitive exact match; values normalised to lowercase at ingestion)                                |
| `category`       | string | substring filter on category                                                                                                                               |

**Sort infrastructure notes:** `background`, `ready`, and
`rules_count` require extending the sort infrastructure beyond the
current `sortText` / `sortTime` cursor types. A new `sortInt` cursor
type handles boolean columns (via `::int` cast: 0/1) and integer
columns (`rules_count`). `severity` as TEXT sorts
alphabetically (`critical < high < low < medium`); a CASE-based rank
mapping in the sort helper (using `sortInt`) sorts by severity level.

Response: `{items: [...], next_cursor: "..." | null}` per ADR-0042.

The `policy-reports` endpoint follows the same contract with its own
sort/filter allowlist: `name`, `scope_kind`, `scope_name`,
`summary_pass`, `summary_fail`, `summary_warn`, `summary_error`,
`summary_skip`, `reconcile_seen_at`. The `cluster_policy_name` filter
is removed — since Kyverno 1.11, PolicyReports are named after the
evaluated resource's UID, not the policy name. To find reports for a
specific policy, use `GET /v1/policy-reports?cluster_id=<id>` and
filter client-side on `results_raw`, or query the non-conformity
sub-endpoint (§7).
Additional filters for policy-reports: `scope_kind` (exact match on the
evaluated resource kind, e.g. `Namespace`, `Pod`, `Deployment`) and
`scope_name` (substring/ILIKE match).

### 3b. POST & DELETE endpoints (push-collector surface)

Two POST endpoints allow external agents (push-collector apiclient,
automation tooling) to write Kyverno data into the CMDB:

- **`POST /v1/cluster-policies`** — upsert a ClusterPolicy or Policy row
- **`POST /v1/policy-reports`** — upsert a PolicyReport or ClusterPolicyReport row
- **`DELETE /v1/cluster-policies/{id}`** — delete an API-authored ClusterPolicy
- **`DELETE /v1/policy-reports/{id}`** — delete an API-authored PolicyReport

Both are gated by `requireScope(write)` (viewer token → 403) **and**
`settings.policies_enabled` (disabled → 409 Conflict). The gate aligns
with the GET endpoints and the collector's per-tick gate (§2).

**Ingest-gateway exclusion:** these POST routes are registered on the
_public_ listener only; they are **not** mounted on the DMZ ingest-gateway
(`ingest_mux.go` / `allowlist.go`). A push-collector operating through
the air-gapped ingestion path (REF-003) therefore **cannot** POST Kyverno
policies. This is intentional: the ingest-gateway exposes only the
existing workload/VM/NM write surface; Kyverno policy ingestion via the
DMZ is deferred (see NEG-008). A push-collector that needs to write
policies must reach the public listener directly.

**Request body types** — dedicated `ClusterPolicyCreate` and
`PolicyReportCreate` structs exclude server-generated fields (`id`,
`source`, `reconcile_seen_at`). Required fields are validated:

| Endpoint         | Required fields                                   | Validation                                                                                                                                                                            |
| ---------------- | ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| cluster-policies | `name`, `cluster_id`, `resource_type`, `spec_raw` | `resource_type` ∈ {`ClusterPolicy`, `Policy`}; `scope` ∈ {`cluster`, `namespace`} (defaults to `cluster`); `spec_raw` must be non-empty (DB NOT NULL); scope↔namespace_id rules below |
| policy-reports   | `name`, `cluster_id`                              | `scope_kind` normalised to title-case if provided                                                                                                                                     |

**Scope↔namespace_id consistency:**

- `resource_type=Policy` → `namespace_id` is required (namespaced policy
  must belong to a namespace; 400 if omitted).
- `resource_type=ClusterPolicy` → `namespace_id` must be omitted or null
  (cluster-scoped policy; 400 if provided).

**Case normalisation:** `action` and `severity` are lowercased by the
handler (matching the collector's ingestion-time normalisation in §5).
`failure_policy` is **title-cased** (`Fail` / `Ignore`) to match the
Kyverno CRD spec and the collector's raw K8s API output (§5).
`scope_kind` is stored **verbatim** — the collector writes K8s kinds
as-is (CamelCase, e.g. `ReplicaSet`) and normalising would split the
same kind across two spellings; the list filter compares
case-insensitively instead.

**Source discriminator:** the handler sets `source='api'` on every row
created via POST. The in-process collector sets `source='collector'`.
This allows the reconcile sweep to target only collector-originated rows
(`WHERE source='collector'`), ensuring API-created rows survive
reconcile ticks. The column is NOT NULL with a CHECK constraint and
defaults to `'collector'` (migration 00061).

**FK error handling:** the store's `UpsertClusterPolicy` and
`UpsertPolicyReport` methods classify PostgreSQL FK violation errors
(23503) and return `api.ErrNotFound`, which the handler maps to 422
Unprocessable Entity. This follows the established pattern in
`CreatePod` / `CreatePersistentVolumeClaim`.

**Response:** 201 Created with the full row (via GET read-back after
upsert). If the read-back fails (unlikely but possible), the handler
logs the error and returns 201 with a plain-text message indicating the
row was created but read-back failed — it never returns a bare
`{"id": "..."}` envelope.

**Collision semantics (same `cluster_id` + `namespace_id` + `name`):**

| Existing row source | Incoming request source  | Behaviour                                                                 | HTTP           |
| ------------------- | ------------------------ | ------------------------------------------------------------------------- | -------------- |
| `collector`         | `collector` (in-process) | Upsert (idempotent)                                                       | n/a (internal) |
| `api`               | `api`                    | Upsert (idempotent)                                                       | 201            |
| `collector`         | `api`                    | **Rejected** — collector-owned rows cannot be overwritten by API requests | 409            |
| `api`               | `collector` (in-process) | Upsert (collector wins)                                                   | n/a (internal) |

The collector→api rejection prevents a user POST from silently overwriting a
live cluster policy. The api→collector overwrite is allowed because the
collector's reconciliation must be able to update a stale API-authored row
when it discovers the same key in the live cluster.

**DELETE endpoints:** `DELETE /v1/cluster-policies/{id}` and
`DELETE /v1/policy-reports/{id}` remove API-authored rows (`source='api'`).
Collector-managed rows return 404 (the operator should not delete rows the
collector will re-create on the next tick). Both require `write` scope.

**Sweep durability guarantee:** rows created via POST (`source='api'`)
are never deleted by the collector's reconcile sweep (§2). The sweep
targets only `source='collector'` rows. This is a deliberate
architectural choice: API-authored rows represent operator intent that
should persist across collector cycles. If an operator needs to remove an
API-authored row, they must use the DELETE endpoint or REST API directly.

### 4. UI — Policies view

**Navigation:** insert a "Policies" entry in the sidebar between "Pods"
and "Services" in `ui/src/App.tsx`, using a new `PolicyIcon` SVG
component.

**Table columns** (sortable per the allowlist in §3; not "all sortable" —
computed columns are not sortable per ADR-0042 §4; column reorder via
drag-and-drop does not exist in the current UI infrastructure — only
resize via `resizable_columns.ts`):

| Column                  | Source                                                    | Link / rendering                                                                                                                                    |
| ----------------------- | --------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Name**                | `cluster_policies.name`                                   | Plain text (policy name)                                                                                                                            |
| **Resource Type**       | `cluster_policies.resource_type`                          | Badge (`ClusterPolicy` / `Policy`)                                                                                                                  |
| **Env**                 | `clusters.environment` (JOIN)                             | Plain text                                                                                                                                          |
| **Cluster**             | `clusters.id` + `clusters.name` (JOIN)                    | Link to `/ui/clusters/${cluster_id}` (relative route; no hardcoded hostname)                                                                        |
| **Zone**                | Derived from cluster (§6)                                 | Plain text                                                                                                                                          |
| **Category / Severity** | `cluster_policies.category` + `cluster_policies.severity` | Colour-coded badge using shared `status-ok`/`status-warn`/`status-bad` CSS classes: critical/high=bad, medium=warn, low/info=ok; category as prefix |
| **Rule Types**          | `cluster_policies.rule_types`                             | Pill badges: `validate`, `mutate`, `generate`, `verifyImages` — colour per type                                                                     |
| **Action**              | `cluster_policies.action`                                 | Pill badge (`enforce` / `audit`)                                                                                                                    |
| **Failure Policy**      | `cluster_policies.failure_policy`                         | Badge: red = `Fail` (request rejected on webhook error), grey = `Ignore`                                                                            |
| **Target Resources**    | `cluster_policies.target_resources`                       | Comma-separated list with overflow tooltip (e.g. `Pod, Deployment, Namespace`)                                                                      |
| **Key Exclusions**      | `cluster_policies.key_exclusions`                         | Comma-separated list with overflow tooltip; muted style (e.g. `ns:kube-system`); hover shows full list from `spec_raw` when capped                  |
| **Background Scan**     | `cluster_policies.background`                             | Boolean icon (checkmark / dash)                                                                                                                     |
| **Ready**               | `cluster_policies.ready`                                  | Status dot: green = `true`, red = `false`, grey = unknown                                                                                           |
| **P99 Latency**         | Prometheus proxy (§7)                                     | Numeric with ms unit; async cell populated via server-side proxy                                                                                    |
| **Block Rate**          | Prometheus proxy (§7)                                     | Requests/min blocked in Enforce mode; async cell                                                                                                    |
| **Error Rate**          | Prometheus proxy (§7)                                     | Errors/min during rule evaluation; async cell                                                                                                       |
| **Non-Conformity**      | PolicyReport (§7)                                         | Failing resources count from PolicyReport results; async cell                                                                                       |

**PolicyReport toggle:** a toggle button (show/hide) in the table
toolbar. When enabled, a nested row or expandable section under each
policy shows the associated `PolicyReport` / `ClusterPolicyReport`
entries with pass/fail/warn/error/skip counts and a link to the full
results. Default state: hidden (collapsed).

**Pagination:** cursor-based, following `EntityListPage<T>` (same as
every other list view). Column sort and column resize use the existing
`SortHeader` + `resizable_columns.ts` infrastructure.

**UI infrastructure gaps** (deferred from v1 — the base table ships
without these; IMP-009 scopes v1 to the base `EntityListPage` table):

- Nested/expandable rows (PolicyReport toggle) — no existing
  `EntityListPage` support.
- Toolbar slot for the toggle button — no existing pattern.
- Async cells that populate after a secondary fetch (Prometheus proxy)
  — no existing pattern; spinner while loading, "—" on error.

These are not "free reuse" — they are incremental shared infrastructure
to be estimated and delivered as part of the Policies feature.

### 5. How to retrieve Kyverno policies (collector logic)

The collector retrieves Kyverno policies using the Kubernetes API via
client-go dynamic or typed clients. The Kyverno CRDs are:

- **`ClusterPolicy`** (`kyverno.io/v1`, cluster-scoped, short: `cpol`)
- **`Policy`** (`kyverno.io/v1`, namespaced, short: `pol`)
- **`ClusterPolicyReport`** (`wgpolicyk8s.io/v1alpha2`, cluster-scoped, short: `cpolr`)
- **`PolicyReport`** (`wgpolicyk8s.io/v1alpha2`, namespaced, short: `polr`)

Per-tick retrieval (all cluster-wide):

```text
1. If policies_enabled is false → skip entirely.

2. Cluster-wide list calls (4 calls total per cluster):
   - GET /apis/kyverno.io/v1/clusterpolicies
     → map each item to a cluster_policies row (namespace_id = NULL, scope = "cluster")

   - GET /apis/kyverno.io/v1/policies
     → map each item to a cluster_policies row (namespace_id set from .metadata.namespace, scope = "namespace")

   - GET /apis/wgpolicyk8s.io/v1alpha2/clusterpolicyreports
     → map each item to a policy_reports row (namespace_id = NULL)

   - GET /apis/wgpolicyk8s.io/v1alpha2/policyreports
     → map each item to a policy_reports row (namespace_id set from .metadata.namespace)

 3. Upsert each row; reconcile after successful list (cluster-scoped and
    namespace-scoped separately, per §2).

    Collector metrics: `longue_vue_collector_upserted_total`,
    `longue_vue_collector_reconciled_total`,
    `longue_vue_collector_errors_total`, and
    `longue_vue_collector_last_poll_timestamp_seconds` are emitted per
    (cluster, resource) with resource labels `cluster_policies` and
    `policy_reports` — matching the existing collector metric pattern.
```

**Note on PolicyReport volume:** Since Kyverno 1.11, PolicyReports are
emitted per evaluated resource (not per policy). A cluster with 10^4–10^5
evaluated resources produces that many PolicyReport objects. The
collector must use paginated list calls (`limit` + `continue`) to avoid
timeout and memory exhaustion, and the upsert/reconcile must be
batch-efficient.

Field extraction from a `ClusterPolicy` / `Policy` object:

| CMDB column        | K8s field path                                                                                                                                                                                                                                                                                                        |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`             | `.metadata.name`                                                                                                                                                                                                                                                                                                      |
| `resource_type`    | `.kind` (`ClusterPolicy` / `Policy`)                                                                                                                                                                                                                                                                                  |
| `scope`            | `"cluster"` if kind=ClusterPolicy, `"namespace"` if kind=Policy                                                                                                                                                                                                                                                       |
| `description`      | `.metadata.annotations["policies.kyverno.io/description"]`; fallback: first non-empty line of `.spec.rules[0].validate.message`                                                                                                                                                                                       |
| `category`         | `.metadata.annotations["policies.kyverno.io/category"]`                                                                                                                                                                                                                                                               |
| `severity`         | `.metadata.annotations["policies.kyverno.io/severity"]`; **normalised to lowercase** at ingestion (`critical` / `high` / `medium` / `low` / `info`)                                                                                                                                                                   |
| `action`           | Derived from the spec as follows (see action derivation rules below)                                                                                                                                                                                                                                                  |
| `failure_policy`   | `.spec.failurePolicy` (Kyverno ≤1.12, primary source) or `.spec.webhookConfiguration.failurePolicy` (Kyverno ≥1.13); normalised to title-case (`Fail` / `Ignore`) at ingestion                                                                                                                                        |
| `background`       | `.spec.background` (boolean, default true when absent)                                                                                                                                                                                                                                                                |
| `rule_types`       | distinct set of rule types present in `.spec.rules[]`: keys `validate`, `mutate`, `generate`, `verifyImages` — stored as TEXT[]                                                                                                                                                                                       |
| `rules_count`      | `len(.spec.rules)`; NULL when 0 (absent spec.rules)                                                                                                                                                                                                                                                                   |
| `target_resources` | distinct union of `.spec.rules[].match.any[].resources.kinds[]` and `.spec.rules[].match.all[].resources.kinds[]`                                                                                                                                                                                                     |
| `key_exclusions`   | distinct union of `spec.rules[].exclude.any[].resources.kinds[]` (prefixed `kind:`), `spec.rules[].exclude.all[].resources.kinds[]` (prefixed `kind:`), `spec.rules[].exclude.any[].resources.namespaces[]` (prefixed `ns:`), and `spec.rules[].exclude.all[].resources.namespaces[]` (prefixed `ns:`) — capped at 10 |
| `ready`            | tri-state: `true` if `status.conditions[type=="Ready"].status == "True"`; `false` if Ready condition exists with non-"True" status; `NULL` if status or Ready condition absent                                                                                                                                        |
| `annotations`      | `.metadata.annotations` (full map as JSONB)                                                                                                                                                                                                                                                                           |
| `spec_raw`         | `.spec` (full spec as JSONB)                                                                                                                                                                                                                                                                                          |

**Action derivation rules:**

`spec.validationFailureAction` is always a string in all Kyverno
versions. There is no object form with an `.action` sub-field.

- Kyverno ≤1.12: `spec.validationFailureAction` is the sole source.
  Values may be `"Enforce"`, `"Audit"`, `"enforce"`, or `"audit"`
  (mixed case across versions). **Normalise to lowercase** (`enforce`
  / `audit`) at ingestion.
- Kyverno ≥1.13: `spec.validationFailureAction` is deprecated. The
  authoritative source is the per-rule
  `spec.rules[].validate.failureAction`, which can differ across rules
  within the same policy. `spec.validationFailureActionOverrides`
  allows namespace-level overrides. **Aggregation rule for the `action`
  column:** if any rule has `Enforce` (case-insensitive — including
  per-entry `verifyImages[].failureAction`/`failureActionOverrides`),
  the policy-level `action` is `enforce`; otherwise `audit` for any
  policy with validation semantics (`validate` or `verifyImages`
  rules). A mutate-/generate-only policy has no enforcement posture and
  the column is NULL — reporting the kubebuilder default `audit` there
  would fabricate one. This is a conservative display choice —
  operators see `enforce` if any rule blocks. The per-rule breakdown is
  available in `spec_raw` for drill-down.

Field extraction from a `PolicyReport` / `ClusterPolicyReport`:

| CMDB column     | K8s field path                   |
| --------------- | -------------------------------- |
| `name`          | `.metadata.name`                 |
| `scope_kind`    | `.scope.kind` (nullable)         |
| `scope_name`    | `.scope.name` (nullable)         |
| `summary_pass`  | `.summary.pass`                  |
| `summary_fail`  | `.summary.fail`                  |
| `summary_warn`  | `.summary.warn`                  |
| `summary_error` | `.summary.error`                 |
| `summary_skip`  | `.summary.skip`                  |
| `results_raw`   | `.results` (full array as JSONB) |

### 6. Zone derivation

The "Zone" column is not stored as a first-class field on policies. It
is derived at read time from the cluster's identity:

- **Contract:** if `clusters.annotations` contains a `"zone"` key, use
  that value. This is the authoritative source.
- **Default heuristic:** when no annotation is set, parse the cluster
  name using the convention `<platform>-<env>-std-<zone>` (e.g.,
  `zex-prod-std-main` → zone `main`, `zex-prod-std-dmz` → zone `dmz`).
  The zone is the fourth hyphen-delimited segment when the name matches
  the pattern `*-std-*`. Clusters that do not match fall back to an
  empty zone.

This heuristic is deployment-specific and may not cover all
KUBECONFIG contexts. The annotation is the contract; the name parser is
a convenience fallback.

### 7. Prometheus-derived policy metrics and non-conformity

Three columns in the Policies table are derived from Kyverno's
Prometheus metrics, and one from PolicyReport data. All four are
**fetched via the server-side Prometheus proxy** (IMP-008); the UI never
queries Prometheus directly. They are not stored in PostgreSQL and are
therefore not sortable server-side (ADR-0042 §4: computed columns are
not sortable).

A new setting `policy_prometheus_url` (default: empty = disabled)
enables the Prometheus integration. The setting is exposed at
`GET /v1/admin/settings`. When empty, the three Prometheus columns
render as "—"; the Non-Conformity column is always available (it reads
from `policy_reports` in PostgreSQL).

| Metric column      | Source                                                       | Type      | PromQL                                                                                                                                                                  |
| ------------------ | ------------------------------------------------------------ | --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **P99 Latency**    | `kyverno_policy_execution_duration_seconds`                  | Histogram | `histogram_quantile(0.99, sum(rate(kyverno_policy_execution_duration_seconds_bucket{policy_name=~"p1\|p2\|…"}[5m])) by (le, policy_name))`                              |
| **Block Rate**     | `kyverno_policy_results_total`                               | Counter   | `sum(rate(kyverno_policy_results_total{rule_result="fail", policy_validation_mode="enforce", rule_execution_cause="admission_request", policy_name=~"p1\|p2\|…"}[5m]))` |
| **Error Rate**     | `kyverno_policy_results_total`                               | Counter   | `sum(rate(kyverno_policy_results_total{rule_result="error", rule_execution_cause="admission_request", policy_name=~"p1\|p2\|…"}[5m]))`                                  |
| **Non-Conformity** | PolicyReport `results_raw` via JSONB (CMDB `policy_reports`) | DB read   | See below                                                                                                                                                               |

**Non-Conformity calculation:** Since Kyverno 1.11, PolicyReports are
named after the evaluated resource's UID (not the policy name), and
`results[].policy` holds the policy name. The Non-Conformity count for a
given policy is computed by:

```sql
SELECT COALESCE(SUM(CASE WHEN r->>'result' = 'fail' THEN 1 ELSE 0 END), 0)
FROM policy_reports pr,
     jsonb_array_elements(pr.results_raw) AS r
WHERE pr.cluster_id = $1
  AND r->>'policy' = $2;
```

A sequential scan over `results_raw` satisfies this query at read time.
If measured performance warrants, a GIN index on `results_raw` (via
`jsonb_path_ops`) or a precomputed `policy_report_policy_summary` table
can be added at ingestion time to avoid the JSONB scan at read time;
this optimisation is deferred.

**Key design decisions:**

- **Why not store metrics in PostgreSQL?** Prometheus counters are
  cumulative and high-cardinality; storing them in the CMDB would
  duplicate time-series data and require a polling enricher. The
  proxy-time fetch keeps the CMDB a CMDB and lets Prometheus handle
  rate/histogram math natively.
- **Why is non-conformity from PolicyReport, not Prometheus?** The
  PolicyReport `results[].result = "fail"` is a point-in-time count of
  non-compliant resources — exactly what the operator needs. The
  Prometheus counter
  `kyverno_policy_results_total{rule_result="fail"}` is cumulative and
  requires a window function to approximate; the PolicyReport is the
  authoritative source for current compliance state.
- **Batch fetch strategy:** the UI sends a single request to the proxy
  endpoint `GET /v1/cluster-policies/metrics` per page load. The proxy
  constructs a single PromQL query per metric type with a regex selector
  `policy_name=~"p1|p2|…"` covering all visible policies. The response
  is indexed by `policy_name` label and merged into the table rows
  client-side. The UI does **not** query Prometheus directly — the
  proxy avoids exposing Prometheus auth to the browser.
- **Cluster correlation:** Kyverno metrics are emitted by the Kyverno
  controller Pod. The Prometheus datasource is typically cluster-local
  (Thanos/Mimir federated). When `policy_prometheus_url` points to a
  centralised Thanos, the `cluster` label (or equivalent) is used to
  correlate metrics with CMDB clusters. If no cluster label exists,
  the metrics are shown without per-cluster breakdown (aggregate only).
- **Proxy auth and SSRF posture:** the proxy endpoint requires
  `requireReadScope` (consistent with all other GET endpoints). The
  `policy_prometheus_url` is an admin-configured setting (not
  user-supplied input); HTTPS validation is deferred — the setting
  stores the URL as-is. The proxy does not follow redirects.

**Kyverno `kyverno_policy_results_total` label reference:**
(verified against `pkg/metrics/common_types.go` and
`pkg/metrics/policy_engine.go` in kyverno/kyverno main branch)

| Label                        | Values                                                                                                                             |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `policy_name`                | policy name                                                                                                                        |
| `policy_namespace`           | namespace (`"-"` for ClusterPolicy)                                                                                                |
| `policy_validation_mode`     | `"enforce"`, `"audit"` (lowercase)                                                                                                 |
| `policy_type`                | `"cluster"`, `"namespaced"` (lowercase)                                                                                            |
| `policy_background_mode`     | `"true"`, `"false"`                                                                                                                |
| `rule_name`                  | rule name within the policy                                                                                                        |
| `rule_result`                | `"pass"`, `"fail"`, `"warn"`, `"error"`, `"skip"` (lowercase)                                                                      |
| `rule_type`                  | `"validate"`, `"mutate"`, `"generate"`, `"imageVerify"` (lowercase; `imageVerify` is a distinct value, not folded into `validate`) |
| `rule_execution_cause`       | `"admission_request"`, `"background_scan"` (lowercase)                                                                             |
| `resource_kind`              | e.g. `"Pod"`, `"Deployment"`                                                                                                       |
| `resource_namespace`         | namespace of the audited resource                                                                                                  |
| `resource_request_operation` | `"create"`, `"update"`, `"delete"` (lowercase)                                                                                     |
| `dry_run`                    | `"true"`, `"false"`                                                                                                                |

**Metric naming convention:** Kyverno uses OpenTelemetry for
instrumentation. The counter is registered as `kyverno_policy_results`
in the OTel SDK; the Prometheus exporter appends `_total` automatically,
so the scraped metric name is `kyverno_policy_results_total`. The
histogram is registered as `kyverno_policy_execution_duration_seconds`
(and scraped with the same name, as OTel does not append suffixes to
histograms).

## Consequences

### Positive

- **POS-001**: Operators gain a single-pane view of Kyverno policy
  posture across all environments and zones — no more per-cluster
  `kubectl get cpol` runs.
- **POS-002**: The PolicyReport toggle surfaces compliance results
  (pass/fail counts) alongside policy definitions without cluttering the
  default view.
- **POS-003**: Case normalisation at ingestion (`action`, `severity`
  → lowercase; `failure_policy` → title-case; `scope_kind` → title-case)
  makes filtering reliable regardless of Kyverno version — the DB always
  stores canonical values.
- **POS-004**: The cluster link (to `/ui/clusters/<uuid>`) keeps the
  existing navigation pattern consistent with other list views; no
  hardcoded hostname.
- **POS-005**: Gating collection behind `policies_enabled` avoids API
  round-trip cost on clusters without Kyverno and allows incremental
  rollout.
- **POS-006**: Reusing the ADR-0042 contract (cursor pagination, sort,
  name filter) means zero UI-level pagination/sort rewrite — the
  `EntityListPage<T>` component is reused for the base table. Nested
  rows, toolbar slots, and async cells are incremental shared
  infrastructure (§4).
- **POS-007**: Storing `spec_raw` and `results_raw` as JSONB mirrors the
  `network_policies.spec_raw` pattern and supports future drill-down
  pages without schema changes.
- **POS-008**: Prometheus-derived columns (p99 latency, block rate,
  error rate) give operators runtime observability alongside the
  static policy definition — combining inventory and telemetry in one
  view without duplicating time-series data in PostgreSQL.
- **POS-009**: `failure_policy` surfaces the webhook failure mode
  (`Fail` vs `Ignore`), which is critical for understanding blast
  radius: a policy with `Fail` blocks the entire admission request on
  webhook error, while `Ignore` silently passes it.
- **POS-010**: `rule_types` lets operators filter policies by what they
  do (validate, mutate, generate, verifyImages) — essential for
  understanding the admission-control surface at a glance.
- **POS-011**: `target_resources` and `key_exclusions` provide immediate
  visibility into what a policy selects and what it exempts, without
  needing to open the full spec.
- **POS-012**: The `source` discriminator prevents reconcile sweeps from
  deleting API-created rows, following the ADR-0038 pattern and avoiding
  the anti-pattern of silent data loss for push-collected clusters.

### Negative

- **NEG-001**: Four additional cluster-wide list calls per tick
  increase collector API surface. Mitigated by the `policies_enabled`
  gate and by the cluster-wide approach (same as all other resources).
- **NEG-002**: Zone derivation from cluster name is convention-dependent.
  If a cluster's name does not follow `<platform>-<env>-std-<zone>`, the
  zone column is empty. Mitigated by the `annotations["zone"]`
  fallback (the contract), with the name parser as a default heuristic.
- **NEG-003**: `ClusterPolicy` and `Policy` share the same table
  (`cluster_policies`), distinguished by `resource_type` / `scope`. A
  cluster-scoped `ClusterPolicy` has `namespace_id = NULL`; a
  namespace-scoped `Policy` has `namespace_id` set to its namespace UUID.
  Therefore a `ClusterPolicy` named `foo` and a `Policy` named `foo` in
  namespace `bar` have different `(cluster_id, namespace_id, name)` tuples
  and coexist without collision. For duplicate cluster-scoped names
  (same name, same cluster, both with `namespace_id = NULL`), the
  standard UNIQUE constraint would not catch the violation because
  PostgreSQL treats NULL ≠ NULL. A single COALESCE expression-based
  unique index (`uq_cluster_policies_scope`) closes this gap by mapping
  NULL `namespace_id` to a sentinel UUID, enforcing uniqueness correctly
  for both scopes. The same pattern applies to `policy_reports`
  (`uq_policy_reports_scope`).
- **NEG-004**: The `description` field relies on a Kyverno annotation
  convention (`policies.kyverno.io/description`). Policies that omit
  this annotation fall back to the first non-empty line of
  `spec.rules[0].validate.message`, which is a best-effort heuristic
  and may produce unexpected text for policies whose first rule is not
  a validation rule.
- **NEG-005**: Prometheus-derived columns (p99 latency, block rate,
  error rate) are fetched via the server-side proxy and are not sortable
  server-side. When Prometheus is unavailable, these columns render as
  "—". The proxy fetch adds latency to the table load (one proxy request
  per page). Mitigated by the batch fetch strategy (single vector query
  per metric type) and by the graceful "—" fallback.
- **NEG-006**: `failure_policy` uses `spec.failurePolicy` as the primary
  source on Kyverno ≤1.12. On Kyverno ≥1.13, the authoritative source
  is `spec.webhookConfiguration.failurePolicy`. If neither field is
  present, the column renders as empty. In practice, Kyverno always
  sets a default (`Fail`), but the edge case is documented.
- **NEG-007**: `key_exclusions` is capped at 10 entries to bound the
  array size. Policies with more than 10 exclusion rules will show a
  truncated list with a "+N more" indicator. The full exclusion list is
  available in `spec_raw` and rendered in a hover tooltip on the
  "Key Exclusions" cell — so operators see the cap in the cell and the
  complete list on mouse-over without opening a detail page.
- **NEG-008**: The push-collector apiclient methods (batch upsert,
  reconcile) are not yet implemented. The POST endpoints exist and are
  functional, but the apiclient's KyvernoStore implementation is
  deferred to a follow-up. Push-collected clusters can use the POST
  endpoints directly if needed, but the convenience methods are not
  available yet.
- **NEG-009**: The `action` column is a single-value summary. When a
  policy has mixed enforce/audit rules (Kyverno ≥1.13), the column
  shows `enforce` if any rule enforces — a conservative display. The
  per-rule breakdown is available in `spec_raw`.
- **NEG-010**: PolicyReport volume in Kyverno 1.11+ (one report per
  evaluated resource) can reach 10^4–10^5 rows per cluster. The
  collector must use paginated list calls, and the `results_raw` JSONB
  column is omitted from the list endpoint response to bound row size.
  The detail endpoint `GET /v1/policy-reports/{id}` returns the full
  `results_raw`.
- **NEG-011**: The table name `cluster_policies` stores both
  `ClusterPolicy` and `Policy` rows, which is potentially confusing.
  Renaming to `kyverno_policies` was considered but rejected to avoid
  a migration on the already-landed schema slot; the `resource_type`
  discriminator and §1 naming note make the semantics clear.

## Alternatives Considered

### Separate tables for ClusterPolicy and Policy

- **ALT-001**: **Description**: Create `cluster_policies` for
  `ClusterPolicy` only and `namespace_policies` for `Policy`, mirroring
  how clusters and namespaces are separate tables.
- **ALT-002**: **Rejection Reason**: The two Kyverno CRDs share
  nearly the same spec schema (the namespaced `Policy` has some
  restrictions on cross-namespace references that `ClusterPolicy` does
  not). A single table with a `resource_type` discriminator is simpler
  to query, paginate, and display in one unified table view. The UI
  needs a single list endpoint, not two.

### Collect PolicyReport results as individual rows

- **ALT-003**: **Description**: Instead of storing `results_raw` as
  JSONB, normalise each `PolicyReportResult` into its own row in a
  `policy_report_results` table with columns for `policy`, `rule`,
  `result`, `severity`, `resource_kind`, `resource_name`,
  `resource_namespace`.
- **ALT-004**: **Rejection Reason**: In Kyverno 1.11+, there are
  ~10^4–10^5 reports per cluster, each with multiple results.
  Normalising into individual rows would produce 10^5–10^6 rows per
  cluster and complicate reconciliation. JSONB storage lets the UI
  render summary counts cheaply and lazy-load individual results on
  demand (via the detail endpoint). If per-result querying becomes
  necessary, a SQL view over `jsonb_array_elements(results_raw)` can
  be added later.

### Real-time Kyverno API proxy instead of collection

- **ALT-005**: **Description**: Instead of collecting and storing
  policies in PostgreSQL, proxy live Kyverno API calls from the UI
  through longue-vue to each cluster's Kubernetes API.
- **ALT-006**: **Rejection Reason**: Violates the CMDB's core design
  principle (ADR-0001): longue-vue is a cache/snapshot, not a proxy.
  It must work when clusters are unreachable (air-gapped, VPN-down).
  The collector model preserves historical data and supports the
  `reconcile_seen_at` staleness signal.

### MCP-only exposure (no UI)

- **ALT-007**: **Description**: Expose `list_cluster_policies` and
  `get_cluster_policy` MCP tools only, without a UI page.
- **ALT-008**: **Rejection Reason**: The ADR-0006 rationale (UI for
  audit and curated metadata) applies directly: operators triage policy
  posture visually, not via API calls. The MCP tools will be added as
  a follow-up, but the UI is the primary interface.

## Implementation Notes

- **IMP-001**: Migration `00059_create_cluster_policies.sql` creates
  `cluster_policies` and `policy_reports` tables with indexes on
  `(cluster_id)`, `(namespace_id)`, and COALESCE expression-based
  unique indexes on both tables (`uq_cluster_policies_scope` and
  `uq_policy_reports_scope`) to correctly enforce uniqueness with NULL
  `namespace_id` semantics. GIN indexes on `rule_types`,
  `target_resources`, and `results_raw` were initially included but
  removed — no consumer queries these arrays/JSONB via containment or
  path operators. The non-conformity query (§7) can be satisfied by a
  sequential scan over `results_raw` at read time; a GIN index or
  precomputed summary table can be added when measured performance
  warrants it.
- **IMP-002**: Add `policies_enabled` boolean to the `settings` table
  (migration `00060`). Seed from `LONGUE_VUE_POLICIES_ENABLED`. Expose
  at `GET /v1/admin/settings`.
- **IMP-003**: *Deferred to the metrics follow-up.* The
  `policy_prometheus_url` setting ships together with the Prometheus
  proxy that consumes it (IMP-008): landing the column ahead of its
  consumer means an admin-set value silently does nothing.
- **IMP-004**: Collector: add `ingestClusterPolicies` and
  `ingestPolicyReports` methods in `internal/collector/`. Gate behind
  `policies_enabled`. Add the four Kyverno CRDs to the in-process
  ClusterRole template only (`charts/longue-vue`). **Do not** modify
  the push-collector ClusterRole (`charts/longue-vue-collector`).
  Normalise `action` (lowercase), `severity` (lowercase), and
  `failure_policy` (title-case) during the K8s list→row mapping.
  Use paginated list calls (`limit` + `continue`) for PolicyReports.
- **IMP-005**: Store: `internal/store/pg_cluster_policies.go` —
  `UpsertClusterPolicy`, `DeleteClusterScopedPoliciesNotIn`,
  `DeleteClusterPoliciesByNamespace`,
  `ListClusterPolicies` (with ADR-0042 sort/filter/pagination),
  `GetClusterPolicy` (detail endpoint).
  `internal/store/pg_policy_reports.go` — same pattern for reports,
  with `GetPolicyReport` detail endpoint. `ListPolicyReports` omits
  `results_raw` from the SELECT to bound response size.
  Extend sort infrastructure: add `sortInt` cursor type
  for `background` (via `::int` cast), `ready` (via `::int` cast),
  `rules_count`, and CASE-based severity rank mapping.
  All sweep methods use parameterized `api.SourceCollector` for the
  source discriminator and include `cluster_id` in the WHERE clause
  for defense-in-depth.
- **IMP-006**: OpenAPI: add `/v1/cluster-policies`, `/v1/cluster-policies/{id}`,
  `/v1/policy-reports`, `/v1/policy-reports/{id}` endpoints to
  `api/openapi/openapi.yaml` with the shared params (Limit, Cursor,
  NameFilter, SortKey, SortOrder) plus entity-specific filters. Workflow:
  add `exclude-operation-ids` in `oapi-codegen.yaml`, then `make generate`,
  `make swagger-sync`. (There is no `api.ts` codegen; the UI fetch wrapper
  is hand-written.)
- **IMP-007**: API handlers: `internal/api/cluster_policy_*.go` and
  `internal/api/policy_report_*.go` — hand-written handlers per the
  established pattern (cf. `internal/api/application_*`).
- **IMP-008**: *Deferred to the metrics follow-up (with IMP-003 and the
  IMP-009 async cells).* Prometheus proxy: add a server-side proxy endpoint
  `GET /v1/cluster-policies/metrics` (requires `read` scope, consistent
  with all other GET endpoints) that forwards batched PromQL queries to
  the configured `policy_prometheus_url` and returns the result indexed
  by policy name. The UI calls this endpoint once per page load; it
  never queries Prometheus directly. HTTPS validation of
  `policy_prometheus_url` is deferred; the proxy does not follow redirects
  (SSRF posture).
- **IMP-009**: UI: add `PolicyIcon` to `ui/src/icons.tsx`. Add
  `<NavLink>` entries for `/cluster-policies` and `/policy-reports` in
  `ui/src/App.tsx`. Add `ClusterPolicies()` and `PolicyReports()`
  functions in `ui/src/pages/Policies.tsx` with column definitions
  matching the table spec above. The Policies page uses the shared
  `EntityListPage<T>` component with an `errorRenderer` prop that
  catches 409 (feature disabled) and renders a banner linking to the
  admin settings page. Shared infrastructure work for v1 is limited
  to the base table; nested/expandable rows, toolbar slots, and async
  cells (Prometheus metrics) are deferred to a follow-up:
  (a) nested/expandable rows for the PolicyReport toggle, (b) toolbar
  slot for the toggle button, (c) async cells that populate after the
  metrics proxy responds; they display a spinner while loading and "—"
  on error or when Prometheus is not configured.
- **IMP-010**: Zone derivation: add a `clusterZone(cluster)` helper
  (shared between API response rendering and UI) that checks
  `annotations["zone"]` first (the contract), then falls back to the
  `<platform>-<env>-std-<zone>` name heuristic.
- **IMP-011**: MCP follow-up: add `list_cluster_policies`,
  `get_cluster_policy`, `list_policy_reports` tools to
  `internal/mcp/` — separate PR after the UI lands.
- **IMP-012**: Reconcile: implement cluster-level reconcile —
  cluster-scoped and namespace-scoped policies/reports swept together
  after the cluster-wide list via `DeleteClusterScopedPoliciesNotIn` /
  `DeleteClusterScopedPolicyReportsNotIn` (cluster-scoped) and
  `DeleteClusterPoliciesByNamespace` /
  `DeletePolicyReportsByNamespace` (per-namespace, with `cluster_id`
  defense-in-depth). The `seen` slice is initialised as
  `make([]uuid.UUID, 0)` so that a successful but empty tick sweeps
  all rows (consistent with the established sweep contract).
- **IMP-013**: Tests: unit tests for action derivation logic (case
  normalisation, mixed enforce/audit aggregation), store integration
  tests for COALESCE expression indexes with NULL namespace_id, and API
  handler tests for sort/filter allowlist enforcement.

## References

- **REF-001**: ADR-0001 — CMDB for SNC using Kubernetes
- **REF-002**: ADR-0005 — Multi-cluster collector topology
- **REF-003**: ADR-0009 — Push collector for air-gapped clusters
- **REF-004**: ADR-0042 — Uniform list search & sort contract
- **REF-005**: ADR-0033 — Collector chart RBAC profiles
- **REF-006**: ADR-0038 — NetworkPolicy push surface (anti-pattern: RBAC without write path)
- **REF-007**: ADR-0029 — First-class Application entity (extract evidence precedent)
- **REF-008**: ADR-0037 — Flow matrix evidence (extract cap + truncation + audit)
- **REF-009**: Kyverno CRD API reference — https://kyverno.io/docs/reference/crd/
- **REF-010**: PolicyReport CRD (wgpolicyk8s.io) — https://github.com/kubernetes-sigs/wg-policy-prototypes/tree/master/crd
- **REF-011**: Kyverno Prometheus metrics — https://kyverno.io/docs/monitoring/
- **REF-012**: `kyverno_policy_results` metric source — https://github.com/kyverno/kyverno/blob/main/pkg/metrics/policy_engine.go
- **REF-013**: Kyverno metrics types — https://github.com/kyverno/kyverno/blob/main/pkg/metrics/common_types.go
