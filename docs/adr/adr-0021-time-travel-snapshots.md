---
title: "ADR-0021: Time-travel snapshots for SecNumCloud asset history"
status: "Accepted"
date: "2026-05-04"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "cmdb", "snc", "time-travel", "history", "audit"]
supersedes: ""
superseded_by: ""
---

# ADR-0021: Time-travel snapshots for SecNumCloud asset history

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-05-04
- **Supersedes:** none
- **Superseded by:** none

## Context

ADR-0001 §IMP-005(d) listed *snapshot and versioning strategy* as a
follow-up decision; §IMP-003 already promised that "each collection
produces a versioned snapshot so history and diffs can be
reconstructed". That promise has never been kept. The CMDB as of
`v0.12.x` is strictly a current-state mirror: every entity table
carries `created_at` and `updated_at` only, the collector reconcile
loop hard-deletes rows that disappear from a cluster (per the
`LONGUE_VUE_COLLECTOR_RECONCILE=true` default), and the only
historical record of *anything* is `audit_events` — which tracks who
hit which endpoint with what body, not what the resulting row looked
like before or after.

That gap shows up in three concrete operational and compliance
questions longue-vue cannot answer today:

1. **"What did cluster `prod-eu-1` look like on 2026-02-15?"** —
   point-in-time cartography. SNC v3.2 chapter 8 (*Gestion des
   actifs*, see ADR-0008) requires a maintained inventory; an
   assessor reading a 2026-Q1 incident report typically asks "show me
   the inventory **as it stood when the incident started**", not
   "show me today's". Today the only honest answer is "we don't
   keep that".
2. **"Was pod `payment-svc-7d8f` alive on 2026-03-04T14:22Z?"** —
   forensic reconstruction. The reconcile loop deletes the row the
   moment the pod is reaped; even the workload-level timeline
   ("when did the deployment first appear, when did its replica
   count change?") is gone unless a human happened to PATCH a
   curated field through `audit_events` in the right window.
3. **"What changed on workload `web-frontend` between 2026-04-01 and
   2026-05-01?"** — change-set diffs for change-management evidence
   under SNC chapter 12 (*Conduite du changement*). `audit_events`
   captures human PATCHes (with field diffs in `details`) but not
   collector-driven changes — the collector writes via the DMZ
   ingest gateway, which does land in `audit_events` since
   ADR-0016, but the row volume (one event per resource per tick
   per cluster) and the lack of a structured "before / after"
   shape make it unfit as the primary historical source.

ADR-0008 §ALT-007 explicitly punted classification history into
"future snapshots / time-travel work (ADR-0001 roadmap)". This is
that ADR.

The framing constraints, decided via the brainstorming pass that led
to this ADR:

- **Scope is the SNC-relevant durable kinds, not pods.** Pods churn
  every minute under normal cluster operation; including them would
  drown the history tables in noise (200 pods × 1-min poll × 365 d
  ≈ 100 M pod-history rows per cluster per year before any
  change-filter), without serving the cartography use case ADR-0008
  is anchored on. Pod-level forensics is a follow-up problem that
  belongs in observability tooling (Loki, Tempo), not in the CMDB.
- **Storage shape is per-kind sidecar tables**, not bi-temporal
  in-place columns and not a single polymorphic event log. ADR-0003
  already establishes that workloads are polymorphic on `kind`;
  adding `valid_from`/`valid_to`/`latest` columns to the parent
  tables would force every existing query to add `WHERE latest =
  true`, would break the `UNIQUE(namespace_id, name)` and FK
  constraints (FK to which row version?), and would invalidate the
  current-row read path that the API and UI depend on. Sidecar
  tables keep the current-state path untouched.
- **Capture lives in the Go Store layer**, not in Postgres triggers.
  The codebase pattern (see `internal/store/`, `internal/auth/`,
  `audit_events` middleware) is "data-layer logic in Go, narrow
  Store interface, tests against memStore". Triggers move logic
  outside Go's test reach and mix poorly with goose migrations
  that themselves edit the parent tables.
- **Retention is one year online**, aligned with the annual SNC
  audit cycle. SNC chapter 5 logging baseline is 90 days, but
  asset history is closer to "what did the perimeter look like
  during the last cert renewal" than to "what request hit the
  rate limiter last Tuesday" — one year is the right operational
  horizon. Cold archive beyond that is operator-driven, not
  automatic.
- **Time-travel is operator-toggleable** via the `settings` table,
  consistent with ADRs 0012 (EOL enricher) and 0014 (MCP server) —
  a deployment that doesn't need history (small lab, dev cluster)
  pays no storage cost.

## Decision

Build **time-travel** as six per-kind sidecar history tables
populated by the Store layer, governed by a single
`time_travel_enabled` setting, and surfaced through a uniform
`?as_of=<RFC3339>` query parameter plus a per-detail-page **History**
tab. Behind the scenes, the four kinds that currently hard-delete on
reconcile gain a `terminated_at` soft-delete column; the two that
already track lifecycle (virtual_machines via ADR-0015 §2,
cloud_accounts via `disabled_at`) keep their existing semantics.

### 1. Scope of kinds in history

| Kind                | Has history table | Soft-delete column | Notes |
|---------------------|-------------------|--------------------|-------|
| `clusters`          | yes               | `terminated_at`    | new column, this ADR |
| `namespaces`        | yes               | `terminated_at`    | new column, this ADR |
| `nodes`             | yes               | `terminated_at`    | new column, this ADR |
| `workloads`         | yes               | `terminated_at`    | new column, this ADR |
| `virtual_machines`  | yes               | `terminated_at` (existing) | ADR-0015 §2 |
| `cloud_accounts`    | yes               | `disabled_at` (existing)   | semantic match: a disabled account is not "alive" for cartography |
| `pods`              | **no**            | n/a                | excluded by §Context bullet 1 |
| `services`          | **no**            | n/a                | derived from a workload; classify together with the workload |
| `ingresses`         | **no**            | n/a                | low operator-curated metadata; defer to a follow-up ADR if needed |
| `persistent_volumes` | **no**           | n/a                | follow-up ADR if SNC §8.1.b requires |
| `persistent_volume_claims` | **no**     | n/a                | follow-up ADR if SNC §8.1.b requires |

### 2. History table shape

Each `<kind>_history` table mirrors the parent's column set verbatim
(every column the parent has — typed columns, JSONB columns, FK
columns) plus a fixed bi-temporal envelope:

```sql
CREATE TABLE clusters_history (
    -- Surrogate PK for the history row itself.
    history_id     UUID PRIMARY KEY,

    -- Parent identity. Not a FK: when the parent is hard-deleted
    -- by an operator (rare; reconcile uses soft-delete now), we
    -- keep the historical rows.
    entity_id      UUID NOT NULL,

    -- Validity window. valid_to NULL = this is the row that
    -- represents the current observed state. Each (entity_id) has
    -- at most one row with valid_to IS NULL at any time.
    valid_from     TIMESTAMPTZ NOT NULL,
    valid_to       TIMESTAMPTZ,

    -- Why this row was written.
    change_type    TEXT NOT NULL CHECK (change_type IN
                                        ('create', 'update', 'soft_delete', 'restore')),

    -- Provenance. NULL for system-driven (collector / reconcile);
    -- populated for human / token writes that flow through the
    -- API. Mirrors the audit_events.actor_* shape so the two
    -- tables can be joined on (occurred_at, actor_id) for "who
    -- did what to what".
    actor_id       UUID,
    actor_kind     TEXT CHECK (actor_kind IN
                                ('user', 'token', 'system', 'collector')),

    -- ... every column the clusters table has, replicated here.
    name               TEXT NOT NULL,
    display_name       TEXT,
    environment        TEXT,
    provider           TEXT,
    region             TEXT,
    kubernetes_version TEXT,
    api_endpoint       TEXT,
    labels             JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- curated metadata (ADR-0008 / PR #48)
    owner              TEXT,
    criticality        TEXT,
    notes              TEXT,
    runbook_url        TEXT,
    annotations        JSONB NOT NULL DEFAULT '{}'::jsonb,
    location           TEXT,
    -- soft-delete marker (mirror of parent)
    terminated_at      TIMESTAMPTZ,
    -- timestamps from the parent at the moment this snapshot was taken
    created_at         TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL
);

CREATE INDEX clusters_history_entity_idx
    ON clusters_history (entity_id, valid_from DESC);
CREATE INDEX clusters_history_current_idx
    ON clusters_history (entity_id) WHERE valid_to IS NULL;
CREATE INDEX clusters_history_retention_idx
    ON clusters_history (valid_to)
    WHERE valid_to IS NOT NULL;
```

The five other tables (`namespaces_history`, `nodes_history`,
`workloads_history`, `virtual_machines_history`,
`cloud_accounts_history`) follow the same envelope plus their
parent's typed-column set.

### 3. Watched field set per kind

A history row is written **only when at least one *watched* field
has changed**. Watched fields are the ones an SNC assessor or an
incident reviewer would care about; non-watched fields (e.g.
`updated_at`, the per-tick `last_seen_at` heartbeat on cloud
accounts, transient JSONB churn from the collector) do not trigger
history. The watched sets are:

- **clusters** — `kubernetes_version`, every curated-metadata field
  (`owner`, `criticality`, `notes`, `runbook_url`, `annotations`,
  `location`), `provider`, `region`, `environment`,
  `terminated_at`. **Excluded**: `labels` (high churn), `display_name`,
  `api_endpoint`.
- **namespaces** — every curated-metadata field, every DICT field
  (`sec_disponibilite`, `sec_integrite`, `sec_confidentialite`,
  `sec_tracabilite`, `sec_notes`), `terminated_at`. **Excluded**:
  `labels`.
- **nodes** — `role`, `kubelet_version`, `kernel_version`,
  `operating_system`, `container_runtime_version`, `instance_type`,
  `zone`, `hardware_model`, every curated-metadata field, `ready`,
  `unschedulable`, `terminated_at`. **Excluded**: `internal_ip`,
  `external_ip`, `pod_cidr` (transient in cloud topologies),
  `conditions` and `taints` JSONB (high churn — flap-prone),
  `capacity_*` and `allocatable_*` (numeric drift unrelated to
  configuration).
- **workloads** — `kind`, every curated-metadata field, every DICT
  field, `terminated_at`. The `spec` JSONB **is** watched at the
  top level (a deep diff is too expensive); any change to `spec`
  writes a history row carrying the full new spec.
  **Excluded**: `labels`.
- **virtual_machines** — `power_state`, `instance_type`, `region`,
  `image_id`, `image_name`, every curated-metadata field, the
  `applications` JSONB (top-level — adding/removing/changing a
  product entry is operator intent worth recording),
  `terminated_at`. **Excluded**: `nics`, `security_groups`,
  `block_devices` (all infrastructure-derived churn).
- **cloud_accounts** — `name`, `region`, `provider`, every
  curated-metadata field, `status`, `disabled_at`. **Excluded**:
  `access_key`, `secret_key_encrypted` and all secret-related
  columns (rotation events go to `audit_events`, not here —
  history rows must never carry plaintext or ciphertext keys),
  `last_seen_at`, `last_error*` (heartbeat noise).

The watched-field set is encoded in Go (one constant per kind in
`internal/timetravel/watched.go`); changes to the set go through
code review, not configuration. A `TestWatchedFields_*` table-test
per kind asserts that every column on the parent table is either
in the watched set or in an explicit **excluded** set, so adding a
column to a parent table without classifying it for history fails
the test.

### 4. Capture path

Capture lives in the Store layer, in a thin
`internal/timetravel/` package consumed by both the PostgreSQL
store (`internal/store/`) and the in-memory test store. The flow
inside a single `Update<Kind>` transaction:

1. `SELECT … FOR UPDATE` the current row by `id`.
2. Compute the merge-patched next row (existing logic, unchanged).
3. Diff watched fields between (1) and (2).
4. If no watched field changed → no history write; commit just the
   parent UPDATE. (Saves storage; `updated_at`-only churn does not
   pollute history.)
5. If a watched field changed:
   - `UPDATE <kind>_history SET valid_to = NOW() WHERE entity_id = $1
     AND valid_to IS NULL`
   - `INSERT INTO <kind>_history (... full row snapshot, valid_from
     = NOW(), valid_to = NULL, change_type = 'update', actor_*)
     VALUES (...)`
   - Commit alongside the parent UPDATE.

`Create<Kind>` always writes the first history row with
`change_type='create'`. `Soft-delete` (the new path that replaces
hard `DELETE` from reconcile) writes
`change_type='soft_delete'`. A future `Restore<Kind>` path —
re-creating an entity that came back from the dead — writes
`change_type='restore'` and is what the VM resurrection path
(ADR-0015 §2) becomes once unified. The actor for collector- or
reconcile-driven changes is `actor_kind='collector'`,
`actor_id=NULL`. The actor for human / token writes is the
existing `auth.Caller` from request context.

### 5. Soft-delete on the four currently-hard-deleting kinds

Each of `clusters`, `namespaces`, `nodes`, `workloads` gains:

```sql
ALTER TABLE clusters    ADD COLUMN terminated_at TIMESTAMPTZ;
ALTER TABLE namespaces  ADD COLUMN terminated_at TIMESTAMPTZ;
ALTER TABLE nodes       ADD COLUMN terminated_at TIMESTAMPTZ;
ALTER TABLE workloads   ADD COLUMN terminated_at TIMESTAMPTZ;

CREATE INDEX clusters_terminated_at_idx
    ON clusters (terminated_at) WHERE terminated_at IS NOT NULL;
-- (and three identical partial indexes on the other tables)
```

The collector reconcile path that today does `DELETE FROM clusters
WHERE id = ANY(...)` becomes `UPDATE clusters SET terminated_at =
NOW() WHERE id = ANY(...) AND terminated_at IS NULL`. List
endpoints (`GET /v1/clusters` etc.) gain a default `WHERE
terminated_at IS NULL` filter, with an opt-in
`?include_terminated=true` mirroring the existing VM list
behaviour. CASCADE FKs from children continue to point at the
parent's `id`; the soft-delete is invisible to the FK chain (the
parent row stays alive). Children of a soft-deleted parent are
themselves soft-deleted in the same transaction (cascade, in
application code).

Re-appearing entities (a cluster that comes back after a
reconcile, e.g. transient API-server outage on the next tick)
clear `terminated_at`, write `change_type='restore'` to history,
and otherwise resume normal upsert semantics.

### 6. API surface

Two read endpoints per history-bearing kind, plus one global
filter:

- `GET /v1/{kind}/{id}/history` — paginated, newest-first list of
  history rows for one entity. Each row carries `valid_from`,
  `valid_to`, `change_type`, `actor_*`, and a `diff` field (a
  JSON-Patch-shaped array describing the watched-field changes
  relative to the previous row). Returns 200 with an empty list
  for an entity that has only ever existed in its current form.
- `GET /v1/{kind}/{id}?as_of=<RFC3339>` — point-in-time view. The
  store resolves it as
  `SELECT … FROM <kind>_history WHERE entity_id = $1 AND valid_from
  <= $2 AND (valid_to IS NULL OR valid_to > $2)`. Returns 200 with
  the snapshot, or 404 if the entity did not exist at that
  timestamp. Without `as_of`, behaviour is unchanged (returns the
  current row from the parent table).
- `GET /v1/{kind}?as_of=<RFC3339>` — list-as-of. Same semantics, in
  bulk. Pagination is preserved; cursor encoding is independent of
  `as_of`.

All three are gated on the existing `read` scope (humans,
auditors, admins). They land in `audit_events` as ordinary GETs
**only when** the request hit `/v1/admin/*`, per the existing
audit policy — history reads on operational endpoints stay
unlogged, consistent with how `GET /v1/clusters/{id}` is unlogged
today. The audit middleware is **not** widened to log time-travel
reads; the access-control review for sensitive history is the
same as for any current-state read.

### 7. UI surface

Each detail page gains a **History** tab next to the existing
Overview / Impact tabs (the latter from ADR-0013). The tab
renders:

- A reverse-chronological timeline of history rows for the entity.
- For each row: timestamp (relative + absolute), `change_type`
  pill, actor (linked to the user / token / collector source), and
  a collapsed diff preview.
- Expanding a row shows the full JSON-Patch-shaped diff with the
  watched-field changes, rendered with a green / red side-by-side
  view for the small set of values, falling back to a JSON
  unified diff for JSONB columns.
- A "view at this point" link on each row navigates to
  `/{kind}/{id}?as_of=<valid_from>`; the detail page renders
  read-only with a banner "Showing this {kind} as it existed at
  <time>" and a "back to current" link.

A global time-travel page at `/ui/time-travel` is **out of scope
for v1**. The per-detail-page experience covers the cartography
question; a global "show me the perimeter at date D" page is a
follow-up if a real workflow demands it.

### 8. Retention

`settings.time_travel_retention_days` (INTEGER, default 365)
governs the online retention horizon. A nightly reaper goroutine
in `internal/timetravel/reaper.go` (analogous to
`internal/eol/`) deletes rows where `valid_to IS NOT NULL AND
valid_to < NOW() - retention_days`. Rows with `valid_to IS NULL`
are never reaped (the current observed state is always kept,
even if older than retention). The reaper is operator-toggleable
via `time_travel_reaper_enabled` (default true). A
`longue_vue_time_travel_rows_pruned_total{kind}` counter and
`longue_vue_time_travel_rows_total{kind}` gauge expose retention
health to Prometheus.

Cold archive — exporting reaped rows to S3-compatible storage in
SecNumCloud-qualified hosting before purge — is **out of scope
for v1**. A follow-up ADR will define the archive shape if an
operator commits to a >1-year retention need.

### 9. Feature flag

`settings.time_travel_enabled` (BOOLEAN, default true) gates the
entire subsystem at runtime, mirroring `eol_enabled` (ADR-0012)
and `mcp_enabled` (ADR-0014). When disabled:

- Capture is skipped: Store `Update*` and `Create*` paths short-
  circuit the history write. Soft-delete still happens (the
  schema change is permanent; a flag toggle does not revert it).
- API endpoints `/{kind}/{id}/history` return 503 with a clear
  problem-details body (`"feature_disabled": "time_travel"`).
- `?as_of=` query parameters are rejected with 400.
- The History tab is hidden in the UI (the `/v1/auth/me`
  response gains `feature_flags.time_travel` for that purpose).

`LONGUE_VUE_TIME_TRAVEL_ENABLED` env var seeds the DB row at first
boot, identical to the EOL / MCP pattern.

### 10. Access control

History endpoints require the **`read`** scope (every logged-in
user). Point-in-time and history-list reads are not more
sensitive than current-state reads — an auditor walking through
a workload's lifecycle is the canonical use case, and viewers
already see the current row, so refusing them the previous row
adds friction without adding security. The `audit` scope remains
the gate for `audit_events` itself (who-did-what), which carries
strictly more sensitive provenance metadata (request bodies,
source IPs).

## Consequences

### Positive

- **POS-001**: Closes ADR-0001 §IMP-005(d) and §IMP-003 — the
  long-promised "snapshot per collection" capability lands, with
  a deduplicated, change-driven shape that is materially cheaper
  than the original "every tick produces a snapshot" framing.
- **POS-002**: Closes ADR-0008 §ALT-007 — DICT classification
  history is captured automatically by the watched-field set on
  namespaces and workloads, with no additional sidecar table or
  bespoke audit shape. An SNC assessor reading §8.3 can now point
  at a workload's History tab and see when each DICT axis last
  changed and who changed it.
- **POS-003**: Soft-delete on the four currently-hard-deleting
  kinds is the right shape independent of time-travel — every
  incident reviewer who has asked "but when did this node
  disappear?" was running into the same gap. ADR-0015 §2 already
  proved out the pattern for VMs; this generalises it.
- **POS-004**: Storage cost is bounded by *configuration churn*,
  not by *poll cadence*. A cluster whose `kubernetes_version`,
  curated metadata, and DICT values change at most once a week
  produces ~52 history rows per year, not millions.
- **POS-005**: Capture in the Store layer (not Postgres triggers)
  keeps tests in memStore reach. The same `TestStore_*Update`
  golden tests that verify merge-patch semantics today extend
  trivially to verify history-row shape.
- **POS-006**: Feature flag wiring (per ADR-0012 / 0014 pattern)
  lets dev / lab deployments opt out and pay zero storage cost,
  while production keeps history on by default.
- **POS-007**: API shape (`?as_of=` and `/history`) is uniform
  across kinds, so client code (UI, MCP server, scripts) handles
  one pattern, not six.
- **POS-008**: One-year online retention with operator-driven
  archive matches the SNC annual audit cycle without committing
  to a multi-year storage budget. Operators with longer retention
  needs can tune `time_travel_retention_days`; a 3-year setting
  is operationally feasible and changes nothing in the data
  model.

### Negative

- **NEG-001**: Six new tables, four new columns on existing
  tables, one settings flag, one reaper goroutine, three new
  API patterns, one new UI tab, and a per-kind watched-field
  set to keep current. This is the largest single ADR since
  ADR-0007 (auth) — implementation will be a multi-phase plan,
  not a single PR.
- **NEG-002**: Workload `spec` JSONB is watched as a whole, not
  field-by-field. A trivial spec change (e.g. an image-tag bump)
  writes a full-spec snapshot. For workloads with very large
  specs (multi-MB CRDs), the per-row storage cost is non-trivial.
  Mitigation: reaper runs nightly; large-spec kinds dominate
  storage long before they dominate row count.
- **NEG-003**: Pods are explicitly excluded from history. An SNC
  assessor asking "give me the pod inventory as of date D" gets
  "we don't keep that — pod-level forensics is in
  Loki/Tempo/cluster API server audit logs, not in the CMDB".
  This must be documented in `docs/compliance/snc-chapter-8.md`
  alongside the §8.1.c license deferral.
- **NEG-004**: Capture in the Store layer doesn't catch raw SQL
  edits (manual `psql`, ad-hoc migrations, hand-written
  hot-fixes). Same caveat as `audit_events`. The production
  posture is "humans don't `psql` the prod DB"; if that breaks,
  history loses fidelity for the rows the human touched.
- **NEG-005**: The `?as_of=` query parameter is a new shape on
  every list endpoint; it has to be plumbed through every list
  handler's query parser, every cursor encoder, and every list
  filter struct (`PodListFilter`-style). The mechanical cost of
  the rollout is meaningful even though each individual change
  is small.
- **NEG-006**: Soft-delete cascade in application code (not in
  Postgres FK definitions) means the cascade can be skipped by
  an incomplete code path. An integration test must verify that
  deleting a parent soft-deletes the children, mirroring how
  `TestPGCluster_CascadeDelete` covers FK cascade today.
- **NEG-007**: First-bootstrap: when the migration adds the
  history tables on an existing populated database, the *current*
  rows have no `change_type='create'` history entry — the
  perceived "creation date" of every existing entity becomes the
  migration time, not the original `created_at`. The migration
  backfills one synthetic `change_type='create'` history row per
  current entity, with `valid_from = parent.created_at`,
  `actor_kind='system'`, and a JSON note in `details` flagging
  the row as a backfill.

### Risks

- **RISK-001**: Storage growth is bounded by configuration churn
  in steady state, but a misconfigured operator pipeline that
  mass-edits curated metadata daily could produce tens of
  thousands of rows per cluster per day. Mitigation: per-kind
  history-row counters are exposed to Prometheus
  (`longue_vue_time_travel_rows_total{kind}`) so storage drift is
  alertable before it becomes a billing event.
- **RISK-002**: The watched-field set drifts: someone adds a
  column to `nodes` and forgets to classify it. Mitigation: the
  `TestWatchedFields_*` table-test fails fast, before the
  migration ships, by reflecting the parent table's column set
  at test time.
- **RISK-003**: A regression in the diff path silently writes
  history rows when nothing actually changed. Mitigation:
  golden-test the diff function per kind, and add a
  `longue_vue_time_travel_writes_total{kind, result="noop"}`
  counter that should remain at zero in steady state — a
  non-zero rate is a signal to investigate.

## Alternatives Considered

### Bi-temporal in-place columns

- **ALT-001**: **Description**: Add `valid_from`, `valid_to`,
  `latest BOOLEAN` to the existing parent tables. Every UPDATE
  becomes "set `valid_to` and `latest=false` on the old row,
  INSERT a new row with the same `id`, `valid_from=NOW()`,
  `latest=true`". Current-state queries add `WHERE latest =
  true`.
- **ALT-002**: **Rejection Reason**: Breaks every existing
  `UNIQUE(namespace_id, name)` and PRIMARY KEY constraint
  (multiple rows per logical entity), forces every existing
  query in handlers / collector / store to add `WHERE latest =
  true`, breaks every FK in the cascade chain (FK to which row
  version?), and invalidates the polymorphic-workload pattern
  from ADR-0003 (now polymorphic on `(kind, latest)`). The
  blast radius across the existing codebase is enormous and the
  benefit over sidecar tables is zero — sidecars give the same
  history with none of these consequences.

### Postgres triggers

- **ALT-003**: **Description**: Same sidecar tables, but `BEFORE
  UPDATE` / `BEFORE DELETE` triggers on each parent table do
  the history write inside Postgres. Application code is
  unaware of history.
- **ALT-004**: **Rejection Reason**: Moves logic out of Go and
  out of test reach (memStore can't simulate Postgres triggers;
  every diff test would need a real Postgres). Triggers and
  goose migrations interact badly — a goose migration that
  edits a parent table fires the trigger, polluting history
  during a deploy. The "captures raw SQL" benefit is real but
  not justified by the complexity cost; the same caveat
  already applies to `audit_events` (NEG-004) and the team
  posture handles it.

### Reuse `audit_events` as the history backing store

- **ALT-005**: **Description**: The audit middleware already
  captures every PATCH / POST that hits the API, including
  collector-driven writes via the DMZ ingest gateway (since
  ADR-0016, with `source='ingest_gw'`). Extend the body capture
  to include the resulting row, and reconstruct any past state
  by replaying matching events in `occurred_at` order.
- **ALT-006**: **Rejection Reason**: `audit_events.details` is
  designed for forensic provenance (who hit what endpoint with
  what scrubbed body), not for state reconstruction
  (deterministic before/after diffs of the row). The volume
  shape is wrong (one event per resource per tick per cluster,
  not one event per actual change). The scrubbing rules
  (passwords, token plaintexts, secret keys) make the row
  capture deliberately lossy for some kinds. Querying "cluster
  X as of D" via event replay is `O(events_since_creation)`,
  which is fine until the table reaches millions of rows.
  Sidecar tables with `valid_from`/`valid_to` give `O(log n)`
  point-in-time reads.

### Full event sourcing

- **ALT-007**: **Description**: Make events the primary store —
  every change is an event in an `entity_events(kind, id,
  occurred_at, type, payload JSONB)` table; current state
  becomes a projection materialised in the existing parent
  tables. Inspired by RFC-0025 in the ADR skill examples.
- **ALT-008**: **Rejection Reason**: Architectural shift on the
  scale of "rewrite the Store layer", not justified by the
  payoff. Every existing query path becomes "consult the
  projection, fall back to event replay". Event-schema
  versioning, replay logic, snapshot generation, projection
  rebuilds — all real costs the sidecar-table approach avoids.
  Event sourcing is the right shape for systems where the
  *event* is the business object (orders, payments,
  shipments); for a CMDB the *state* is the business object,
  history is metadata, and sidecar tables encode that
  hierarchy correctly.

### Include pods in history

- **ALT-009**: **Description**: Add `pods_history` and
  `terminated_at` to pods so pod-level forensics ("was pod X
  alive at D") stays in the CMDB.
- **ALT-010**: **Rejection Reason**: Volume. At a 1-minute poll
  on a single 200-pod cluster, change-driven capture still
  produces tens of thousands of pod-history rows per day
  (every restart, every phase transition, every IP change is
  watched). Across a multi-cluster deployment the table grows
  into the hundreds of millions of rows per year. The
  cartography use case (the ADR-0008 anchor) does not require
  pod-level history; pod forensics is observability tooling
  territory (Loki for logs, Tempo for traces, cluster API
  server audit logs for the K8s-native who-did-what).

### Hard-delete + history-only deletion event

- **ALT-011**: **Description**: Keep the reconcile loop's
  `DELETE` path; just write a `change_type='soft_delete'`
  history row before the DELETE. "List as of D" queries
  reconstruct from history; the parent table only ever holds
  live rows.
- **ALT-012**: **Rejection Reason**: Reconstructing
  "`SELECT * FROM clusters AS OF D`" becomes a fold over
  history, not an indexed lookup. The current-row read path
  diverges from the historical read path. Soft-delete on the
  parent costs one TIMESTAMPTZ column per kind and gives a
  uniform read path for both current and historical queries.
  The storage cost of keeping a terminated row in the parent
  table is negligible compared to its history rows.

### Include services / ingresses / PVs / PVCs in v1 history

- **ALT-013**: **Description**: Bring the four secondary kinds
  into history immediately rather than deferring to a
  follow-up ADR.
- **ALT-014**: **Rejection Reason**: These kinds carry no
  curated metadata and no DICT classification (per ADR-0008
  §IMP-001 placement); they are derivative of their owning
  workload / cluster. Their cartography value is in the
  current state, not in their history (an SNC assessor
  asking "what services were exposed on D" is really asking
  "what workloads were running on D, and what services do
  those workloads currently expose"). Adding four more
  history tables for low-value churn is premature; if a real
  use case surfaces, a follow-up ADR adds them with the same
  shape.

## Implementation Notes

- **IMP-001**: **Migration order**. One migration per concern,
  ordered so a half-applied state still compiles:
  - `00030_add_terminated_at.sql` — `terminated_at` column +
    partial index on each of `clusters`, `namespaces`, `nodes`,
    `workloads`. Idempotent: `ADD COLUMN IF NOT EXISTS`.
  - `00031_create_clusters_history.sql` — table + three
    indexes.
  - `00032_create_namespaces_history.sql`.
  - `00033_create_nodes_history.sql`.
  - `00034_create_workloads_history.sql`.
  - `00035_create_virtual_machines_history.sql`.
  - `00036_create_cloud_accounts_history.sql`.
  - `00037_backfill_history_create_rows.sql` — synthetic
    `change_type='create'` row per current entity (NEG-007),
    `valid_from = parent.created_at`,
    `actor_kind='system'`, a JSON note flagging backfill.
    Idempotent via `ON CONFLICT DO NOTHING` keyed on
    `(entity_id, valid_from)` with a unique partial index
    `WHERE change_type = 'create'`.
  - `00038_settings_time_travel.sql` — three new rows in the
    single-row `settings` table:
    `time_travel_enabled BOOLEAN DEFAULT TRUE`,
    `time_travel_retention_days INTEGER DEFAULT 365`,
    `time_travel_reaper_enabled BOOLEAN DEFAULT TRUE`.

- **IMP-002**: **Package boundaries**. Three new packages:
  - `internal/timetravel/` — `WatchedFields` constant per
    kind, `Diff(prev, next, watched) JsonPatch` helper,
    `Capture(tx, kind, ...)` wrapper used by the Store's
    `Update*` and `Create*` methods.
  - `internal/timetravel/reaper.go` — nightly goroutine,
    cadence and feature flag from `settings`. Mirrors
    `internal/eol/` lifecycle.
  - `internal/api/history_handlers.go` — three handlers
    (`HandleEntityHistory`, `HandleEntityAsOf`,
    `HandleListAsOf`). Hand-written, not generated, because
    `?as_of=` is a per-list filter that doesn't fit the
    generated Store interface cleanly. Routes are mounted
    in `main.go` alongside the impact and settings routes.

- **IMP-003**: **OpenAPI shape**. New endpoints documented in
  `api/openapi/openapi.yaml`:
  - `GET /v1/{kind}/{id}/history` — schema
    `EntityHistoryPage` with `items[].diff` as a
    JSON-Patch-shaped array. Pagination cursor mirrors the
    list endpoints.
  - `GET /v1/{kind}/{id}` gains a `?as_of` query param.
  - `GET /v1/{kind}` (list endpoints for the six kinds)
    gain the same `?as_of` query param.
  - All three feed into the generated server stubs; the
    handlers themselves are hand-written as noted in
    IMP-002. Per the project's OpenAPI-validation
    convention, every new endpoint ships with a
    `pb33f/libopenapi-validator` test for spec +
    request/response.

- **IMP-004**: **Store interface change**. The narrow
  `Store` interface in `internal/api/store.go` gains six
  new methods:
  - `GetClusterAsOf(ctx, id, t) (*Cluster, error)`
  - `ListClusterHistory(ctx, id, page) ([]ClusterHistoryRow, error)`
  - …and four equivalents per kind (namespace, node,
    workload, vm, cloud_account).
  Existing `UpdateCluster` etc. **gain a new internal
  helper** `recordHistoryIfChanged(tx, prev, next)` that
  the PG store implements via `internal/timetravel`.
  memStore implements the same contract in-memory so unit
  tests cover both paths.

- **IMP-005**: **Audit integration**. The audit middleware
  is unchanged. History rows duplicate `actor_*` to keep
  history queryable without joining `audit_events`, but a
  follow-up convenience handler (`GET /v1/admin/audit/{id}`)
  joins the two — given an audit event, return the
  matching history row pair (before / after) on the
  affected entity. Out of scope for this ADR; flagged as
  a follow-up.

- **IMP-006**: **Reaper cadence and tunables**. Default
  cadence is once per day at 03:00 UTC (low-traffic window,
  mirrors how `audit_events` retention would land if it
  were implemented). Per-kind batch size is 1000 rows per
  iteration with a `time.Sleep(100ms)` between batches to
  avoid lock thrash with the live workload. Both knobs are
  hard-coded constants in v1; if operators need to tune
  them, a follow-up ADR exposes them as `settings` rows.

- **IMP-007**: **Soft-delete cascade in application code**.
  `SoftDeleteCluster(ctx, id, actor)` in the Store does:
  ```
  BEGIN;
  -- soft-delete children first, in dependency order
  UPDATE workloads SET terminated_at = NOW()
    WHERE namespace_id IN (SELECT id FROM namespaces WHERE cluster_id = $1)
    AND terminated_at IS NULL;
  UPDATE namespaces SET terminated_at = NOW()
    WHERE cluster_id = $1 AND terminated_at IS NULL;
  UPDATE nodes SET terminated_at = NOW()
    WHERE cluster_id = $1 AND terminated_at IS NULL;
  UPDATE clusters SET terminated_at = NOW() WHERE id = $1;
  -- recordHistoryIfChanged() runs once per row touched
  COMMIT;
  ```
  Children that are already terminated (`terminated_at IS
  NOT NULL`) are skipped — the `AND terminated_at IS NULL`
  guard is the idempotency check. Pods, services,
  ingresses, PVs, PVCs are **hard-deleted via FK CASCADE**
  unchanged — they aren't in scope for history.

- **IMP-008**: **UI integration**. The History tab is a
  thin React component under `ui/src/pages/components/`
  reused across detail pages, taking
  `(kind: Kind, id: string)` as props. Diff rendering uses
  the existing JSON-Patch viewer from the curated-metadata
  edit flow (PR #48). The "view at this point" link sets
  the `as_of` query param via React Router; the detail
  page reads `useSearchParams().get('as_of')` and threads
  it into `useResource()` so the same component renders
  current and historical state.

- **IMP-009**: **MCP integration**. The 17 read-only MCP
  tools from ADR-0014 gain optional `as_of` parameters
  uniformly. `get_cluster(id, as_of?)`,
  `list_namespaces(cluster_id, as_of?)`, etc. AI agents
  asking "what did this look like during the incident"
  get a first-class API. Tool schemas updated in
  `internal/mcp/tools.go`; the underlying handler reuse
  is automatic.

- **IMP-010**: **Metrics**. Three new Prometheus series:
  - `longue_vue_time_travel_rows_total{kind}` (gauge) —
    current row count per history table.
  - `longue_vue_time_travel_writes_total{kind, change_type}`
    (counter) — history rows written, partitioned by
    change type.
  - `longue_vue_time_travel_rows_pruned_total{kind}`
    (counter) — rows reaped per kind.
  All three live in `internal/metrics/` alongside the
  existing collector / impact / EOL series.

- **IMP-011**: **SecNumCloud documentation**.
  `docs/compliance/snc-chapter-8.md` (introduced by
  ADR-0008 §IMP-008) gains a new sub-section explicitly
  cross-referencing time-travel as the v1 satisfaction of
  §8.1.a *currentness over time* and §8.3 *traceability of
  classification changes*. The pod-exclusion (NEG-003) is
  documented there too, with the explicit
  Loki/Tempo/cluster-audit pointer.

- **IMP-012**: **Success criterion**. A walk-through of
  three concrete questions against a populated longue-vue:
  1. "What did cluster `prod-eu-1` look like on
     2026-02-15T12:00Z?" — `GET /v1/clusters/prod-eu-1?as_of=...`
     returns a snapshot, or 404 if the cluster did not exist.
  2. "Was workload `payment-svc` alive on 2026-03-01T08:00Z?" —
     `GET /v1/workloads/{id}?as_of=...` returns the row with
     `terminated_at` either NULL (alive) or non-NULL
     (terminated, with the timestamp of soft-delete).
  3. "What changed on namespace `team-platform` between
     2026-04-01 and 2026-05-01?" — `GET
     /v1/namespaces/{id}/history?since=2026-04-01&until=2026-05-01`
     returns the history rows with diffs in that window.
  A `docs/compliance/snc-chapter-8.md` update demonstrates
  each of the three queries on a seeded test deployment.

## References

- **REF-001**: ADR-0001 — *CMDB for SNC using Kubernetes*.
  §IMP-005(d) committed to a snapshot/versioning ADR;
  §IMP-003 promised versioned snapshots from the
  collector. This ADR closes both.
- **REF-002**: ADR-0007 — *Auth & RBAC*. The
  `auth.Caller{id, kind, role, scopes}` context plumbed by
  the auth middleware is the source of `actor_*` columns
  on history rows; the `audit` and `read` scope semantics
  defined here are the ones used by §10 above.
- **REF-003**: ADR-0008 — *Asset-management data model
  for SecNumCloud v3.2 chapter 8*. §ALT-007 explicitly
  punted classification history into "future snapshots /
  time-travel work"; this ADR delivers it.
- **REF-004**: ADR-0012 — *EOL enrichment via
  endoflife.date*. Reference pattern for the `settings`-
  toggleable, env-seeded, background-goroutine subsystem
  shape that the time-travel reaper mirrors.
- **REF-005**: ADR-0013 — *Impact analysis graph*. The
  per-detail-page History tab sits alongside the Impact
  tab introduced here; both are consumed by the same UI
  detail-page chrome.
- **REF-006**: ADR-0014 — *MCP server*. AI-tool access to
  CMDB state extends naturally to historical state via the
  optional `as_of` parameter (IMP-009).
- **REF-007**: ADR-0015 — *VM collector for non-Kubernetes
  platform VMs*. §2 introduced the `terminated_at`
  soft-delete pattern that this ADR generalises to four
  more kinds.
- **REF-008**: ADR-0017 — *Public-listener TLS posture*.
  Trust-aware HSTS and `httputil.ClientIP` are reused by
  the audit middleware that history capture interoperates
  with (collector-source actors land via the DMZ ingest
  gateway, ADR-0016).
- **REF-009**: ANSSI, *Prestataires de services
  d'informatique en nuage (SecNumCloud) — référentiel
  d'exigences*, v3.2, 2022-03-08, chapter 8 (*Gestion des
  actifs*) — §8.1.a (currentness of inventory), §8.3
  (DICT classification traceability).
- **REF-010**: ANSSI, *Prestataires de services
  d'informatique en nuage (SecNumCloud) — référentiel
  d'exigences*, v3.2, 2022-03-08, chapter 5 (*Politique de
  sécurité de l'information* — log retention baseline,
  contrast for §Context bullet on retention horizon).
- **REF-011**: ANSSI, *Prestataires de services
  d'informatique en nuage (SecNumCloud) — référentiel
  d'exigences*, v3.2, 2022-03-08, chapter 12 (*Conduite du
  changement* — change-set diff requirement that motivates
  §6 history endpoint).
- **REF-012**: Martin Fowler, *Bitemporal History* —
  https://martinfowler.com/articles/bitemporal-history.html.
  Source of the `valid_from` / `valid_to` envelope shape
  used in §2.
