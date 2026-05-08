---
title: "ADR-0022: Container image versions enrichment (V1)"
status: "Accepted"
date: "2026-05-08"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "cmdb", "enrichment", "image-versions", "containers", "registry"]
supersedes: ""
superseded_by: ""
---

# ADR-0022: Container image versions enrichment (V1)

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-05-08
- **Supersedes:** none
- **Superseded by:** none

## Context

ADR-0012 deferred container image enrichment to "v2 scope" because matching arbitrary image tags to endoflife.date products requires registry-aware parsing and a heuristic mapping layer. V1 of that work delivers the simplest useful slice: the latest available tag per image, sourced directly from the originating public registry, without any image-to-product mapping.

## Decision

- New top-level `image_versions` table keyed by `(image_repo, variant)`.
- Periodic enricher in `internal/imageversions/`, default 24h, with a manual `POST /v1/image-versions/refresh` admin trigger.
- Allowlist of registries lives in DB (`image_versions_registries`), seeded by migration with the seven major public registries; admin CRUD handles overrides at runtime.
- The annotation column reuses the `eol.Annotation` shape so a future "rich" enrichment (V3) can populate EOL fields without a schema migration.
- Workload/pod GET responses gain a `containers_versions` sibling field via a server-side join.

## Consequences

- New surface: 5 endpoints, 2 tables, 1 settings field, 3 new UI pages.
- Coverage limited to public registries; private registries deferred (V2).
- Variant-aware comparisons use the semver prefix only — variant suffix differences (e.g., `alpine3.18` vs `alpine3.19`) aren't ordered. Acceptable in V1; revisit if confusing in practice.

## References

- Spec: `docs/superpowers/specs/2026-05-08-image-versions-design.md`
- Plan: `docs/superpowers/plans/2026-05-08-image-versions.md`
