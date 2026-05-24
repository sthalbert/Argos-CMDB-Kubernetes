<div align="center"><img src="../logo.svg" alt="longue-vue" height="38" /></div>

---

# SecNumCloud chapter 8 — Asset management

This page maps the ANSSI **SecNumCloud (SNC) v3.2 chapter 8** ("Gestion des actifs", 2022-03-08) clauses onto the longue-vue data model, so an assessor can see at a glance which clause each feature serves and where the evidence lives.

The design rationale is in [ADR-0008](../adr/adr-0008-secnumcloud-chapter-8-asset-management.md); the applicative-asset and DICT story were completed by [ADR-0029](../adr/adr-0029-first-class-application-entity.md).

## Clause coverage

| Clause | Requirement | longue-vue coverage |
|--------|-------------|---------------------|
| **8.1.a** | Equipment inventory — identification, function, model, location, owner, security need. | Clusters, Nodes, Namespaces, Workloads, Pods, Services, Ingresses, PVs, PVCs, plus non-Kubernetes **Virtual Machines** (ADR-0015). ANSSI cartography `layer` per ADR-0002. Curated `owner` / `criticality` / `runbook_url` / `notes` on the durable kinds. Security need → DICT, see 8.3. |
| **8.1 (applicative)** | Application-level inventory — "a coherent set of IT objects / a grouping of application services", optionally grouped into application blocks. | **First-class Application + ApplicationBlock entities** (ADR-0029). Block → Application → workloads/VMs, cross-substrate (Kubernetes **and** VMs). See [Applications guide](../applications.md). |
| **8.1.b** | Software inventory with version + installed equipment. | `containers` JSONB on Pods/Workloads; node-level software in enriched Node fields; VM `applications[]` (ADR-0019). Largely satisfied. |
| **8.1.c** | License validity. | Out of scope — deferred to [Dependency-Track](https://dependencytrack.org/) via the `containers[].image` join key. |
| **8.2** | Restitution des actifs. | Procedural HR control; out of technical scope. |
| **8.3** | Identification des besoins de sécurité (DICT). | **DICT on the Application entity** (ADR-0029) — `sec_disponibilite` / `sec_integrite` / `sec_confidentialite` / `sec_tracabilite` (0..4) + `sec_notes`. Inherited read-only onto linked workloads/VMs as `effective_dict`. Evidence: classification heat-map + `GET /v1/applications/extract.csv?dict_min=N`. |
| **8.4** | Marquage et manipulation (RECOMMANDÉ). | Labels / annotations carry marking metadata; partial. |
| **8.5** | Supports amovibles. | Out of scope — physical-media procedure. |

## 8.1 — Applicative-asset inventory

The CMDB now carries a first-class **applicative layer**. Where earlier versions could only enumerate the technical assets (workloads, pods, VMs), an assessor can now point at an **Application** row and read its owner, criticality, security classification, runbook, member assets, and end-of-life exposure in one place — no per-workload reconciliation.

The two-level inventory (**ApplicationBlock → Application → workloads/VMs**) matches the SNC framing directly: *"an application block represents a set of applications — office, management, analysis, development applications, …"*. An Application can span multiple Kubernetes clusters and one or more non-Kubernetes VMs, so "Vault" is **one** row even when it runs as a workload in `kube-prod`, a workload in `kube-staging`, and a daemon on a bastion VM.

See the [Applications operator guide](../applications.md) for the workflow.

## 8.3 — Security needs (DICT)

The référentiel requires the provider to identify per-service security needs. Following the ANSSI / EBIOS-RM convention, longue-vue stores the **DICT** vector (Disponibilité / Intégrité / Confidentialité / Traçabilité, each 0..4, plus `sec_notes`) on the **Application** entity — the abstraction the standard intends.

> **Correction since ADR-0008.** ADR-0008 originally proposed putting DICT columns on **Namespace** and **Workload**, on the rationale that those were the abstractions closest to "application" at the time. That migration was never shipped (its slot was taken by `00023_create_cloud_accounts.sql`), and ADR-0029 introduced the proper Application entity. **DICT therefore lives only on the Application today.** Linked workloads and VMs do not store DICT; they surface a read-only `effective_dict` inherited from their Application. There are no namespace/workload DICT columns for an assessor to inspect — the Application row is the single source of truth.

### Evidence for the audit package

- **Classification heat-map** — `/ui/admin/classification` lists every Application whose `MAX(DICT)` meets an adjustable threshold (default 3), heat-coloured per axis with a `sec_notes` excerpt.
- **CSV / JSON extract** — `GET /v1/applications/extract.csv?dict_min=3` (audit-logged, capped by `LONGUE_VUE_EXTRACT_MAX_ROWS`) produces a per-Application export with DICT, member counts, and metadata in a stable shape for the evidence package.
- **Audit trail** — every DICT write lands in `audit_events` with `resource_type="application"`, before/after values included, so an assessor can trace when and by whom a classification changed.

## See also

- [Applications](../applications.md) — operator workflow for blocks, applications, linking, DICT, and EOL.
- [ADR-0008](../adr/adr-0008-secnumcloud-chapter-8-asset-management.md) — asset-management data model for SNC chapter 8.
- [ADR-0029](../adr/adr-0029-first-class-application-entity.md) — first-class Application entity.
- [ADR-0002](../adr/adr-0002-kubernetes-to-anssi-cartography-layers.md) — ANSSI cartography layer mapping.
