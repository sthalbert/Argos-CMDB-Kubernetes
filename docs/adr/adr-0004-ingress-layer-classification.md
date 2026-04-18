---
title: "ADR-0004: Ingress layer classification — applicative, with future ecosystem linkage"
status: "Proposed"
date: "2026-04-18"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "anssi", "cartography", "ingress"]
supersedes: ""
superseded_by: ""
---

# ADR-0004: Ingress layer classification — applicative, with future ecosystem linkage

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

ADR-0002 decided the ANSSI cartography layer for every entity Argos catalogues and published a roadmap table listing `Ingress → applicative (with ecosystem boundary semantics)`. The parenthetical deliberately left the door open: Ingress is the only Kubernetes kind whose entire reason for existing is to bridge the cluster to external traffic, and a sharp reading of the ANSSI model could argue it belongs in the `ecosystem` layer rather than `applicative`.

This ADR resolves the question before any Ingress code lands, so the walking skeleton doesn't bake in a classification we'd have to retrofit.

**What the two layers actually mean** (per ADR-0002 + Mercator):

- `applicative` — applications and services internal to the information system. Deployed software, managed by application teams, owned inside the organisation.
- `ecosystem` — external entities interacting with the information system: the internet at large, partners, suppliers, customer portals. Things *on the other side* of the IS boundary.

**Where Ingress fits:** an Ingress is a Kubernetes API object — authored by the same teams that author Deployments and Services, managed through the same RBAC, stored in the same etcd. Its *purpose* is boundary traversal, but it itself is not an external actor. The traffic it routes comes from the ecosystem; the Ingress is the gateway that lives on the internal side of that boundary.

**What ANSSI audits actually ask about this kind of asset:** "What endpoints are reachable from the internet?" is a frequent question, and it's the reason the parenthetical in ADR-0002 wavered. But that question is best answered with a **relationship** ("which Ingress routes to which ecosystem entity") or a **flag** ("is this Ingress internet-exposed?"), not by forcing the Ingress itself out of the `applicative` layer where it otherwise cleanly belongs.

## Decision

**Ingress is classified in the `applicative` layer.** This matches ADR-0002's roadmap and the Mercator convention that reserves `ecosystem` for external entities, not for the internal objects that touch them.

**Implications for future work** (scoped here, not built in this ADR):

- When Argos eventually models the `ecosystem` layer, it does so with a **separate entity kind** — e.g., `ExternalEndpoint` or similar — representing the real external actors: "the public internet", "partner X's callback API", "customer portal domain foo.example.com", etc.
- An Ingress-to-ExternalEndpoint relationship then answers the "what's internet-exposed?" audit question explicitly, rather than through a layer assignment that is really a proxy for that relationship.
- As a simpler interim signal, a future schema revision of Ingress may carry an optional `internet_exposed` boolean (set by the collector based on `status.loadBalancer.ingress` presence and ingress-class configuration). Out of scope for v1.

**Schema shape for the Ingress entity itself** (detailed in the implementation PR):

- Typed columns: `namespace_id`, `name`, `ingress_class_name`.
- JSONB columns: `rules` (host/path/backend triples), `tls` (hosts + secret names), `labels`.
- Natural key `(namespace_id, name)`, same as Service. FK `namespace_id → namespaces(id) ON DELETE CASCADE`.
- Readonly `layer: "applicative"` set by the server per ADR-0002.

## Consequences

### Positive

- **POS-001**: Layer classification stays consistent with how the ANSSI / Mercator model uses `ecosystem`: for external actors, not for bridges to them. Auditors reading Argos output see a taxonomy that matches their mental model.
- **POS-002**: Keeps every Kubernetes-native kind inside `applicative` / `infrastructure_*` / `administration`. The `ecosystem` layer stays reserved for the work that genuinely populates it when the CMDB grows beyond K8s.
- **POS-003**: Future "what's internet-exposed?" queries are answered by an explicit relationship or flag — a stronger, more auditable signal than a layer label that mixes classification with exposure.
- **POS-004**: Implementation matches the existing Service pattern (same natural key shape, same FK cascade, same handler scaffolding). No new architectural concepts required for the walking skeleton.

### Negative

- **NEG-001**: `ecosystem` gets no entity in v1 of Argos. The layer remains populated only when the broader cartography (external endpoints, partners) is modelled — a later milestone.
- **NEG-002**: Operators who expect `GET /v1/ingresses?layer=ecosystem` to list internet-facing assets won't get that answer from layer alone. Documentation must spell out the intended query (filter or future flag) once Ingress lands.
- **NEG-003**: Future classification of resources that genuinely straddle layers (e.g., Gateway API resources, EndpointSlice bridging external services) will likely revisit this ADR. That's acceptable: the rule "ecosystem is for external actors" gives those revisits a consistent starting point.

## Alternatives Considered

### Classify Ingress as `ecosystem`

- **ALT-001**: **Description**: Put Ingress in `ecosystem` because its primary purpose is external traffic ingestion. It would be the first and only K8s kind assigned to that layer.
- **ALT-002**: **Rejection Reason**: Conflates classification (what kind of asset) with exposure (whether it's reachable externally). An Ingress managed entirely inside the organisation, routing only between internal namespaces, is no more "ecosystem" than a Service. The more useful answer for audits is a relationship or flag, not a layer.

### Split-classification: primary layer plus a secondary "boundary" tag

- **ALT-003**: **Description**: Allow an entity to carry both a primary layer and a set of secondary layers (e.g., Ingress = `applicative` + boundary of `ecosystem`).
- **ALT-004**: **Rejection Reason**: Adds a second classification dimension that every consumer then has to understand. ADR-0002 already committed to single-layer classification for clarity. The audit use case is better served by explicit relationships — modelled once — than by a taxonomy multiplier everyone pays for.

### Defer the classification until Ingress is implemented

- **ALT-005**: **Description**: Ship Ingress in a PR, use `applicative` by convention, and revisit if audits demand it.
- **ALT-006**: **Rejection Reason**: The question was explicitly flagged in ADR-0002 and deserves a clean resolution before code. Deferring means the first Ingress implementation PR carries an implicit decision without review — the opposite of what ADRs exist for.

## Implementation Notes

- **IMP-001**: In `internal/api/layers.go`, add `LayerIngress = Applicative` alongside the existing constants and a `withIngressLayer` decorator. Apply on every Ingress handler response path.
- **IMP-002**: OpenAPI spec additions mirror the Service shape: new `ingresses` tag, `/v1/ingresses` and `/v1/ingresses/{id}` endpoints with `read` / `write` / `delete` scopes, cursor pagination, `?namespace_id=` filter, allOf `IngressMutable` / `IngressCreate` / `IngressUpdate` / `Ingress` / `IngressList`. `namespace_id` + `name` immutable post-creation.
- **IMP-003**: Migration `00007_create_ingresses.sql`: UUID PK, `namespace_id` FK with `ON DELETE CASCADE`, `(namespace_id, name)` unique, `ingress_class_name TEXT`, `rules JSONB`, `tls JSONB`, `labels JSONB`, timestamps, usual indexes.
- **IMP-004**: Collector follow-up: `KubeClient.ListIngresses` via `NetworkingV1().Ingresses("").List`; `IngressInfo` DTO capturing `ingress_class_name`, a flattened rules array, and a flattened tls array; per-namespace reconcile via `DeleteIngressesNotIn`.
- **IMP-005**: No schema field for `internet_exposed` in the first Ingress PR. A later revision (separate ADR if the logic becomes non-trivial) will add it together with collector heuristics.
- **IMP-006**: When the `ecosystem` layer is populated (probably with an `ExternalEndpoint` entity), revisit this ADR to describe the Ingress → ExternalEndpoint relationship and whether to mark it explicitly in the data model.

## References

- **REF-001**: ADR-0001 — CMDB for SNC using Kubernetes — `docs/adr/adr-0001-cmdb-for-snc-using-kube.md`
- **REF-002**: ADR-0002 — Kubernetes-to-ANSSI cartography layer mapping — `docs/adr/adr-0002-kubernetes-to-anssi-cartography-layers.md`
- **REF-003**: ADR-0003 — Workload polymorphism — `docs/adr/adr-0003-workload-polymorphism.md`
- **REF-004**: Kubernetes Ingress API reference — https://kubernetes.io/docs/concepts/services-networking/ingress/
- **REF-005**: ANSSI — Cloud / SecNumCloud — https://cyber.gouv.fr/enjeux-technologiques/cloud/
