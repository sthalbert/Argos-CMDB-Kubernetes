---
title: "ADR-0028: Replica-mirror chain and persisted origin resolutions"
status: "Accepted"
date: "2026-05-23"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "cmdb", "enrichment", "image-versions", "containers", "mirror", "harbor", "oci"]
supersedes: ""
superseded_by: ""
---

# ADR-0028: Replica-mirror chain and persisted origin resolutions

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-05-23
- **Supersedes:** none
- **Superseded by:** none

## Context

ADR-0026 introduced single-hop mirror resolution: a pod referencing
`mirror.io/container/library/nginx:1.25` is rewritten to its public
origin via OCI manifest annotations on the mirror, after which the
image-versions enricher computes freshness against the public registry.
The V1 design assumed exactly one mirror layer.

Real SecNumCloud deployments rarely have just one. The common topology
is three rungs:

```
pod ref          local-registry.io/containers/sthalbert/foo:0.26
                       │  (operator-known rewrite pattern)
                       ▼
global mirror    mirror.io/containers/sthalbert/foo:0.26
                       │  (OCI org.opencontainers.image.base.name)
                       ▼
internet origin  ghcr.io/sthalbert/foo:0.26
```

The local registry holds no annotations — it is just a replication
target of the global mirror. ADR-0026's resolver, applied to a
`local-registry.io/…` ref, fetches the local manifest, finds no
annotation, and silently drops the image from version tracking. The
pod/workload detail join keys lookups on `local-registry.io/…` even
when an `image_versions` row exists under the resolved
`ghcr.io/…` repo, so the "Last version" badge is empty for every
mirrored pod — defeating the goal of ADR-0022/0026.

Two missing pieces:

1. The resolver must traverse one more hop: rewrite the local-registry
   hostname to the global-mirror hostname before fetching annotations.
2. The mirror→origin mapping must be **persisted** so the pod/workload
   GET-time join can find the resolved-origin `image_versions` row and
   surface the canonical upstream ref on the response.

## Decision

- Extend `image_versions_registries` with a `replicates_from_hostname
  TEXT` column. When set on a row with `is_mirror=true`, that row is a
  **replica mirror**: it holds no annotations of its own; the resolver
  rewrites `hostname` to `replicates_from_hostname` and re-looks up the
  upstream annotation mirror, then continues with the existing
  ADR-0026 fetch-annotation logic.
- Two CHECK constraints guard the column: `replica_is_mirror`
  (replicas must have `is_mirror=true`) and `replica_not_self`
  (`replicates_from_hostname <> hostname`). Admin CRUD additionally
  rejects pointing at a hostname that does not match an existing
  mirror row, and pointing at a hostname that is itself a replica
  (`replica target must not itself be a replica`).
- Chain depth is bounded to **one hop**. A replica pointing at another
  replica fails fast with `ErrChainTooDeep`; a replica pointing at a
  hostname with no matching mirror row fails with
  `ErrReplicaTargetMissing`. Both are recorded via the existing
  `imageversions_mirror_resolve_total{result}` metric.
- Hostname-swap only (no path-prefix rewriting). V1 assumes the local
  and global registries use identical paths and differ only by
  hostname — the common Harbor-replication shape. Path-rewriting is
  deferred until a real topology demands it.
- A new top-level table `image_origin_resolutions` persists every
  resolution outcome — both successes and classified failures — keyed
  by the pod-ref's `(image_repo, variant)`. Success rows carry
  `origin_image_repo` and `via_hostname` (the annotation mirror hit);
  failure rows carry `last_error` (one of `missing_annotation`,
  `ambiguous_annotation`, `auth_error`, `fetch_error`,
  `chain_too_deep`, `replica_target_missing`). A CHECK constraint
  enforces that `origin_image_repo` and `via_hostname` are non-NULL
  together. Unclassified errors (transient network blips) are
  logged-and-retried; they are not persisted as failure rows.
- The enricher tick writes one resolution row per resolved ref and
  reaps stale rows at end of tick — mirroring the existing
  `image_versions` lifecycle.
- The pod/workload GET-time join (`containers_versions_enrich.go`)
  reads `image_origin_resolutions` before reading `image_versions`.
  Three branches: `resolved` returns `origin_image_repo` + the badge
  computed against the origin row; `unresolved` returns
  `origin_status="unresolved"` + `origin_error`; passthrough (no
  resolution row) falls through to today's direct
  `image_versions` lookup. Public-registry images and refs the
  enricher has not yet touched continue to render exactly as before.
- `ContainerVersionInfo` in the OpenAPI spec gains three optional
  fields (`origin_image_repo`, `origin_status`, `origin_error`); the
  three pre-existing fields (`latest_tag`, `is_behind`,
  `last_checked_at`) become optional to support the unresolved
  branch's no-badge response shape.

## Consequences

- Operators opt in by adding replica rows in Admin → Image Registries.
  Zero-config deployments remain unaffected (the chain step is a no-op
  when no row has `replicates_from_hostname` set).
- Replica rows may carry auth credentials (`auth_username` /
  `auth_token_ciphertext`), but those credentials are **never used**:
  the resolver always fetches manifests from the upstream annotation
  mirror at the end of the chain. Documented in this ADR so operators
  do not expect them to take effect.
- The OpenAPI relaxation of `ContainerVersionInfo` (three previously
  required fields now optional) is technically a breaking change for
  strict clients. The only consumer today is the React SPA in this
  repo, which adapts in the same PR; external API clients that
  hard-coded the three fields would need to handle absent fields.
- The new table inherits the same reaping cadence as `image_versions`:
  pod refs that disappear from the cluster between ticks are dropped
  on the next tick, regardless of whether their resolution succeeded
  or failed.
- The hostname-swap-only design caps V1 at the topologies described.
  More complex replication schemes (path-prefix rewrites, multiple
  upstream candidates per replica, registry-vendor APIs) are deferred
  to V2 if the deployment surface justifies them.
- MCP exposure of `image_origin_resolutions` is **out of scope** for
  V1; agents continue to query `image_versions` directly, and the new
  table is invisible to them. A follow-up will surface the resolution
  metadata if AI-agent workflows demand it.
- Surfaces other than pod/workload detail (Search, EOL Dashboard,
  Images page) render unchanged. Origin information is added only
  where operators explicitly asked for it.

## References

- ADR-0022 — Container image versions enrichment (V1)
- ADR-0026 — Mirror image source resolution (V1, single hop)
- ADR-0015 §4 — Encrypted secrets pattern (`internal/secrets`,
  AES-256-GCM with PK-bound AAD)
- OCI Image Spec §6 (Annotations) and Distribution Spec §3 (Pulling
  manifests)
