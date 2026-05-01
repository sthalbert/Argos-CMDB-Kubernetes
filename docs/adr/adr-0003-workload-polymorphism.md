---
title: "ADR-0003: Workload polymorphism — one table with a kind discriminator"
status: "Proposed"
date: "2026-04-18"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "datamodel", "workloads"]
supersedes: ""
superseded_by: ""
---

# ADR-0003: Workload polymorphism — one table with a kind discriminator

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

The CMDB currently tracks `Cluster`, `Namespace`, `Node`, and `Pod`. The next Kubernetes layer longue-vue must catalogue is the **workloads** that own those pods: `Deployment`, `StatefulSet`, `DaemonSet`, and eventually `Job`, `CronJob`, `ReplicaSet`, `ReplicationController`.

These kinds share most of their CMDB-relevant metadata:

- name, parent namespace, labels, annotations
- desired replica count (or equivalent running-instance count)
- observed ready replicas
- a pod selector that binds the workload to its pods

They differ on a handful of kind-specific fields (`strategy` for Deployment, `service_name` and `volume_claim_templates` for StatefulSet, `update_strategy` for DaemonSet, `schedule` and `concurrency_policy` for CronJob, etc.). Those differences are real but far narrower than the shared surface.

A choice is required **before any workload code is written**:

1. Separate tables / endpoints / handlers per workload kind (e.g., `/v1/deployments`, `/v1/statefulsets`, …).
2. A single polymorphic `workloads` table / endpoint with a `kind` discriminator column, sharing columns for the common fields and a JSONB bag for kind-specific ones.
3. A hybrid: a `workloads` base table plus FK-linked per-kind detail tables.

longue-vue already commits to storing heterogeneous Kubernetes specs in JSONB when relational typing doesn't pay (ADR-0001, POS-004 / NEG-004). The workload kinds match that profile: most of what consumers want to query lives in the shared columns; the kind-specific fields are display / drill-down detail.

## Decision

Model all Kubernetes workload kinds in a **single `workloads` table with a `kind` discriminator**, paired with a single `/v1/workloads` endpoint group. The schema keeps the common fields as typed columns and the kind-specific spec as a JSONB column.

**Shared columns (v1):**

| Column             | Type         | Notes                                                                    |
|--------------------|--------------|--------------------------------------------------------------------------|
| `id`               | `uuid`       | Server-assigned primary key.                                             |
| `namespace_id`     | `uuid`       | FK → `namespaces(id)` with `ON DELETE CASCADE`.                          |
| `kind`             | `text`       | Enum-like. Immutable after creation. See kind list below.                |
| `name`             | `text`       | K8s workload name. Immutable after creation. DNS-subdomain pattern.      |
| `replicas`         | `integer`    | Desired replica count, nullable (DaemonSet has no scalar desired count). |
| `ready_replicas`   | `integer`    | Observed ready count, nullable until first status update.                |
| `labels`           | `jsonb`      | String key/value map. Defaults to `{}`.                                  |
| `spec`             | `jsonb`      | Kind-specific fields the collector chooses to persist. Defaults to `{}`. |
| `created_at`       | `timestamptz`| Server-assigned.                                                         |
| `updated_at`       | `timestamptz`| Server-assigned, refreshed on upsert.                                    |

**Natural key:** `(namespace_id, kind, name)`. Kubernetes allows a Deployment `web` and a StatefulSet `web` to coexist in the same namespace; the composite key reflects that without forcing the CMDB to disambiguate by name alone.

**Kind values accepted in v1:** `Deployment`, `StatefulSet`, `DaemonSet`. Canonical K8s casing (not lowercased) so the value echoes the `kind` field Kubernetes itself emits. Enum values for `Job`, `CronJob`, `ReplicaSet`, `ReplicationController` are added when their collectors land, without a schema migration.

**API shape:**

- `GET /v1/workloads` — list, cursor pagination, filterable by `?namespace_id=<uuid>` and `?kind=<enum>`.
- `POST /v1/workloads` — create; `kind` is required in the body.
- `GET|PATCH|DELETE /v1/workloads/{id}` — individual operations.

Scopes follow the established `read` / `write` / `delete` model.

**Cartography layer:** `applicative` (per ADR-0002). Set by the server on every response.

**Collector ingestion** (in a follow-up PR):

- `AppsV1().Deployments("").List()`, `AppsV1().StatefulSets("").List()`, `AppsV1().DaemonSets("").List()` — each mapped to `WorkloadInfo{Kind: "Deployment" | …}` and upserted through a single `UpsertWorkload` store method.
- Per-namespace reconcile on `(kind, name)` tuples so a deleted Deployment `web` doesn't wipe the still-live StatefulSet `web`.

## Consequences

### Positive

- **POS-001**: One endpoint, one handler set, one table, one set of tests. Adding Job/CronJob/ReplicaSet/ReplicationController becomes a new enum value plus a collector list call — no per-kind boilerplate.
- **POS-002**: Cross-kind queries are native SQL (`WHERE namespace_id = $1`) instead of UNIONs across three-to-seven tables.
- **POS-003**: Matches the heterogeneity strategy ADR-0001 already chose for the CMDB: typed columns for queryable shared fields, JSONB for kind-specific detail.
- **POS-004**: The `kind` discriminator is a column, not a URL segment, so `?kind=…` filters compose cleanly with other filters.
- **POS-005**: Workload → Pod relationships remain implicit via label selectors (not an FK) just like Kubernetes itself does it, keeping the model small.

### Negative

- **NEG-001**: Kind-specific fields inside `spec` JSONB are opaque to SQL integrity checks. Validation has to live in the API layer or in the collector's mapping code.
- **NEG-002**: The enum-like `kind` column uses `text` for forward compatibility rather than a PostgreSQL `ENUM`. Adding an unknown value is possible if the server is buggy; this is caught by the handler's input validation, not by the schema.
- **NEG-003**: Consumers expecting a strict URL-per-kind shape (`/v1/deployments`) will have to use `?kind=Deployment`. For an internal SecNumCloud-aligned CMDB this is acceptable; it would be heavier for a public API.
- **NEG-004**: Reconciliation is keyed on `(namespace_id, kind, name)`, so the store's `DeleteWorkloadsNotIn` signature is slightly heavier than the node / namespace / pod variants — it takes a list of `(kind, name)` tuples rather than a flat name list.

## Alternatives Considered

### One table, one endpoint per workload kind

- **ALT-001**: **Description**: Separate `deployments`, `statefulsets`, `daemonsets` tables, each with its own `/v1/deployments`, `/v1/statefulsets`, `/v1/daemonsets` handlers.
- **ALT-002**: **Rejection Reason**: 3× to 7× boilerplate for schema, store methods, handlers, tests, and collector plumbing. Cross-kind queries become UNIONs. A new workload kind means a new migration + new handler + new test file every time — the pattern doesn't scale as Kubernetes adds kinds.

### Single table with per-kind detail tables (classic table inheritance)

- **ALT-003**: **Description**: A `workloads` base table with shared columns plus FK-linked `workload_deployment_details`, `workload_statefulset_details`, etc., tables for kind-specific columns.
- **ALT-004**: **Rejection Reason**: Two-table reads and writes for every operation; the JSONB alternative captures the same kind-specific data with one table and no join. The structured typing would only earn its keep if auditors queried kind-specific columns directly, which SecNumCloud evidence exports don't do.

### Kind as a URL segment, still one table

- **ALT-005**: **Description**: Keep the unified table but expose it as `/v1/workloads/deployments`, `/v1/workloads/statefulsets`, etc.
- **ALT-006**: **Rejection Reason**: The URL segment mirrors data already carried in the `kind` column, coupling resource addressing to classification and making generic tooling (e.g., cross-kind dashboards) harder to build. Query parameters compose better.

### PostgreSQL `ENUM` for `kind`

- **ALT-007**: **Description**: Declare `kind` as `CREATE TYPE workload_kind AS ENUM ('Deployment', 'StatefulSet', 'DaemonSet')`.
- **ALT-008**: **Rejection Reason**: Adding a new enum value to a Postgres `ENUM` is a DDL change (`ALTER TYPE ... ADD VALUE`), so introducing `Job` later is a migration rather than a spec+code edit. A plain `text` column with API-layer validation keeps the schema stable across Kubernetes' own kind expansion.

## Implementation Notes

- **IMP-001**: Add a shared `WorkloadKind` enum to `api/openapi/openapi.yaml` (values `Deployment`, `StatefulSet`, `DaemonSet`) and a `Workload` schema referencing it. Mirror the existing Cluster/Node/Namespace/Pod shape (`WorkloadMutable` / `WorkloadCreate` / `WorkloadUpdate` / `Workload` / `WorkloadList` via `allOf`).
- **IMP-002**: Migration `00005_create_workloads.sql`: `id`, `namespace_id` FK with cascade, `kind TEXT`, `name TEXT`, `replicas INT`, `ready_replicas INT`, `labels JSONB`, `spec JSONB`, timestamps; indexes on `namespace_id`, `(kind, namespace_id)`, and `(created_at DESC, id DESC)`; `UNIQUE (namespace_id, kind, name)`.
- **IMP-003**: Store methods — `CreateWorkload`, `GetWorkload`, `ListWorkloads(namespaceID *uuid, kind *string, ...)`, `UpdateWorkload`, `DeleteWorkload`, `UpsertWorkload`, `DeleteWorkloadsNotIn(namespaceID, keep []struct{ Kind, Name string })`.
- **IMP-004**: Handlers set `layer=applicative` via `withWorkloadLayer`; add `LayerWorkload = Applicative` to `internal/api/layers.go`.
- **IMP-005**: Collector follow-up: `KubeClient.ListWorkloads` fans out three `AppsV1` list calls, folds the results into `[]WorkloadInfo`, and feeds them through `UpsertWorkload`. Reconcile by namespace using the `(kind, name)` tuple list built during the upsert pass.
- **IMP-006**: Kind validation lives in the handler: reject `POST /v1/workloads` bodies whose `kind` is not in the accepted enum with 400.
- **IMP-007**: This ADR is the contract — expanding to `Job` / `CronJob` / `ReplicaSet` / `ReplicationController` later is additive: new enum value in the spec, new collector list call, no schema change.

## References

- **REF-001**: ADR-0001 — CMDB for SNC using Kubernetes — `docs/adr/adr-0001-cmdb-for-snc-using-kube.md`
- **REF-002**: ADR-0002 — Kubernetes-to-ANSSI cartography layer mapping — `docs/adr/adr-0002-kubernetes-to-anssi-cartography-layers.md`
- **REF-003**: Kubernetes API reference — https://kubernetes.io/docs/reference/kubernetes-api/
- **REF-004**: PostgreSQL JSONB — https://www.postgresql.org/docs/current/datatype-json.html
