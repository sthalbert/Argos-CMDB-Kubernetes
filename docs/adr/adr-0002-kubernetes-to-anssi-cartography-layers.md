---
title: "ADR-0002: Kubernetes-to-ANSSI cartography layer mapping"
status: "Proposed"
date: "2026-04-18"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "anssi", "cartography", "datamodel"]
supersedes: ""
superseded_by: ""
---

# ADR-0002: Kubernetes-to-ANSSI cartography layer mapping

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

ADR-0001 committed Argos to preserving the ANSSI cartography layering for classical IT assets. As of today the Argos data model has three Kubernetes-native entities (`Cluster`, `Node`, `Namespace`) but **none of them declares which ANSSI cartography layer it belongs to**. Without this information:

- The inventory cannot be exported in a form the SecNumCloud evidence package expects (auditors receive asset lists grouped by layer).
- Cross-entity filters such as "show every asset in the *infrastructure* layer" cannot be expressed.
- Follow-up work (Pods, Services, HelmReleases, …) has no discipline to follow — each entity risks a different maintainer's opinion on where it belongs.

ANSSI cartography is structured in six layers. The French canonical names and the English identifiers we adopt in the API are:

| English identifier        | French (ANSSI)                | Scope                                                            |
|---------------------------|-------------------------------|------------------------------------------------------------------|
| `ecosystem`               | Écosystème                    | External partners, internet-facing interactions, third parties   |
| `business`                | Métier                        | Business processes, missions, activities                         |
| `applicative`             | Applicatif                    | Applications, services, software assets                          |
| `administration`          | Administration                | Administrative / ops tooling managing the SI                     |
| `infrastructure_logical`  | Infrastructure logique        | Logical / virtual infrastructure (OS, VMs, containers, DBs)      |
| `infrastructure_physical` | Infrastructure physique       | Physical infrastructure (hardware, network devices, datacenter)  |

The CMDB needs a stable, documented mapping from Kubernetes kinds onto those layers.

## Decision

Argos adopts the six-layer ANSSI model unchanged and decorates every inventory entity with a **`layer`** attribute carrying one of those six values.

**Mapping for the v1 entities:**

| Entity      | Layer                     | Rationale                                                                                       |
|-------------|---------------------------|-------------------------------------------------------------------------------------------------|
| `Cluster`   | `infrastructure_logical`  | A Kubernetes cluster is an abstraction over compute; it is software infrastructure, not metal. |
| `Namespace` | `infrastructure_logical`  | Purely a logical partitioning primitive inside a cluster.                                      |
| `Node`      | `infrastructure_physical` | Represents the compute substrate (host). We classify nodes as physical by default; a future `node_kind` field may distinguish physical hosts from virtual hosts. |

**Mapping roadmap for entities not yet in the spec** (informational, to be confirmed when each lands):

| Future entity kind                               | Layer                    |
|--------------------------------------------------|--------------------------|
| Workloads (Deployment / StatefulSet / DaemonSet) | `applicative`            |
| Pod                                              | `applicative`            |
| Service                                          | `applicative`            |
| Ingress                                          | `applicative`            |
| ConfigMap, Secret (metadata only)                | `applicative`            |
| PersistentVolume                                 | `infrastructure_logical` |
| PersistentVolumeClaim                            | `applicative`            |
| CustomResourceDefinition, RBAC                   | `administration`         |
| HelmRelease                                      | `applicative`            |

**Storage and API shape:**

- `layer` is **derived**, not stored. It is a pure function of the entity's type. Every GET / POST / PATCH response includes it; the server ignores it on input. No new database column, no migration.
- The OpenAPI spec exposes `layer` as a `readOnly` enum with the six values above. The schema is shared so every entity references the same enum.
- A future per-instance override (needed only if we decide Node instances can be tagged physical vs virtual) would be an additive change: new mutable field, possibly persisted, without breaking the derived default.

## Consequences

### Positive

- **POS-001**: SecNumCloud evidence exports can be grouped by layer directly from API responses — no post-processing in the auditor's workflow.
- **POS-002**: Layer taxonomy is pinned to the six ANSSI layers, so Argos-managed Kubernetes data composes with the rest of SNC's inventory without a translation step.
- **POS-003**: Derived classification has zero schema cost. A new entity kind only needs a one-line mapping in the handler, and the implication is enforced at the type boundary.
- **POS-004**: Readonly field semantics prevent clients from mis-classifying assets and drifting the inventory.

### Negative

- **NEG-001**: Per-instance override (e.g., "this particular node is a VM, not physical hardware") is not supported in v1. Every `Node` row reads back as `infrastructure_physical`.
- **NEG-002**: The ANSSI (French) → Argos layer names differ linguistically. Consumers that pivot between the two must own the translation table (documented above).
- **NEG-003**: Cross-entity "list everything in layer X" queries are not available in v1 — they would require either a materialised view or UNION ALL across tables. Out of scope here; revisit when the kind inventory grows.

## Alternatives Considered

### Store `layer` as a persisted column on every entity

- **ALT-001**: **Description**: Add a `layer` column to `clusters`, `nodes`, `namespaces` and populate it on every INSERT/UPDATE. Let clients override.
- **ALT-002**: **Rejection Reason**: The layer is an invariant of the entity kind in v1; persisting it duplicates information and invites drift (two nodes in the same cluster with different layer values). The derived approach is smaller and enforced by construction.

### Collapse to fewer layers (e.g., a 5-layer variant)

- **ALT-003**: **Description**: Merge `infrastructure_logical` and `infrastructure_physical` under a single `infrastructure` layer.
- **ALT-004**: **Rejection Reason**: The distinction is load-bearing for compliance: ANSSI audits frequently ask where the *physical* boundary of sensitive processing lies. Losing the split forces every downstream consumer to re-derive it.

### Let clients set `layer` freely on create/update

- **ALT-005**: **Description**: Make `layer` a writable field on each entity.
- **ALT-006**: **Rejection Reason**: Opens the door to inconsistent tagging across instances of the same kind and contradicts the cartography's role as an authoritative classification. Read-only in v1 keeps the data model honest; a targeted mutable sub-field (e.g., `node_kind: physical|virtual`) is preferable when per-instance nuance is genuinely needed.

### Use French ANSSI names as enum values

- **ALT-007**: **Description**: Use `ecosysteme`, `metier`, `applicatif`, `administration`, `infrastructure_logique`, `infrastructure_physique` in JSON.
- **ALT-008**: **Rejection Reason**: The rest of the Argos codebase (identifiers, error messages, logs) is in English. Keeping the enum in English matches that convention; the ADR documents the mapping back to the French reference.

## Implementation Notes

- **IMP-001**: Add a shared `Layer` schema to `api/openapi/openapi.yaml` (enum of the six identifiers above) and reference it from the `Cluster`, `Node`, and `Namespace` schemas as a `readOnly` field required on responses.
- **IMP-002**: Regenerate `internal/api/api.gen.go`.
- **IMP-003**: Every handler that returns one of the three entity types sets `layer` to the constant appropriate for its kind. Package-level constants (`LayerCluster`, `LayerNode`, `LayerNamespace`) centralise the mapping.
- **IMP-004**: Store-layer code is unchanged: layer is assembled in the handler, not persisted. PG round-trip tests therefore do not need updating for this concern.
- **IMP-005**: Follow-up ADR when a per-instance override is needed (likely first for nodes, to separate physical from virtual hosts).
- **IMP-006**: When a new Kubernetes kind lands, add it to the mapping table here before implementing the handler, so the decision is reviewed with the same framing.

## References

- **REF-001**: ADR-0001 — CMDB for SNC using Kubernetes — `docs/adr/adr-0001-cmdb-for-snc-using-kube.md`
- **REF-002**: ANSSI — Cloud / SecNumCloud — https://cyber.gouv.fr/enjeux-technologiques/cloud/
- **REF-003**: ANSSI cartography — community guide — https://my-carto.com/blog/cartographie-anssi-cybersecurite/
