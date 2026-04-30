---
title: "ADR-0010: Admin-only cluster deletion with mandatory audit trail"
status: "Proposed"
date: "2026-04-23"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "security", "rbac", "audit", "deletion"]
supersedes: ""
superseded_by: ""
---

# ADR-0010: Admin-only cluster deletion with mandatory audit trail

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

Deleting a cluster is the single most destructive operation in Argos. The `clusters` table sits at the root of the FK tree; a `DELETE FROM clusters WHERE id = $1` cascades through every child table — namespaces, nodes, persistent volumes, and transitively all pods, workloads, services, ingresses, and PVCs belonging to those namespaces. A single API call can erase the entire inventory of a production cluster from the CMDB.

Today the `DELETE /v1/clusters/{id}` endpoint is gated on the `delete` scope, which per ADR-0007 is granted exclusively to the `admin` role. That satisfies the "only admins can do it" requirement at the scope level. However, two gaps remain:

1. **No explicit confirmation safeguard.** A single `DELETE` call — whether from the UI, a script, or an accidental `curl` — immediately drops the cluster and all its children. There is no "are you sure?" at the API level and no soft-delete window.
2. **Audit coverage is implicit.** The `AuditMiddleware` already captures every non-GET request including `DELETE /v1/clusters/{id}`, recording the actor, timestamp, HTTP method/path, response status, and scrubbed body. But the audit record does not capture _what_ was lost — no count of cascaded children, no cluster name snapshot. If the cluster row is gone, the audit event's `resource_id` (a UUID) becomes an opaque string with no human context.

ANSSI SecNumCloud chapter 8 (asset management) requires that the CMDB maintain an accurate, auditable inventory. Deleting an entire cluster's worth of assets is a significant event that must be traceable to a named human actor with enough context to answer "what happened and what was lost."

## Decision

Restrict cluster deletion to the `admin` role and enrich the audit trail so every cluster deletion is fully traceable. Specifically:

### 1. Scope stays `delete` — admin-only by role mapping

The existing `security: - BearerAuth: [delete]` declaration on `DELETE /v1/clusters/{id}` is correct. Only the `admin` role carries the `delete` scope (ADR-0007). No other role — `editor`, `auditor`, `viewer` — can invoke this endpoint. Machine tokens (`api_tokens`) can only carry `read`, `write`, or `delete` scopes; in practice, issuing a token with `delete` is an admin decision made in the admin UI.

This decision explicitly **does not** elevate the scope to `admin`; that would break symmetry with the other `DELETE` endpoints (nodes, pods, etc.) without adding real security, since `delete` is already admin-exclusive.

### 2. Enriched audit event for cluster deletion

The `AuditMiddleware` will enrich the `details` JSONB payload for `cluster.delete` events with a **pre-deletion snapshot**:

```json
{
  "cluster_name": "prod-eu-west-1",
  "cluster_display_name": "Production EU West",
  "cluster_environment": "production",
  "cluster_owner": "platform-team",
  "cluster_criticality": "critical",
  "cascade_counts": {
    "namespaces": 12,
    "nodes": 8,
    "pods": 347,
    "workloads": 42,
    "services": 28,
    "ingresses": 15,
    "persistent_volumes": 6,
    "persistent_volume_claims": 9
  }
}
```

This snapshot is captured **before** the `DELETE` executes so the data is still available to query. If the handler returns a non-2xx status (e.g., 404 — cluster not found), the snapshot is omitted and the audit event records only the failed attempt.

### 3. Database-level cascade remains the deletion mechanism

The FK chain (`clusters → namespaces/nodes/PVs → pods/workloads/services/ingresses/PVCs`) with `ON DELETE CASCADE` is the correct mechanism. No application-level loop deleting children one-by-one — that would be slower, race-prone, and add no value since the DB enforces referential integrity atomically.

### 4. No soft-delete (v1)

Soft-delete (a `deleted_at` column with a grace period) was considered and rejected for v1. The collector's reconciliation loop would immediately conflict: it would try to re-create a "soft-deleted" cluster on the next poll tick, or the reconciler would need to learn about soft-delete semantics. The complexity is not justified at current scale. The enriched audit snapshot provides the "what was lost" answer without keeping zombie rows.

### 5. UI confirmation gate

The UI must present a confirmation dialog before calling `DELETE /v1/clusters/{id}`. The dialog shows the cluster name, environment, owner, and cascade counts (fetched via existing endpoints) and requires the user to type the cluster name to confirm. This is a UI-only safeguard — the API itself does not enforce a confirmation token, keeping it simple for automation that genuinely needs to delete clusters.

## Consequences

### Positive

- **POS-001**: Cluster deletion is restricted to the `admin` role by construction (ADR-0007 scope mapping). No configuration needed, no way to bypass without changing the role.
- **POS-002**: Every cluster deletion is traceable to a named human (or a named API token created by a named human) with full context — cluster identity, ownership, and a count of cascaded assets.
- **POS-003**: The enriched audit event satisfies ANSSI SecNumCloud chapter 8 traceability requirements: auditors can answer "who deleted what, when, and how much was lost" without the cluster row existing.
- **POS-004**: UI confirmation gate with name-typing prevents accidental deletions — the most common destructive mistake.
- **POS-005**: No schema changes required for the deletion path itself; the `ON DELETE CASCADE` FKs already handle child cleanup.

### Negative

- **NEG-001**: The pre-deletion snapshot requires counting children across multiple tables before the DELETE executes. For very large clusters (thousands of pods), the count queries add latency to the delete call. Acceptable for a rare, admin-only operation.
- **NEG-002**: No undo. Once deleted, recovery requires re-collecting the cluster (re-add it to `LONGUE_VUE_COLLECTOR_CLUSTERS` and wait for the next poll tick). Curated metadata (owner, criticality, notes, runbook_url, annotations) is permanently lost.
- **NEG-003**: UI confirmation is bypassable via direct API call. This is by design (automation use-case), but means a mistyped `curl` from an admin can still drop a cluster.

### Neutral

- **NEU-001**: Other `DELETE` endpoints (nodes, namespaces, pods, etc.) are unchanged. They carry the same `delete` scope and are logged by the same audit middleware, but without the enriched cascade snapshot (their blast radius is smaller).

## Alternatives Considered

### Elevate cluster deletion to `admin` scope

- **ALT-001**: **Description**: Change `DELETE /v1/clusters/{id}` from `BearerAuth: [delete]` to `BearerAuth: [admin]`, preventing machine tokens from ever deleting clusters.
- **ALT-002**: **Rejection Reason**: The `delete` scope is already admin-exclusive. Switching to `admin` scope would break the scope model's symmetry and create a confusing precedent (some DELETEs need `delete`, one needs `admin`). If a future role needs `delete` without `admin`, that's a future ADR's problem.

### Soft-delete with grace period

- **ALT-003**: **Description**: Set `deleted_at` instead of hard-deleting. A background job purges after 7 days. Admin can "undelete" within the window.
- **ALT-004**: **Rejection Reason**: Conflicts with the collector's reconciliation loop, which would re-create or re-poll the cluster. Every query would need `WHERE deleted_at IS NULL` guards. Complexity not justified at current scale; the enriched audit snapshot provides the forensic value without the operational cost.

### Require a confirmation token from the API

- **ALT-005**: **Description**: `DELETE /v1/clusters/{id}` returns a `409 Conflict` with a one-time confirmation token; the caller must retry with `X-Confirm: <token>` within 60 seconds.
- **ALT-006**: **Rejection Reason**: Adds API complexity for a dubious gain — scripts that auto-delete would just blindly retry with the token. The UI confirmation dialog achieves the same goal for human users without complicating the API contract.

### Two-admin approval (four-eyes principle)

- **ALT-007**: **Description**: Cluster deletion requires approval from a second admin before executing.
- **ALT-008**: **Rejection Reason**: Argos is typically operated by small teams (1-3 admins). Requiring a second admin blocks single-admin installations entirely. The audit trail provides after-the-fact accountability; the UI confirmation gate provides before-the-fact friction. Four-eyes can be added as an optional policy in a future ADR if customers request it.

## Implementation Notes

- **IMP-001**: Add a `Store` method `CountClusterChildren(ctx, clusterID) (CascadeCounts, error)` that runs count queries against namespaces, nodes, PVs, pods, workloads, services, ingresses, and PVCs for the given cluster. Use a single round-trip with a multi-CTE query for efficiency.
- **IMP-002**: In the `DeleteCluster` handler (before calling `s.store.DeleteCluster`), fetch the cluster record and cascade counts. On successful deletion, attach the snapshot to the audit event's `details` JSONB. The audit middleware will need a mechanism for handlers to enrich the `details` field — e.g., a context key `audit.Details` that the middleware reads after the handler returns.
- **IMP-003**: The UI cluster detail page (`/ui/clusters/:id`) adds a "Delete cluster" button visible only to `admin` role. Clicking opens a confirmation modal showing cluster name, environment, owner, and child counts. The user must type the cluster name exactly to enable the "Delete" button.
- **IMP-004**: Integration test: create a cluster with namespaces, nodes, and pods; delete it as admin; verify 204, verify all children are gone, verify the audit event contains the cascade snapshot. Test as `editor` / `viewer` and verify 403.
- **IMP-005**: The existing `AuditMiddleware` already logs the `DELETE` as action `cluster.delete` with `resource_type: cluster` and `resource_id: <uuid>`. The enrichment in IMP-002 adds the `details` payload without changing the middleware's core logic.
- **IMP-006**: OpenAPI spec: no changes needed — `DELETE /v1/clusters/{id}` already exists with the correct scope and response codes.

## References

- **REF-001**: ADR-0007 — Human authentication, roles, and admin-issued machine tokens — `docs/adr/adr-0007-auth-and-rbac.md`
- **REF-002**: ADR-0008 — SecNumCloud chapter 8 asset management — `docs/adr/adr-0008-secnumcloud-chapter-8-asset-management.md`
- **REF-003**: ADR-0006 — UI for audit and curated metadata — `docs/adr/adr-0006-ui-for-audit-and-curated-metadata.md`
- **REF-004**: ANSSI SecNumCloud — Chapter 8 (asset management and traceability) — https://cyber.gouv.fr/enjeux-technologiques/cloud/
- **REF-005**: PostgreSQL ON DELETE CASCADE — https://www.postgresql.org/docs/current/ddl-constraints.html#DDL-CONSTRAINTS-FK
