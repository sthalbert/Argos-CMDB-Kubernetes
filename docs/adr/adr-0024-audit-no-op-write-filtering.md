---
title: "ADR-0024: Audit no-op write filtering for SecNumCloud trail compactness"
status: "Accepted"
date: "2026-05-13"
authors: "Steve ALBERT"
tags: ["architecture", "audit", "secnumcloud", "performance"]
supersedes: ""
superseded_by: ""
---

# ADR-0024: Audit no-op write filtering for SecNumCloud trail compactness

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-05-13
- **Supersedes:** none
- **Superseded by:** none

## Context

The `audit_events` table grows by ~500k rows/day on the production CMDB
instance. The 2026-05-12 snapshot measured **6.7 GB / 6.4M rows over 16
days**. The dominant write volume comes from K8s collector ticks (POST
`/v1/pods`, `/v1/services`, `/v1/virtual-machines`, …): every tick
re-upserts every resource even when nothing changed, and the audit
middleware records every non-GET request — so each tick produces one
audit row per resource regardless of state change.

The growth has two operational consequences:

1. Insert pressure cascades. `context canceled` errors were observed on
   `/v1/services/reconcile` under collector load.
2. ANSSI SecNumCloud retention (≥30 days) extrapolates to ~15 GB of
   mostly-redundant rows — pure cost without forensic value.

The audit middleware (ADR-0007 §IMP-005, ADR-0008 §8.3, ADR-0010) was
written before push collectors existed; its rule "audit every non-GET
request" was correct for a human-only API but is wrong for a system
where 95%+ of writes are idempotent collector re-ticks.

## Decision

**Audit = state change, not request method.**

A write is auditable when it produces an observable change in CMDB
state. A re-upsert that touches only clock fields (`last_seen`,
`updated_at`) is **not** a state change and is not audited. The audit
middleware remains the sole writer of `audit_events`; the change is
purely a filter applied to its insert path.

### State-change definition

- **Upsert (POST `/v1/<resource>`)**: any business field changes
  (`INSERT`, or `UPDATE` where the CTE detects at least one column
  changed). Clock-field-only updates → no audit row.
- **Reconcile (POST `/v1/<resource>/reconcile`)**: at least one row
  was deleted/tombstoned (`Delete*NotIn` returned `> 0`,
  `ReconcileVirtualMachines` tombstoned `> 0`). Empty reconcile → no
  audit row.
- **Everything else stays audited** — see compliance invariants below.

### Mechanism

A new `UpsertOutcome` enum is returned by every `Upsert*` store
function:

```go
type UpsertOutcome int

const (
    OutcomeInserted        UpsertOutcome = iota // new row
    OutcomeBusinessChanged                      // UPDATE: ≥1 business field changed
    OutcomeNoChange                             // UPDATE: only clock fields touched
)
```

The PG implementation uses a CTE that compares each business column
with `IS DISTINCT FROM` (NULL-safe) in a single round-trip:

```sql
WITH old AS (SELECT <business cols> FROM <table> WHERE …),
     upserted AS (
       INSERT … ON CONFLICT … DO UPDATE SET … RETURNING id, xmax, <business cols>
     )
SELECT u.id,
       (u.xmax = 0) AS inserted,
       (u.xmax <> 0 AND (o.col_a IS DISTINCT FROM u.col_a OR …)) AS business_changed
FROM upserted u LEFT JOIN old o ON true;
```

Handlers consume the outcome and call `api.SetAuditSkip(ctx)` when it
is `OutcomeNoChange`. Reconcile handlers call `SetAuditSkip` (and
`SetAuditSkipReason(ctx, "reconcile_empty")`) when the store returned
`0`. The audit middleware reads the bag after the handler returns and
decides whether to insert.

### Forensic bypass

Even when `SetAuditSkip` was called, the middleware **always inserts**
when any of the following holds:

- HTTP status ≥ 400 (any error path — forensic value).
- Path matches an auth-always set: `/v1/auth/login`, `/v1/auth/logout`,
  `/v1/auth/change-password`, `/v1/auth/oidc/authorize`,
  `/v1/auth/oidc/callback`.

`shouldAudit()` itself is unchanged: it still passes admin GETs
(`/v1/admin/*`) and the credentials-fetch path
(`/v1/cloud-accounts/.../credentials`) into the audit pipeline. The
skip flag is only ever set by upsert + reconcile handlers, never by
admin or auth handlers.

## Compliance invariants

| Category | Before | After | Reason |
|---|---|---|---|
| Admin GET `/v1/admin/*` | audited | **audited** | `shouldAudit()` unchanged |
| Credentials fetch GET | audited | **audited** | `shouldAudit()` unchanged (ADR-0015 §5) |
| Auth (login, logout, change-pwd, OIDC) | audited | **audited** | explicit skip bypass on auth-always paths |
| Error 4xx/5xx on write | audited | **audited** | explicit skip bypass when status ≥ 400 |
| Admin create/update/delete | audited | **audited** | admin handlers do not call `SetAuditSkip` |
| User self-service (change-password) | audited | **audited** | auth bypass |
| Collector INSERT | audited | **audited** | `Outcome=Inserted` |
| Collector UPDATE business | audited | **audited** | `Outcome=BusinessChanged` |
| Collector reconcile with deletes | audited | **audited** | `count > 0` |
| **Collector UPDATE no-op (clock only)** | audited | **dropped** | `Outcome=NoChange` |
| **Collector reconcile empty** | audited | **dropped** | `count == 0` |

The trade-off is that we lose the ability to assert from the audit
table alone "this collector token called the API at 15:32:14". That
behavioural signal moves to the Prometheus counter below — `audit_events`
remains the forensic record of state changes only.

## Observability

A new Prometheus counter replaces the "collector heartbeat via audit
row" signal:

```
longue_vue_audit_events_skipped_total{actor_kind, resource_type, reason}
```

- `actor_kind`: `user|token|anonymous`
- `resource_type`: `pod|service|workload|node|namespace|ingress|persistentvolume|persistentvolumeclaim|virtual_machine|<resource>`
- `reason`: `no_change` (upsert no-op) | `reconcile_empty` (reconcile no-op)

The ratio `skipped / (skipped + inserted)` measures filter efficiency.
Targeted dashboard panel "Audit write rate" gets two series — inserted
vs skipped.

No new application logs: skipped events stay silent. Detection errors
(unexpected `UpsertOutcome` value) log at WARN.

## Consequences

### Positive

- ~95% reduction in `audit_events` insert volume on collector traffic
  (3 identical upserts → 1 row, validated by the integration test).
- SecNumCloud 30-day window shrinks from ~15 GB to ~750 MB on the
  production traffic mix.
- Insert pressure relieved → `context canceled` cascades on hot
  reconciles disappear.
- Forensic value preserved: every state change, every error, every
  auth event still lands in `audit_events`.

### Negative

- Per-upsert SQL grows from a single `INSERT … ON CONFLICT …` to a CTE
  with `IS DISTINCT FROM` against the old row. Benchmark `UpsertPod`
  (hottest path) before merge; if regression > 15% the CTE is reverted
  and a different detection strategy (e.g. application-side hash) is
  evaluated.
- Each `Upsert*` carries a hand-maintained list of business columns.
  Adding a new business column without updating that list silently
  drops audits for changes to it. Mitigations: `// AUDIT_BUSINESS_FIELDS`
  marker comment on every list, PR review checklist line, and a
  table-driven test asserting `Outcome=BusinessChanged` when each
  business field is touched individually.
- Behavioural observability ("how often does this collector tick?")
  moves out of `audit_events` and into the Prom counter. Dashboards
  that previously scraped `audit_events` for collector activity must
  migrate.

### Neutral

- No schema change on `audit_events`. Existing rows are preserved;
  future rows just contain less collector noise.
- No feature flag. The change is non-recoverable — a missed audit
  event cannot be backfilled — so a graduated flag would only defer
  the risk, not mitigate it. Mitigation lives in tests, not in a
  runtime toggle. Rollback is a code revert: middleware ignores `skip`
  and audit becomes verbose again.

## Alternatives considered

### Asynchronous audit insertion (queue + worker)

Move audit inserts off the request path entirely into a background
worker. Rejected: doesn't reduce row count, only smooths latency.
Same 6.4M rows / 16 days, same retention cost.

### Audit-events partitioning

PostgreSQL declarative range partitioning on `occurred_at`. Solves the
storage-management problem (drop old partitions instead of `DELETE`)
but **does not** reduce row volume. Tracked separately as a follow-up
structural fix; complementary to this ADR.

### Application-side hash comparison

Compute a `SHA-256` of the row's business columns in Go, compare to
the previous hash stored in the DB. Rejected: doubles the storage
overhead on every row, requires a schema migration, and risks
divergence between the in-Go hash and the actual SQL semantics.

### Audit-everything-as-now, just batch the writes

Buffer audit writes in-memory and flush every N seconds. Rejected:
loses durability on crash, complicates the forensic chain (gap windows
between flush and crash), and still costs the row volume.

### Per-collector heartbeat suppression

Drop audits from a specific token-id allowlist. Rejected: brittle (new
collector deployment forgotten in the allowlist would re-flood
audits), and it preserves audits for handwritten Postman/curl traffic
that nobody asked for.

## Implementation status

- Migration: none. No `audit_events` schema change.
- Store: `Upsert*` signature returns `UpsertOutcome`; PG impl uses CTE
  with `IS DISTINCT FROM`; memStore fake mirrors via snapshot +
  `reflect.DeepEqual`.
- Middleware: `SetAuditSkip(ctx)`, `SetAuditSkipReason(ctx, reason)`,
  bypass when `status ≥ 400` or path ∈ auth-always set.
- Handlers wired: 8 K8s upserts + VM upsert + 8 K8s reconciles + VM
  reconcile.
- Metrics: `longue_vue_audit_events_skipped_total{actor_kind,resource_type,reason}`.
- Tests: per-upsert handler tests (mock store outcome → assert skip),
  reconcile tests (`n == 0` → skip with reason `reconcile_empty`),
  middleware matrix (status ≥ 400 → never skip, auth-always → never
  skip), integration test (3 identical pod upserts → 1 row + counter
  delta = 2).

## Related ADRs

- ADR-0007 — Auth & RBAC — **unchanged**, concept preserved.
- ADR-0008 — SecNumCloud Ch.8 asset management — **amendment** to
  IMP-005: criterion changes from "every non-GET" to "every state
  change"; PATCH classification fields always change state, so §8.3
  traceability is preserved.
- ADR-0010 — Admin-only cluster deletion with audit trail —
  **amendment**: the "AuditMiddleware captures every non-GET request"
  line is footnoted; cluster deletes are never no-op (always remove
  rows), so the ADR's conclusions stand.
- ADR-0015 — VM collector — credentials fetch GET stays audited (the
  `shouldAudit()` path is unchanged).

## References

- Design doc: `docs/superpowers/specs/2026-05-13-audit-noop-filter-design.md`
- Implementation plan: `docs/superpowers/plans/2026-05-13-audit-noop-filter.md`
- Code: `internal/api/audit.go`, `internal/api/outcome.go`, `internal/api/server.go`,
  `internal/api/virtual_machine_handlers.go`, `internal/store/pg_*.go`,
  `internal/metrics/metrics.go`.
- CLAUDE.md — "Audit log" section.
