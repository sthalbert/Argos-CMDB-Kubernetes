---
title: "ADR-0044: Cluster staleness — collector heartbeat + derived status"
status: "Accepted"
date: "2026-08-28"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "cmdb", "cluster", "collector", "heartbeat", "staleness"]
supersedes: ""
superseded_by: ""
---

# ADR-0044: Cluster staleness — collector heartbeat + derived status

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-08-28
- **Supersedes:** none
- **Superseded by:** none

## Context

Collectors create clusters but nothing signals that one stopped
reporting. `clusters.updated_at` is not a heartbeat: EnsureCluster's
steady-state branch was a no-op, so a healthy cluster can carry a
months-old updated_at and a dead cluster looks identical to a live one.
Operators need to list and filter clusters with no sign of life for X
days (SNC inventory hygiene).

## Decision

1. **Heartbeat column.** `clusters.last_seen_at` (NOT NULL, backfilled to
   now()) is refreshed by every EnsureCluster branch. Every collector
   tick reaches EnsureCluster in both modes (in-process pull calls the
   store; push mode POSTs /v1/clusters through the ingest gateway), so
   no new endpoint or allowlist entry is needed. The no-op branch
   updates only last_seen_at — updated_at keeps meaning "data changed".

2. **Derived status, not materialized.** `stale = last_seen_at <
   now() - cluster_stale_after_days` is computed at read time and
   returned as a read-only field, plus a `stale=` list filter and a
   `last_seen_at` sort key. No sweeper job, no transitions, no
   flapping; changing the threshold reclassifies instantly.

3. **Threshold.** `settings.cluster_stale_after_days` (default 7,
   0 = disabled), seeded from LONGUE_VUE_CLUSTER_STALE_AFTER_DAYS,
   hot-editable via PATCH /v1/admin/settings.

4. **Time-travel.** last_seen_at is classified in ExcludedFields —
   heartbeat writes must not generate clusters_history rows (per-tick
   noise). Precedent: migration 00057 nodes.image_*.

5. **Audit.** The no-op ensure tick now calls SetAuditSkip (ADR-0024),
   ending the one-audit-row-per-push-tick pollution. Trade-off: a
   RESTORE ensure is skipped too; the resurrection remains traceable
   via its clusters_history `restore` row.

6. **Metrics.** metricsrefresh exports
   longue_vue_cluster_last_seen_timestamp_seconds{cluster} and
   longue_vue_clusters_stale — the server-side counterpart of the
   collector-registry last-poll gauge, which is invisible to server-side
   alerting in push mode.

## Alternatives considered

- **Materialized stale_at + sweeper**: auditable dated transitions, but a
  new job, flapping guards, and history noise. Can be layered on later
  if SNC evidence requires recorded transitions.

- **Reusing operator soft-delete**: SoftDeleteCluster hard-deletes
  pods/services/ingresses/PVCs — far too destructive for an automatic
  marker. `stale` is reversible and orthogonal to `terminated_at`.

## Consequences

- A manually declared cluster with no collector goes stale after the
  threshold — factually correct ("no collector heartbeat"); the UI
  wording reflects it.

- Terminated clusters stay governed by include_terminated; staleness
  applies to live rows only (heartbeat query excludes terminated).

- Old collectors need no change: the heartbeat is entirely server-side.

## References

- **REF-001**: ADR-0024 — Audit no-op write filtering for SecNumCloud
  trail compactness (the `SetAuditSkip` mechanism the no-op ensure
  tick now calls, per decision item 5) —
  `docs/adr/adr-0024-audit-no-op-write-filtering.md`
- **REF-002**: ADR-0042 — Uniform list search & sort contract (the
  `stale=` filter and `last_seen_at` sort key join this existing
  list/filter/sort contract rather than inventing a parallel one) —
  `docs/adr/adr-0042-uniform-list-search-sort.md`
- **REF-003**: ADR-0015 — VM collector for non-Kubernetes platform
  infrastructure (`cloud_accounts.last_seen_at` heartbeat is the
  precedent this ADR generalises to clusters) —
  `docs/adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md`
- **REF-004**: ADR-0021 — Time-travel snapshots for SecNumCloud asset
  history (watched vs. excluded fields and the soft-delete/restore
  history-row contract that `last_seen_at`'s ExcludedFields
  classification and the RESTORE-ensure trade-off in decision item 5
  build on) — `docs/adr/adr-0021-time-travel-snapshots.md`
