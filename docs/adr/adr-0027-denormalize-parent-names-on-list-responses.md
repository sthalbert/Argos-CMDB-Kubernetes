---
title: "ADR-0027: Denormalize parent names on list responses for scale"
status: "Accepted"
date: "2026-05-22"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "api", "performance", "scale", "ux"]
supersedes: ""
superseded_by: ""
---

# ADR-0027: Denormalize parent names on list responses for scale

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-05-22
- **Supersedes:** none
- **Superseded by:** none

## Context

The UI rendered parent context (cluster name, namespace name, workload
name) by joining client-side: every Lists.tsx page fetched the *entire*
namespace + cluster + workload tables into in-memory `Map<UUID, T>`
indexes, then looked up each row of the visible page against those
indexes.

This worked acceptably at a few hundred namespaces. It will not scale
to 10 000+ resources for three reasons:

1. **Round-trips and bytes.** At 10 000 namespaces, the
   `useNamespaceIndex` helper triggers ~50 paged `GET /v1/namespaces`
   requests on every page load — ~3 MB of payload to render 50 rows.
2. **Browser memory.** The `Map<UUID, Namespace>` index sits resident
   for the lifetime of the page; multiple list pages multiply the
   footprint.
3. **Silent truncation.** `fetchAllPaged` caps at 200 pages × 500 items
   = 40 000 items. Beyond that, the index is incomplete and the UI
   silently displays raw UUIDs as a fallback (the immediate bug that
   surfaced this ADR — soft-deleted namespaces fall out of the default
   index because the server filters them, and the UI's
   `NamespaceLink` falls back to `<IdLink to={…} id={uuid} />`).

The third point also reveals a correctness gap: any row whose parent is
soft-deleted, terminated, or otherwise hidden from the default list
query is rendered as a raw UUID with no indication of *why*. Operators
see opaque identifiers in place of human-readable names.

## Decision

List and detail endpoints on entities that carry a parent FK return the
parent's name (and where useful, the grandparent's id+name) directly in
the response payload. The UI drops the client-side index helpers
entirely and renders the denormalized fields verbatim.

| Endpoint(s) | Fields added |
|---|---|
| `GET /v1/ingresses[/{id}]` | `namespace_name`, `cluster_id`, `cluster_name` |
| `GET /v1/services[/{id}]` | `namespace_name`, `cluster_id`, `cluster_name` |
| `GET /v1/pvcs[/{id}]` | `namespace_name`, `cluster_id`, `cluster_name` |
| `GET /v1/pods[/{id}]` | `namespace_name`, `cluster_id`, `cluster_name`, `workload_name` |
| `GET /v1/workloads[/{id}]` | `namespace_name`, `cluster_id`, `cluster_name` |
| `GET /v1/namespaces[/{id}]` | `cluster_name` |

Implementation:

- The store joins read-time:
  ```sql
  LEFT JOIN namespaces n ON n.id = entity.namespace_id
  LEFT JOIN clusters   c ON c.id = COALESCE(entity.cluster_id, n.cluster_id)
  ```
  `LEFT JOIN` is deliberate — soft-deleted parents (`terminated_at IS NOT
  NULL`) stay visible with their names intact rather than disappearing.
  Orphans whose parent row no longer exists return `null` for the joined
  name, which the UI renders as an explicit "(terminated)" or
  "(orphan)" badge instead of a UUID.
- All new fields are **optional in OpenAPI** (`*string` / `*UUID` in Go).
  Non-breaking for any existing client; old clients ignore unknown
  fields. The current generated SDKs are repo-internal so this is a
  pure additive change.
- The UI deletes `useNamespaceIndex`, `fetchAllNamespaces`,
  `fetchAllClusters`, and `fetchAllWorkloads` from `Lists.tsx`.
  `NamespaceLink` and equivalent helpers accept inline data and render
  in O(1) per row.

## Consequences

- **List render becomes O(page_size)** in payload, memory, and time —
  constant regardless of total entity count. A 50-row Ingresses page is
  one request and a few KB.
- **The "UUID-instead-of-name" UX bug is closed** at its root. Any
  remaining `null` parent name is a real signal (orphan or terminated)
  that the UI now surfaces explicitly.
- **One JOIN per list query** — measured cost on FK-indexed columns is
  sub-millisecond at expected scale. PostgreSQL's planner uses the
  existing FK indexes on `entity.namespace_id` and `namespaces.cluster_id`.
- **No collector changes.** The collectors keep writing the canonical
  FK fields; denormalization happens at read time only.
- **Phase 2 escape hatch (deferred):** if at very high scale (~100k+
  rows per entity) the JOIN ever becomes hot in pgbench, materialize
  `namespace_name` / `cluster_name` as actual columns on the child
  tables. K8s namespace and cluster names are immutable for the
  lifetime of the resource, so no consistency-drift problem. *This
  optimization is explicitly not done now — pre-optimization without a
  measured signal.*

## References

- ADR-0021 — Soft-delete semantics (creates the orphan scenario this
  ADR makes visible)
- ADR-0004 — Layer model (cluster → namespace → applicative)
- Original bug: ingresses showing namespace UUID instead of name in
  list views, traced to `useNamespaceIndex` excluding soft-deleted
  namespaces.
