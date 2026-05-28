---
title: "ADR-0031: Manual image origin mappings become authoritative"
status: "Accepted"
date: "2026-05-28"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "cmdb", "enrichment", "image-versions", "containers", "mirror", "harbor", "oci"]
supersedes: ""
superseded_by: ""
---

# ADR-0031: Manual image origin mappings become authoritative

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-05-28
- **Amends:** ADR-0030
- **Supersedes:** none
- **Superseded by:** none

## Context

ADR-0030 introduced an operator-curated `image_origin_mappings` table as
a *tie-breaker* consulted by the mirror resolver only after a successful
`FindMirror(hostname, imagePath)` lookup. The table fixed the
"missing_annotation" case but left a class of refs unreachable: pods
whose `image` field carries a misconfigured hostname (commonly the
`docker.io/` default, or a public registry such as `quay.io/` or
`ghcr.io/`) prefixing a path that actually belongs to a private mirror.

A canonical example observed in production:

    docker.io/containers/grafana/alloy:v1.16.1

The cluster's containerd `registry.mirrors` rewrites `docker.io` at pull
time to a private mirror, so the workload runs correctly. Longue-Vue,
which only reads the pod spec, sees a literal `docker.io/...` ref. The
hostname does not match any row in `image_registries`, the FindMirror
gate misses, and the resolver passes the ref through unchanged. The
image-versions enricher then normalises it under `docker.io/...` and the
operator sees a row that no public registry can resolve.

Twenty-one such rows were observed in production after ADR-0030 was
deployed.

## Decision

The manual mapping table is **authoritative**. The resolver consults it
*before* the FindMirror gate, by progressive suffix stripping:

1. Try `FindOrigin(imagePath)`.
2. If miss, strip one leading segment from imagePath and retry.
3. Repeat until a hit or no slash remains.

On hit, the resolver returns `<public_registry>/<matched_key>:<tag>` and
emits the existing `ok_manual` metric label. On miss, the resolver
continues to the existing FindMirror → replica → OCI annotation flow,
which itself still includes the original post-mirror FindOrigin
tie-breaker. The OCI flow is unchanged.

`FindOrigin` errors fail open: a transient store error is logged at WARN
and the resolver falls through to the existing flow. We accept the
possibility of a one-tick stale resolution over abandoning the whole
resolve.

## Consequences

### Positive

- Misconfigured refs (`docker.io/containers/...`, `quay.io/<mirror-prefix>/...`,
  etc.) are rewritten to the curated public registry without requiring
  Helm chart fixes upstream.
- No data migration: the 198 mappings already in production immediately
  match more cases.
- No new metric label, no new store method, no migration, no
  configuration knob.

### Negative — accepted

- A legitimate public ref whose name collides with a mapping will be
  rewritten. For example, with mapping `grafana/loki → docker.io`, a
  `quay.io/grafana/loki` ref (if one ever existed) would be rewritten
  to `docker.io/grafana/loki`. Operators must curate the table with
  this in mind; the CRUD endpoints, audit logging, and admin scope from
  ADR-0030 remain the safety boundary.
- Each resolve incurs one DB hit per segment-strip iteration. Image
  paths are short (typically ≤ 5 segments); the enricher tick runs at
  most every 24h. No caching added in this iteration. Revisit if
  `imageversions_mirror_resolve_total{result="ok_manual"}` growth makes
  the tick observably slower.

### Unchanged

- ADR-0030's table schema, CRUD endpoints, audit rules, and admin
  scope.
- ADR-0026 / ADR-0028 OCI annotation and replica chain flow.
- All existing metric labels.
