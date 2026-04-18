---
title: "ADR-0001: CMDB for SNC using Kubernetes"
status: "Proposed"
date: "2026-04-18"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "cmdb", "kubernetes", "go", "anssi"]
supersedes: ""
superseded_by: ""
---

# ADR-0001: CMDB for SNC using Kubernetes

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

**SNC** in this document refers to **SecNumCloud**, the ANSSI security qualification framework for trusted cloud offerings (IaaS / PaaS / SaaS) handling sensitive data and sensitive information systems. Qualified offerings receive the *Visa de sécurité de l'ANSSI*. The framework imposes technical, operational, and legal requirements — including cartography of the qualified perimeter — on cloud providers and on the sensitive systems operated within that perimeter. Authoritative reference: https://cyber.gouv.fr/enjeux-technologiques/cloud/.

The Kubernetes perimeter in scope of this project falls within a SecNumCloud-aligned environment, so its inventory must be catalogued with the rigour the SecNumCloud / ANSSI cartography requirements expect — for evidence packages, compliance audits, impact analysis, and incident response. The predecessor CMDB, **Mercator** (https://github.com/dbarzin/mercator), covers the ANSSI cartography layering for traditional IT assets (physical, logical, applicative, administration, ecosystem) but its data model does not represent Kubernetes-native objects — pods, deployments, services, helm releases, CRDs, nodes, namespaces, ingresses, persistent volumes — nor the relationships between them.

Manually maintaining these assets in Mercator is infeasible: Kubernetes workloads are dynamic (pods roll frequently) and hierarchical (helm release → chart → resources → pods → nodes → cluster).

A new CMDB — project codename **Argos** — is required with the following constraints:

- Written in Go, for performance, static-binary distribution, and first-class alignment with the Kubernetes ecosystem.
- Must preserve the ANSSI cartography layering model, extended with Kubernetes-specific entity types.
- Must expose an HTTP API supporting both read (GET) and write (PUSH) so external scripts, pipelines, and tools can integrate.
- Must be able to ingest data by querying a Kubernetes cluster directly when network reachability permits.
- Must persist the data model in a relational database.
- Cloud-hosted deployment is acceptable; no hard on-prem constraint. A SecNumCloud-qualified hosting target is a plausible future requirement and should not be designed out.
- Initial team size: one developer (solo project).

## Decision

Build **Argos**, a Go-based CMDB purpose-built for Kubernetes, to replace Mercator for Kubernetes-scoped inventory while preserving the ANSSI cartography model.

Foundational stack:

- **Language / Runtime**: Go (latest stable).
- **Database**: PostgreSQL. Relational integrity is required for the ANSSI layered model; JSONB columns absorb the heterogeneity of Kubernetes spec fields and CRDs without abandoning relational guarantees.
- **API style**: REST over HTTP, contract-first with an OpenAPI 3 specification. Any client — curl, Python, Go, shell — can push or query data using the published contract.
- **Kubernetes ingestion**: polling-based collector that invokes `kubectl` (or the equivalent Kubernetes REST API) against reachable clusters on a scheduled interval. Informer/watch-based ingestion is explicitly out of scope for v1.
- **External push clients**: language-agnostic — scripts in Python, Go, or shell POST to the REST API using the OpenAPI contract.
- **Data model**: inherits Mercator's ANSSI cartography layering and extends each layer with Kubernetes-native entity types (Cluster, Node, Namespace, Workload, Pod, Service, Ingress, HelmRelease, CRD, PersistentVolume, Secret metadata, …) and their relationships.

## Consequences

### Positive

- **POS-001**: ANSSI cartography compliance is preserved by keeping the layered model as the organising backbone.
- **POS-002**: Kubernetes assets become first-class — pods, helm releases, and workloads are representable without shoehorning them into generic CI types.
- **POS-003**: REST + OpenAPI lowers the integration barrier for ad-hoc scripts in any language (Python, Go, shell), matching the solo-maintainer workflow.
- **POS-004**: PostgreSQL with JSONB balances relational integrity (for ANSSI relationships) with flexibility (for arbitrary Kubernetes spec fields and CRD variability).
- **POS-005**: Go produces a single static binary, simplifying deployment in both cloud-hosted and restricted environments.
- **POS-006**: Polling ingestion is simpler to operate, debug, and reason about than a watch/informer pipeline — appropriate for a one-person team.

### Negative

- **NEG-001**: Polling introduces staleness: the CMDB reflects cluster state as of the last poll, not real-time. Live incidents must still consult the cluster directly.
- **NEG-002**: Building a bespoke CMDB concentrates the maintenance burden (schema evolution, API versioning, migrations) on a single developer.
- **NEG-003**: Parallel operation with Mercator during transition creates a dual-source-of-truth risk until non-Kubernetes assets are migrated or partitioned by scope.
- **NEG-004**: JSONB fields enable schema drift; API-layer validation and schema linting are required to keep the data model from degrading into opaque blobs.
- **NEG-005**: Solo ownership is a bus-factor risk; documentation and ADRs must compensate.

## Alternatives Considered

### Extend Mercator to support Kubernetes

- **ALT-001**: **Description**: Fork Mercator (PHP/Laravel) and add Kubernetes entity types and collectors to its existing data model.
- **ALT-002**: **Rejection Reason**: Mercator's data model is not designed for the cardinality, hierarchy, or dynamism of Kubernetes objects. Forking diverges from upstream and locks the project into a PHP stack misaligned with the Kubernetes ecosystem, which is Go-native.

### Adopt an existing Kubernetes inventory/catalog tool (Backstage, Kubeview, Steampipe)

- **ALT-003**: **Description**: Use an off-the-shelf tool that catalogs Kubernetes resources and layer ANSSI metadata on top.
- **ALT-004**: **Rejection Reason**: None of these tools are designed around the ANSSI cartography model. Retrofitting ANSSI compliance onto their data models is equivalent to rebuilding it, without the freedom to shape it to SNC's needs.

### Graph database (Neo4j, ArangoDB) instead of PostgreSQL

- **ALT-005**: **Description**: Model the CMDB as a property graph to natively express Kubernetes relationships (owner references, helm → resources, pod → node).
- **ALT-006**: **Rejection Reason**: Operational complexity and skill overhead outweigh the modelling benefit for a solo project. PostgreSQL with recursive CTEs and JSONB is sufficient for the expected query patterns; a graph backend can be revisited in a future ADR if relationship traversal becomes a bottleneck.

### gRPC or GraphQL API instead of REST/OpenAPI

- **ALT-007**: **Description**: Expose the CMDB via gRPC (strong typing, code generation) or GraphQL (flexible querying).
- **ALT-008**: **Rejection Reason**: REST + OpenAPI is the lowest common denominator for ad-hoc scripts in shell, Python, and Go — aligned with the stated integration goal. gRPC adds tooling burden; GraphQL adds query complexity. Neither is justified for v1.

### Informer/watch-based ingestion instead of polling

- **ALT-009**: **Description**: Use client-go informers to react to cluster events in near real-time.
- **ALT-010**: **Rejection Reason**: Higher implementation and operational cost (long-lived watches, resync logic, reconnection handling). Polling meets the CMDB freshness requirement and is revisitable once v1 is stable.

## Implementation Notes

- **IMP-001**: Define the OpenAPI 3 specification before implementation; generate server stubs and client SDKs from it to enforce the contract end-to-end.
- **IMP-002**: Model the PostgreSQL schema with an explicit `layer` enum matching the ANSSI cartography layers; every Kubernetes entity type belongs to exactly one layer. Reserve JSONB for fields that have no cross-entity relationships.
- **IMP-003**: Run the ingestion collector as a scheduled job (cron or internal scheduler) that invokes `kubectl` or the Kubernetes REST API; each collection produces a versioned snapshot so history and diffs can be reconstructed.
- **IMP-004**: Authentication on the push API starts with API tokens; revisit (mTLS or OIDC) once multi-writer or multi-tenant scenarios arise.
- **IMP-005**: Capture follow-up decisions in separate ADRs: (a) concrete Kubernetes-to-ANSSI entity mapping, (b) authentication and authorization model, (c) multi-cluster collector topology, (d) snapshot and versioning strategy.
- **IMP-006**: v1 success criteria: ingest a single Kubernetes cluster on a schedule, persist all core resource kinds, expose them via GET endpoints, and accept POST for at least one non-Kubernetes asset type to demonstrate push parity.

## References

- **REF-001**: ANSSI — SecNumCloud / cloud qualification (authoritative) — https://cyber.gouv.fr/enjeux-technologiques/cloud/
- **REF-002**: ANSSI — SecNumCloud qualification FAQ — https://cyber.gouv.fr/enjeux-technologiques/cloud/faq-qualification-secnumcloud/
- **REF-003**: Mercator CMDB (predecessor) — https://github.com/dbarzin/mercator
- **REF-004**: ANSSI cartography — community guide (non-authoritative introduction to the five-layer model) — https://my-carto.com/blog/cartographie-anssi-cybersecurite/
- **REF-005**: OpenAPI 3.1 specification — https://spec.openapis.org/oas/v3.1.0
- **REF-006**: Kubernetes API reference — https://kubernetes.io/docs/reference/kubernetes-api/
