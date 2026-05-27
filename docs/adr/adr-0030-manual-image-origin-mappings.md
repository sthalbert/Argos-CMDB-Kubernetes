---
title: "ADR-0030: Manual image origin mappings"
status: "Accepted"
date: "2026-05-27"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "cmdb", "enrichment", "image-versions", "containers", "mirror", "harbor", "oci"]
supersedes: ""
superseded_by: ""
---

# ADR-0030: Manual image origin mappings

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-05-27
- **Supersedes:** none
- **Superseded by:** none

## Context

ADR-0026 and ADR-0028 resolve mirrored container refs to their public
origin by reading OCI manifest annotations (`org.opencontainers.image.base.name`
in priority). Field measurement on the tooling deployment on 2026-05-27
shows the OCI approach has fundamental coverage limits:

```
imageversions_mirror_resolve_total{result="ok"}                  5
imageversions_mirror_resolve_total{result="missing_annotation"}  90
imageversions_mirror_resolve_total{result="auth_error"}          95
imageversions_mirror_resolve_total{result="passthrough"}       2803
```

Root cause: `org.opencontainers.image.base.name` per OCI spec designates
the **Dockerfile FROM base image**, not the image's own registry origin.
Public-image authors (Grafana, Prometheus Operator, etc.) have no reason
to stamp their own public location into their own image's `base.name`.
The annotation-mirror approach works for the rare images that explicitly
stamp this metadata; for the common case it produces either
`missing_annotation` or misleading resolutions pointing at the FROM base
(e.g. `docker.io/library/debian` for PostgreSQL).

Operators typically maintain an explicit inventory of which images they
mirror and where the upstream lives. Letting them declare it directly is
both more accurate and more auditable than annotation guessing.

## Decision

- Add a new operator-curated table `image_origin_mappings` with PK
  `image_name` and a non-null `public_registry` column. Each row says
  "the bare image_name (path under the mirror's path_prefix) is published
  at the named public registry".
- The mirror resolver consults this table **in priority**: after a
  mirror-row hit, it strips the matched `path_prefix` from the path and
  looks up the bare name. On hit, it returns
  `<public_registry>/<image_name>:<tag>` and records
  `imageversions_mirror_resolve_total{result="ok_manual"}`. On miss, it
  falls through to the existing OCI annotation fetch path (ADR-0026 /
  ADR-0028) unchanged.
- Resolution outcomes are persisted in `image_origin_resolutions` exactly
  as today. For manual hits, `via_hostname` is set to the pod ref's
  hostname (the resolution did not require a manifest fetch). The column
  semantic widens from "hostname where annotation was read" to "hostname
  that triggered the resolution".
- Admin REST CRUD under `/v1/admin/image-origin-mappings/*` (admin scope,
  audit-logged automatically). The API exposes only longue-vue's domain
  model (`image_name`, `public_registry`, `notes`); no proprietary
  bootstrap format is enforced. Operators write their own ETL to push
  from whatever inventory they keep (YAML, CSV, CMDB exports).
- Constraints: `image_name` must not contain `://`, `:`, `@`, or
  whitespace; `public_registry` must be a bare hostname (no `/`).
  Enforced at handler and DB layers.
- No feature flag. Empty table preserves today's behavior. The mechanism
  activates as soon as rows exist.

## Consequences

- Maintenance burden: operators must keep the mappings in sync with the
  images deployed in their clusters. The UI can surface "unmapped images
  in clusters" to help, but V1 ships without that (see Out of scope).
- Determinism: the lookup is a single PK select with no network call,
  so the manual path is faster and more reliable than the OCI path.
- Open-source friendliness: the API contract is format-agnostic. Any
  operator can write a 5-line script that converts their preferred
  inventory format into POST requests. No coupling to a proprietary
  schema.
- ADR-0026 and ADR-0028 stay in place as fallbacks. No code is removed
  or deprecated. Existing deployments that rely on annotation fetch
  continue to work; adding mappings only improves coverage.
- The `image_origin_resolutions` table receives rows from both code
  paths transparently; the per-tick reaper is unaffected.
- Surfaces beyond pod/workload detail (Search, EOL Dashboard, Images
  page) inherit the improved coverage automatically.

## Out of scope (V1)

- Dedicated UI page. The Swagger UI at `/docs/` provides interactive
  editing today.
- Bulk endpoint. Clients loop over individual CRUD calls (typically a
  one-shot bootstrap, then occasional updates).
- Validation of `public_registry` against a known list of public
  registries. The set is open-ended (`docker.io`, `ghcr.io`, `quay.io`,
  `registry.k8s.io`, `nvcr.io`, `oci.external-secrets.io`,
  `registry.opensource.zalan.do`, …).
- Tags allowlist. The resolver passes the pod's tag through unchanged.
- MCP server exposure of the new table.

## References

- ADR-0022 — Container image versions enrichment (V1)
- ADR-0026 — Mirror image source resolution (V1, single hop)
- ADR-0028 — Replica-mirror chain and persisted origin resolutions
- OCI Image Spec §6 (Annotations) — for the `base.name` semantic that
  motivated this ADR
