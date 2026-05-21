---
title: "ADR-0026: Mirror image source resolution for image-versions enrichment"
status: "Accepted"
date: "2026-05-21"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "cmdb", "enrichment", "image-versions", "containers", "mirror", "harbor", "oci"]
supersedes: ""
superseded_by: ""
---

# ADR-0026: Mirror image source resolution for image-versions enrichment

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-05-21
- **Supersedes:** none
- **Superseded by:** none

## Context

ADR-0022 introduced the image-versions enricher: it queries the originating
public registry of each container image to compute the latest available tag.
Operators running Harbor with replication (the common SecNumCloud-aligned
posture) pull images from an internal mirror — pod specs reference
`ma-registry.io/container/library/nginx:1.25` rather than
`docker.io/library/nginx`. The enricher cannot find lifecycle data because
its allowlist contains only public registries, and the mirror does not serve
upstream release information.

Harbor replication preserves OCI manifest annotations
(`org.opencontainers.image.base.name`, `org.opencontainers.image.source`)
and the underlying config-blob labels. That metadata is sufficient to
reconstruct the public origin in the majority of cases without any
mirror-vendor-specific API. A vendor-agnostic OCI-native approach also keeps
the door open if the mirror technology changes.

## Decision

- Extend `image_versions_registries` with a composite primary key
  `(hostname, path_prefix)`, plus `is_mirror BOOLEAN`, `auth_username TEXT`,
  and `auth_token_ciphertext BYTEA` (encrypted via `internal/secrets`, AAD
  bound to the PK — same pattern as `cloud_accounts.secret_key` per
  ADR-0015 §4).
- The enricher's public-registry iteration filters on `is_mirror=false`; a
  new `internal/imageversions/mirrorresolve` resolver filters on
  `is_mirror=true`. A row never participates in both paths.
- Per image, the resolver: parses the ref, finds the longest-prefix-matching
  mirror row, fetches the manifest from the mirror with bearer-token auth
  when configured, extracts
  `annotations["org.opencontainers.image.base.name"]` (preferred) or
  `…source` (when it parses as a registry ref) and falls back to
  `config.Labels`. The resolved origin replaces the mirror ref on its way
  into the existing enricher flow.
- Failures (missing annotation, ambiguous source, fetch error, auth error)
  skip the image silently and increment
  `longue_vue_image_versions_mirror_resolve_total{result}`. No DB row is
  written for skipped images; the next tick retries.

## Consequences

- Operators opt in by adding a mirror row in Admin → Registries. Zero-config
  deployments are unaffected (the resolver is a no-op when no mirror rows
  exist).
- The admin CRUD endpoint paths gain a composite identifier
  (`/v1/admin/image-versions-registries/{hostname}/{path_prefix}`) — a
  backwards-incompatible URL change relative to ADR-0022's hostname-only
  paths. Acceptable because the V1 surface is admin-only and brand-new.
- A new plaintext-credentials retrieval endpoint follows the
  `cloud_accounts.credentials` audit-logged pattern (ADR-0015 §4).
- Coverage limited to mirrors that preserve OCI annotations. Mirrors that
  strip them are deferred to V2 (admin-maintained mapping table).

## References

- ADR-0012 — Original EOL enricher design
- ADR-0015 — Encrypted secrets pattern (`internal/secrets`)
- ADR-0022 — Image-versions enrichment V1
- OCI Image Spec §6 (Annotations) and Distribution Spec §3 (Pulling manifests)
