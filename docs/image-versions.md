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

### Sidebar

A dedicated **Container images** entry appears in the sidebar under the **Tools** section (next to **EOL**). The icon shows three stacked layers, evoking an OCI image's layered filesystem.

### `/images` page — inventory

Shows one row per `image_repo` with its variant count, registry, last-checked timestamp, and overall status. Click a row to drill into per-variant detail.

**Filtering** combines two layers, mirroring the rest of the UI:

- **Summary cards** (top of the page) double as filter toggles. Each card has a label and a count:
  - **Repos** — total. Click to clear all server-side filters.
  - **Variants** — total variant rows across all repos. Click to clear filters.
  - **With errors** — repos where at least one variant has a non-null `last_error`. Click to filter the table to errored repos only; click again to clear.
  - **Checked** — repos where every variant queried succeeded. Click to filter to non-errored repos.
  The active card is highlighted; a "Filtering: …" banner appears with a `clear` button.
- **Per-column funnels** — a small funnel button in each column header opens a popover that lists the distinct values currently rendered in that column. Toggling a checkbox hides every row whose cell text doesn't match the surviving set. Selections are persisted in `localStorage` under `lv.col-filters.lists.images` and survive refreshes / refetches. This composes with the server-side card filters: server narrows the page; column funnels refine within it.
- **Search box** — image-repo substring (debounced 300 ms), runs server-side.

### `/images/{image_repo}` page — detail

One row per **variant** for the selected `image_repo`. Shows latest tag, source (`registry` for V1; `endoflife`/`github` reserved for V3), last-checked timestamp, and any error message.

### Workload / pod detail pages

Each container in a workload's `(template)` table or a pod's `(runtime)` table gets a **Last version** column.

- When the image is enriched and the tag parses cleanly, the cell shows the **latest available tag in plain text**, styled as a pill: orange when the workload's current tag is older than latest, green when up to date. Hover the pill to see the freshness timestamp.
- When the image cannot be enriched (non-parseable tag, registry outside the allowlist, or not yet processed by the enricher), the cell falls back to a `⚠ unknown` badge.
- When the registry query failed, the cell shows a `⛔ error` badge with the message in tooltip.

The pod's table additionally has an **Init** column distinguishing init containers (Kubernetes containers that run *before* the main containers — typically migrations, volume populators, "wait-for-X" probes) from the main containers. Both are enriched the same way.

## Out of scope (V1)

Private registries, tag-pattern policies, EOL/CVE enrichment, and GitHub releases lookup are explicitly deferred to V2/V3.

## Triggering a refresh

Admins can click **Refresh now** on the `/images` page (visible only to the `admin` role) or call `POST /v1/image-versions/refresh` to run the enrichment cycle immediately. The endpoint is idempotent: a second call while a tick is already running returns 202 with `already_running: true` and is a no-op.

## API surface

| Method | Path | Role | Description |
|---|---|---|---|
| `GET` | `/v1/image-versions` | any authenticated | Paginated list, one entry per `image_repo` with variants nested. Supports `limit`, `cursor`, `registry`, `image_repo` (substring), `variant`, `has_error`, `last_checked_before` query parameters. |
| `GET` | `/v1/image-versions/{image_repo}` | any authenticated | Detail for one repo, all variants. `image_repo` is URL-encoded. |
| `POST` | `/v1/image-versions/refresh` | admin | Trigger an immediate enrichment cycle. |
| `GET` | `/v1/admin/image-versions/registries` | admin | List the supported registries allowlist. |
| `POST` | `/v1/admin/image-versions/registries` | admin | Add a registry. |
| `PATCH` | `/v1/admin/image-versions/registries/{hostname}` | admin | Update rate limit / enabled / notes. |
| `DELETE` | `/v1/admin/image-versions/registries/{hostname}` | admin | Remove a registry. |

All admin writes are recorded in `audit_events` automatically. See `api/openapi/openapi.yaml` for the full schema.

## MCP tools

The same store is exposed to AI agents via three MCP tools (read-only, bearer-authenticated). See [`mcp-server.md`](mcp-server.md) for the full MCP setup.

| Tool | Purpose |
|---|---|
| `list_image_versions` | Filtered list (registry, image_repo substring, variant, has_error). One entry per repo with variants nested. |
| `get_image_version` | All variants for a single fully-qualified `image_repo`. |
| `get_image_versions_summary` | Aggregate snapshot — counts (total/with-errors/all-ok), per-registry breakdown, sample of up to 50 errored variants. Designed for one-shot agent analysis. |

## Troubleshooting

**No rows appear at all:**
- Confirm the toggle is on under **Admin → Settings**.
- Confirm at least one registry is `enabled` under **Admin → Image registries** (the enricher skips reaping when no registry is enabled to avoid wiping data on a transient mis-configuration).
- Hit **Refresh now** to bypass the 24h tick wait.

**A specific image shows `⚠ unknown`:**
- Its tag may not match a recognized semver pattern (`latest`, `master`, `sha-abc123`, date-shaped tags like `2024.01.15`, mixed prerelease+variant like `1.25-rc1-alpine`). These are intentionally skipped.
- Or its registry hostname is not in the allowlist (e.g., `harbor.corp.example.com`). Add it under **Admin → Image registries** if it's a public registry.

**A specific image shows `⛔ error`:**
- Hover the badge for the registry's response. Common cases: 404 repo not found, 401 (Docker Hub returns 401 instead of 404 for non-existent repos — this looks like an auth failure but means the repo doesn't exist), 429 rate-limited.
- For Docker Hub rate limiting, lower `rate_limit_per_sec` for `docker.io` under **Admin → Image registries**.
