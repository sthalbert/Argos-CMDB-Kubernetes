---
title: "ADR-0032: Kubernetes image freshness in the global EOL dashboard"
status: "Accepted"
date: "2026-05-29"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "cmdb", "eol", "image-versions", "dashboard"]
supersedes: ""
superseded_by: ""
---

# ADR-0032: Kubernetes image freshness in the global EOL dashboard

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-05-29
- **Supersedes:** none
- **Superseded by:** none

## Context

Container images used by Kubernetes workloads did not appear in the global EOL
dashboard (`/v1/eol/extract`, `/ui` EOL), which only flattened
`longue-vue.io/eol.*` annotations on clusters, nodes and VMs. Operators want
each workload image surfaced with a traffic-light freshness signal based on how
many minor versions the deployed tag trails the latest registry tag.

## Decision

- A traffic-light status is derived from the minor-version distance between the
  deployed tag and the latest registry tag for the same variant: same minor or
  patch-only behind = `supported` (green); one minor behind = `approaching_eol`
  (yellow); two or more minors, or any major gap = `eol` (red). Patch
  differences are ignored.
- The rule is computed once server-side in `containers_versions_enrich.go` and
  exposed as `eol_status` on `ContainerVersionInfo`. It reuses the existing
  `eol_status` vocabulary — no new values, no palette change.
- The dashboard synthesises rows at read time (read-time synthesis, no annotation
  writes), preserving the "workloads carry no EOL annotations" invariant.
  `eolagg.FlattenWorkloads` emits one row per distinct image repo (worst tier
  wins on collision).
- Workload lists gain an opt-in `?include=containers_versions` enrichment so the
  dashboard can fetch enriched workloads without making every list call
  expensive.

## Consequences

- The `eol_status` field also appears on the workload/pod unit GET responses (a
  by-product of the single source of truth).
- The interactive table shows the image repo's last path segment (`nginx`); the
  CSV/JSON extract carries the fully-qualified repo (`docker.io/library/nginx`).
  Intentional display-only divergence.
- The per-Application EOL aggregation keeps its existing `is_behind → outdated`
  mapping; not changed in this iteration.
- The EOL extract performs one enrichment pass per workload when
  `entity_type` includes workloads; acceptable for an audited admin extract.

## References

- ADR-0022 — Container image versions enrichment (V1)
- ADR-0026 — Mirror image source resolution
- ADR-0029 — First-class Application entity + per-app EOL aggregation
- ADR-0030 / ADR-0031 — Manual image origin mappings
