# Container image versions

`longue-vue` periodically queries public container registries for the latest available tag of each image used in your Kubernetes clusters and surfaces the data in the UI and API.

## Enabling

The feature is **off by default**. Enable it in **Admin → Settings → Image versions enrichment**, or set the env var `LONGUE_VUE_IMAGE_VERSIONS_ENABLED=true` at boot to seed the toggle.

The default refresh interval is 24h, configurable via `LONGUE_VUE_IMAGE_VERSIONS_INTERVAL` (e.g., `12h`, `48h`).

## Supported registries

By default, the following public registries are queried:

- `docker.io` (Docker Hub) — anonymous bearer auth, 1 req/s
- `ghcr.io` (GitHub Container Registry) — 5 req/s
- `quay.io` — 5 req/s
- `gcr.io` — 5 req/s
- `*-docker.pkg.dev` (Google Artifact Registry, regional) — 5 req/s
- `registry.k8s.io` — 5 req/s
- `public.ecr.aws` — 5 req/s

The list is editable in **Admin → Image registries**. You can add additional public registries (with their hostname pattern and rate limit) or disable defaults you don't want queried.

## What's enriched

For each container image used in any K8s cluster:

- The image is parsed into `(registry, repository, tag)` via the OCI distribution standard.
- If the tag has a recognizable semver prefix (`1.25.3`, `v1.25.3-alpine`, etc.), the enricher fetches the registry's tag list and picks the highest non-prerelease tag matching the same variant suffix.
- A row is written to `image_versions(image_repo, variant)` with the resulting `latest_tag`.

## Where to see it

- **Workload / pod detail pages** — each container row gets a status badge (up-to-date, behind, unknown, error).
- **`/images` page** — global inventory of all images currently used, with their latest available tag, registry, and last-checked timestamp.

## Out of scope (V1)

Private registries, tag-pattern policies, EOL/CVE enrichment, and GitHub releases lookup are explicitly deferred to V2/V3.

## Triggering a refresh

Admins can click **Refresh now** on the `/images` page (or `POST /v1/image-versions/refresh`) to run the enrichment cycle immediately.
