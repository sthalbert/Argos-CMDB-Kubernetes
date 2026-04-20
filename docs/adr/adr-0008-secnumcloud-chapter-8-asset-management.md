---
title: "ADR-0008: Asset-management data model for SecNumCloud v3.2 chapter 8"
status: "Proposed"
date: "2026-04-20"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "compliance", "snc", "data-model", "cmdb"]
supersedes: ""
superseded_by: ""
---

# ADR-0008: Asset-management data model for SecNumCloud v3.2 chapter 8

## Status

**Proposed**

## Context

Argos exists to be the Kubernetes-scoped CMDB for a SecNumCloud (SNC)
provider (see ADR-0001). Chapter 8 of the SNC v3.2 référentiel —
*Gestion des actifs* — is the clause that most directly shapes what
the CMDB must store and expose. A gap audit against the published text
(clauses 8.1 through 8.5, v3.2 of 2022-03-08) surfaced concrete
data-model deficiencies that `v0.1.0` does not yet cover.

The clauses and their Argos status as of `v0.1.0` "Canopus":

- **8.1.a — Equipment inventory**: each equipment must carry
  identification (names, IPs, MACs), function, **model**, **location**,
  **owner**, and **security need** (§8.3). Argos covers the first two
  (Mercator-aligned fields on Nodes; `layer` per ADR-0002;
  `workload.kind` per ADR-0003). Model / location / owner are
  **partial or absent** — `cluster.region` + `node.zone` cover cloud
  location only, owner landed on Cluster only (PR #48), and bare-metal
  hardware model has no column.
- **8.1.b — Software inventory with version + installed equipment**:
  containerised software is covered by the `containers` JSONB on Pods
  and Workloads; node-level software (kubelet, kube-proxy, container
  runtime, OS image, kernel) is covered by the Mercator enrichment on
  Nodes. Pod→Node mapping is served by `GET /v1/pods?node_name=…`.
  This clause is **largely satisfied**; only non-Kubernetes software
  (hypervisor / firmware / storage appliances) is out of scope, which
  is acceptable because Argos is the Kubernetes-scoped CMDB — other
  inventories remain in the parent Mercator instance.
- **8.1.c — License validity**: **not modelled** in Argos, and **will
  not be**. Software licensing is handled by
  [Dependency-Track](https://dependencytrack.org/) as the authoritative
  SBOM + license-compliance system. Argos defers to Dependency-Track
  via image reference (the existing `containers[].image` value is the
  join key), it does not duplicate the license field locally.
- **8.2 — Restitution des actifs**: out of technical scope; procedural
  HR control for on/offboarding staff.
- **8.3 — Identification des besoins de sécurité**: the référentiel
  requires the provider to identify per-service security needs. In the
  Mercator data model — which Argos aligns to per ADR-0001 — DICT
  classifications live on the **Application** entity (see
  [Mercator applications view](https://dbarzin.github.io/mercator/model/#applications-view)),
  **not** on every physical or logical asset. Argos has two abstractions
  that map to Mercator's Application: the **Namespace** (tenant-style
  grouping, "application = namespace" view from ADR-0006) and the
  **Workload** (deployed unit, "application = workload" view). DICT
  storage must land on those two kinds only.
- **8.4 — Marquage et manipulation** (RECOMMANDÉ, not obligatoire):
  labels / annotations can carry marking metadata; partial.
- **8.5 — Supports amovibles**: out of scope (physical media procedure).

The actionable gaps — i.e. work that requires code — are **8.1.a
missing fields on non-Cluster kinds** and **8.3 structured security-need
storage scoped to the Application abstractions**. The license clause
(8.1.c) is closed by an external-system reference; the procedural
clauses are called out explicitly so an assessor does not look for
them in the CMDB.

## Decision

Extend the asset data model along three axes, documented under this ADR
and implemented as a follow-up PR series:

1. **Propagate the curated-metadata pattern** (`owner`, `criticality`,
   `notes`, `runbook_url`, `annotations`) from Cluster (PR #48) to
   every other durable asset kind: **Namespace**, **Node**, **Workload**.
   Pods / Services / Ingresses / PVs / PVCs are **excluded** — they
   are either ephemeral (pods are reaped by the collector) or derived
   (a Service's ownership is that of its Workload). This closes the
   §8.1.a *owner* requirement across the kinds that are stable enough
   for a human to annotate. Note: `criticality` here is the
   **operational** tier (critical / high / medium / low for paging /
   change-management purposes); it is **distinct** from the §8.3
   data-classification DICT values introduced below, which describe
   the *information* handled by the application.

2. **Add structured DICT security-need fields at the Application
   level**, following the ANSSI EBIOS-RM convention and the Mercator
   applications view. DICT stands for **D**isponibilité /
   **I**ntégrité / **C**onfidentialité / **T**raçabilité. Concretely:
   - Four integer columns `sec_disponibilite`, `sec_integrite`,
     `sec_confidentialite`, `sec_tracabilite`, each in the range 0..4
     (nullable = "not classified"). The 0..4 scale matches EBIOS-RM's
     default.
   - A sibling `sec_notes` TEXT column carries free-form justification.
   - These columns land on **Namespace** and **Workload** only. They
     **do not** land on Cluster, Node, Service, Ingress, PV, PVC or
     Pod. A cluster is a substrate, not an application; a node is
     infrastructure; the rest are either a Workload's internals or
     ephemeral. If an operator needs a cluster-wide classification,
     they classify each Namespace (they almost certainly want
     different values per tenant anyway).

3. **Add missing 8.1.a columns on existing kinds**: `location` (TEXT,
   free-form site / datacentre descriptor) on Cluster; `hardware_model`
   (TEXT) on Node so bare-metal installs can record a server model
   alongside the existing `instance_type` (which stays the
   well-known-label-derived value for cloud VMs). Additive; the
   collector never writes them — same invariant as PR #48.

**Out of scope for Argos altogether**:

- License validity (§8.1.c): owned by Dependency-Track; Argos exposes
  the `containers[].image` reference that Dependency-Track uses as
  the join key. The UI may link out to the Dependency-Track project
  for a given image but stores no license state.
- Restitution des actifs (§8.2): HR/procedural.
- Supports amovibles (§8.5): physical-media procedural.

## Consequences

### Positive

- **POS-001**: DICT lands where Mercator puts it — at the Application
  abstraction — so an SNC assessor used to Mercator can point at the
  same conceptual row without re-learning a bespoke Argos taxonomy.
  The two Argos Application abstractions (Namespace and Workload) are
  both already first-class entities with detail pages; no new kind
  needed.
- **POS-002**: Keeping DICT off Cluster / Node avoids the
  anti-pattern of pushing data-classification onto infrastructure —
  which would force a cluster to carry a single classification summing
  every tenant's, and a node to carry the max classification of every
  workload it might schedule. Those aggregates are derivable at query
  time if ever needed.
- **POS-003**: Operational `criticality` and data-classification DICT
  are separated explicitly. An operator can mark a Workload as
  operationally critical (wake someone at 3am) without claiming it
  processes confidential data — and vice versa.
- **POS-004**: License tracking stays in Dependency-Track where the
  SBOM workflow and vulnerability context already live. Argos does
  not duplicate, does not drift, and does not invite the "whose value
  is canonical?" question an assessor would otherwise ask.
- **POS-005**: Security-need columns are sibling to curated metadata
  (same merge-patch semantics, same collector-never-writes invariant,
  same UI pattern on detail pages). Consistency reduces reviewer
  cognitive load.

### Negative

- **NEG-001**: Four migrations on top of the existing ones — still
  non-trivial but smaller than the original plan (license fields
  dropped; DICT confined to two kinds instead of four).
- **NEG-002**: DICT values are pure metadata: Argos will not enforce
  any access or handling policy based on them (that would require a
  full data-classification engine, out of scope). A high
  `confidentialité` value on a Workload documents intent; it does not
  restrict who can read the CMDB row.
- **NEG-003**: The ANSSI 0..4 scale is a convention, not a hard
  standard within SNC itself; customers using ISO 27005 equivalents
  (e.g. 1..5 or TLP) will translate. `sec_notes` is the escape hatch.
- **NEG-004**: A cluster's aggregate classification is not stored, so
  "show me every cluster hosting any Workload classified C≥3" is a
  JOIN through namespaces → workloads. The query is straightforward
  but slightly less ergonomic than a denormalised cluster column.
  Denormalisation can land later if reporting needs it.

## Alternatives Considered

### DICT on every durable kind

- **ALT-001**: **Description**: The original draft of this ADR put
  DICT on Cluster, Namespace, Node, Workload.
- **ALT-002**: **Rejection Reason**: Contradicts how Mercator —
  Argos's reference model per ADR-0001 — positions DICT. Mercator
  places classification on the Application entity; replicating it on
  infrastructure (Cluster, Node) forces synthetic summaries and does
  not match what an SNC assessor expects to see. Operator feedback
  flagged this during review of the ADR's first iteration.

### Free-form labels / annotations only

- **ALT-003**: **Description**: Punt every new field to the existing
  `labels` / `annotations` JSONB columns. No schema change; customers
  slot in `sec.disponibilite=3`, `owner=team-platform`, etc. as string
  keys.
- **ALT-004**: **Rejection Reason**: Makes SNC assessment materially
  harder — an assessor cannot rely on a schema contract and must
  inspect every row's JSON to confirm coverage. Breaks typed filtering
  (`GET /v1/workloads?criticality=critical` becomes an ILIKE over a
  JSONB blob). Loses UI affordance for structured editing. PR #48
  already rejected this shape for the Cluster curated-metadata fields
  for the same reasons.

### Single `classification` JSONB column per application

- **ALT-005**: **Description**: One JSONB column `classification` on
  Namespace and Workload holding the DICT values as object keys
  (`{D:3, I:2, C:4, P:2}`).
- **ALT-006**: **Rejection Reason**: Hides the fact that four distinct
  attributes are being tracked. Typed columns enable per-axis indexes
  (`CREATE INDEX ON workloads (sec_confidentialite) WHERE
  sec_confidentialite >= 3`) which matter for reporting queries like
  "list every application with confidentialité ≥ 3". Also costs a
  separate JSONB path expression on every read. Typed columns are
  four integers and cost nothing.

### Dedicated `asset_classification` table

- **ALT-007**: **Description**: One sidecar table keyed on
  `(kind, asset_id)` holding the DICT values and a history of
  revisions.
- **ALT-008**: **Rejection Reason**: Adds a join on every list /
  detail read for no measurable benefit at `v0.1` scale.
  Classification history is a real want, but it belongs in the future
  snapshots / time-travel work (ADR-0001 roadmap), not as a
  special-case sidecar. Can be layered on top of the columnar values
  later.

### Track licenses inside Argos

- **ALT-009**: **Description**: Add `license` / `license_expires_at`
  fields to the `containers` JSONB entries on Pods and Workloads (the
  original draft of this ADR proposed this).
- **ALT-010**: **Rejection Reason**: Dependency-Track is the system of
  record for SBOM + license compliance at the target deployment.
  Duplicating the field in Argos invites drift ("which value is
  canonical when they disagree?") and pulls Argos into SBOM territory
  it was never meant to cover. The `containers[].image` reference is
  sufficient as the join key between the two systems.

### Map DICT into the existing `criticality` free-text column

- **ALT-011**: **Description**: Keep `criticality` as-is and document
  "valid values are `D<n>I<n>C<n>P<n>` strings".
- **ALT-012**: **Rejection Reason**: `criticality` carries operational
  tiering (critical / high / medium / low in PR #48); it's orthogonal
  to data-classification. Reusing the column conflates two axes and
  forces a UI that asks operators to encode DICT in a string.

## Implementation Notes

- **IMP-001**: **Migration order**: one migration per kind, stacked
  so a half-applied state still compiles. Proposed:
  - `00019_namespace_curated_metadata.sql` — owner / criticality /
    notes / runbook_url / annotations on namespaces.
  - `00020_node_curated_metadata.sql` — same five, plus
    `hardware_model` on nodes.
  - `00021_workload_curated_metadata.sql` — same five on workloads.
  - `00022_cluster_location.sql` — `location` column on clusters.
  - `00023_application_security_classification.sql` — DICT (4 × INT)
    + `sec_notes` on **namespaces and workloads only**.

- **IMP-002**: **Collector invariant**: same as PR #48 —
  `UpdateNamespace` / `UpdateNode` / `UpdateWorkload` use merge-patch;
  the collector's per-tick patches set only the fields it actually
  derives from the Kubernetes API (name, labels, image, version …) so
  operator-owned fields are preserved by omission. Integration tests
  must pin this invariant for each kind, mirroring
  `TestPGClusterCuratedMetadata`.

- **IMP-003**: **OpenAPI shape**: extend `NamespaceMutable`,
  `NodeMutable`, `WorkloadMutable` with the curated fields; add DICT
  fields to `NamespaceMutable` and `WorkloadMutable` only. Flatten
  (no nested `classification` object) so PATCH bodies stay simple and
  match how `criticality` / `owner` already sit directly on the
  parent schema.

- **IMP-004**: **UI**: promote the "Ownership & context" card pattern
  from `ClusterDetail` to `NamespaceDetail`, `NodeDetail`,
  `WorkloadDetail`. On Namespace and Workload detail pages, add a
  second "Classification (DICT)" card below it with four numeric
  selectors (0..4) and the `sec_notes` textarea. Read-only for viewer
  / auditor; editable for editor / admin, same role gating as PR #48.
  Node detail gets the curated card but **no** classification card.
  Cluster detail gets a `location` field in its existing card; no
  classification there either.

- **IMP-005**: **Audit trail**: changes to classification fields land
  in `audit_events` automatically — the audit middleware (ADR-0007
  follow-up) already records every PATCH with a scrubbed body. The
  body diff will include the before/after DICT values, which is
  exactly what an SNC assessor needs to verify §8.3 traceability.

- **IMP-006**: **Dependency-Track integration for §8.1.c**: document
  in `docs/compliance/snc-chapter-8.md` that the CMDB's role is to
  publish `containers[].image` references and that license /
  vulnerability state is sourced from Dependency-Track keyed on those
  references. Optionally link out to a configured Dependency-Track
  instance from container rows in the UI — no new data model, just a
  URL template in config (e.g. `ARGOS_DEPTRACK_PROJECT_URL`).

- **IMP-007**: **Reporting queries**: once DICT columns land, the UI
  gets a cartography "heat-map" view at `/ui/admin/classification`
  listing every application (Namespace + Workload) with
  `MAX(disponibilite, integrite, confidentialite, tracabilite) >= threshold`.
  Powers the "show me every application whose confidentialité is ≥ 3"
  reporting use case an SNC assessor asks for.

- **IMP-008**: **Success criterion**: a walk-through of SNC §8.1
  against a populated Argos instance must resolve every required
  attribute to either a named column, an explicit derivation rule
  (e.g. `layer` → function), or a documented cross-reference
  ("licenses are in Dependency-Track keyed on `containers[].image`").
  Documented as `docs/compliance/snc-chapter-8.md` that links each
  clause to the column(s), endpoint(s), or external system that
  satisfy it.

## References

- **REF-001**: ADR-0001 — CMDB for SNC using the Kubernetes API (scope
  framing for this gap analysis).
- **REF-002**: ADR-0005 — Multi-cluster collector (defines the
  data-population side whose invariants §8.1.a must not violate).
- **REF-003**: ADR-0006 — Web UI for audit and curated metadata
  (established the curated-metadata pattern this ADR generalises, and
  the "application = namespace / workload" view that anchors DICT
  placement).
- **REF-004**: ADR-0007 — Auth & RBAC (audit trail requirement for
  §8.3 change traceability).
- **REF-005**: ANSSI, *Prestataires de services d'informatique en nuage
  (SecNumCloud) — référentiel d'exigences*, v3.2, 2022-03-08,
  chapter 8 "Gestion des actifs".
- **REF-006**: ANSSI, *EBIOS Risk Manager* — source of the DICT
  (Disponibilité / Intégrité / Confidentialité / Traçabilité)
  classification convention and the 0..4 default scale.
- **REF-007**: Mercator data model — Applications view
  ([https://dbarzin.github.io/mercator/model/#applications-view](https://dbarzin.github.io/mercator/model/#applications-view))
  — places DICT on the Application entity, which Argos honours by
  landing those columns on Namespace and Workload only.
- **REF-008**: Dependency-Track
  ([https://dependencytrack.org/](https://dependencytrack.org/)) —
  external system of record for SBOM and license compliance. Argos
  publishes `containers[].image` as the join key; licenses are not
  duplicated in the CMDB.
