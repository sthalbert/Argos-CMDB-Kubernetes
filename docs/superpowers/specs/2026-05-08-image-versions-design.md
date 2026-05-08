# Container image versions enrichment — V1 design

**Date:** 2026-05-08
**Status:** Spec, awaiting implementation
**Anticipated ADR:** ADR-0020 (or next available)

## Context

`longue-vue` already enriches clusters, nodes, and VM applications with EOL data via `internal/eol/` (periodic queries to endoflife.date). Container images used in Kubernetes workloads were explicitly marked as **out of v1 scope** in ADR-0012:

> Container images (pods/workloads) are out of v1 scope. Image tags are unstructured (`nginx:1.25-alpine`, `myapp:latest`, `sha256:…`) and matching them to endoflife.date products requires a registry-aware parser and a heuristic mapping layer.

This spec covers the v1 of the container-image enrichment, focused on the simplest useful question: **for each image used in the cluster, what is the most recent available tag?** Semantic enrichment (EOL dates, support windows, CVE flags) is intentionally deferred to a future iteration ("V3" in this document) but the data model and code structure preserve a forward-compatible path.

## Goals

- Display, for each container image used in any K8s workload or pod, the most recent available tag from its source registry.
- Work **out-of-the-box for every longue-vue installation**, with zero per-installation configuration in the common case.
- Surface the data in the UI on workload/pod pages and in a global "image inventory" view.
- Allow admins to **trigger a refresh on demand** without waiting for the next scheduled tick.
- Allow admins to **manage the list of supported registries** (add/remove/tune rate limits) without redeploying.

## Non-goals (V1)

- Private registries / authenticated pulls (deferred to V2).
- Wildcard hostname patterns beyond the seeded `*-docker.pkg.dev` (V2).
- VMs (the existing EOL enricher already handles VM applications via ADR-0019).
- CVE / vulnerability scanning (separate concern).
- Per-image policy ("for bitnami/postgres only consider tags matching `X.Y.Z-debian-12-*`") — V2/V3.
- Image → endoflife product mapping ("rich" semantic enrichment) — V3.
- Direct admin UI editing of `image_versions` rows (the table is enricher-owned).

## Architectural decisions

### Decision 1 — Dedicated table, not annotations on workloads

The data is conceptually **per image**, not per workload. A single image (`docker.io/library/nginx`) used by 50 workloads has one "latest" version, not fifty. Storing the result as annotations on each workload would be gratuitous denormalization and complicate global queries ("list all images in the cluster with their freshness").

The new top-level table `image_versions` is keyed by `(image_repo, variant)`. Workloads keep their existing `containers` JSONB unchanged; the API joins at query time.

This deliberately diverges from the EOL enricher's annotation pattern. The EOL pattern is correct *for entities that have EOL metadata* (a node has an OS, a cluster has a kube version) — but a workload doesn't "have" a latest version, it merely uses an image that has one. The data model should reflect that.

### Decision 2 — Variant in the primary key

The user's chosen latest-resolution strategy is **pattern-aware by variant**: for `nginx:1.25.3-alpine`, the latest is the highest `*-alpine` tag, not the highest pure semver.

This means a single `image_repo` can have multiple meaningful "latests" simultaneously — one per variant pattern actually used by workloads. The table PK is `(image_repo, variant)` to capture this.

The enricher batches: for one `image_repo` it queries the registry once and computes the latest for every variant in use, in memory.

### Decision 3 — `eol.Annotation` struct reused as JSONB column

The `annotation` JSONB column stores an `eol.Annotation` struct (the same type used by the EOL enricher). In V1 it carries essentially `latest_available`; in V3 it gains `eol_status`, `support_end`, etc. **without any schema migration**.

This is the central forward-compatibility hinge: the V3 path doesn't require migrating data, only filling in additional fields when the future image-catalog resolver knows them.

### Decision 4 — Registries allowlist in DB, seeded on first migration

Out-of-the-box behavior comes from a SQL migration that inserts the seven default registries. The seeding is **one-shot** (it's part of the create-table migration, not a re-runnable seed): once the migration has run, the table belongs to the admin. Subsequent admin CRUD via `/v1/admin/image-versions/registries` overrides, extends, or removes entries at runtime, and those choices are respected across restarts. If an admin deletes a default by accident, they can recreate it manually via the same CRUD endpoint.

This pattern matches `cloud_accounts`: DB-backed, admin-managed, audited automatically by `AuditMiddleware` (since the routes are under `/v1/admin/*`), discoverable in the UI.

The defaults seeded:

| `hostname` | `rate_limit_per_sec` |
|---|---|
| `docker.io` | 1.0 |
| `ghcr.io` | 5.0 |
| `quay.io` | 5.0 |
| `gcr.io` | 5.0 |
| `*-docker.pkg.dev` | 5.0 |
| `registry.k8s.io` | 5.0 |
| `public.ecr.aws` | 5.0 |

Docker Hub gets a tighter rate limit because of its strict anonymous-access limits.

### Decision 5 — Default interval 24h + manual trigger

Image tags don't change rapidly. A 24h cadence is plenty fresh for the use case and is gentle on registry rate limits. The interval is configurable via `LONGUE_VUE_IMAGE_VERSIONS_INTERVAL`.

For ad-hoc freshness, an admin-only `POST /v1/image-versions/refresh` triggers an immediate tick. The trigger is idempotent: if a tick is already running, the request returns 202 with `already_running: true`.

### Decision 6 — Separate feature toggle from EOL

A new `image_versions_enabled` BOOLEAN column is added to the `settings` table. It is independent from `eol_enabled` because the two enrichers are conceptually different (EOL is about lifecycle metadata; image-versions is about freshness) and operators should be able to enable one without the other.

### Decision 7 — Server-side join on workload/pod responses

Workload and pod responses gain a sibling field `containers_versions` (a map keyed by container name) carrying the joined latest-version data. The existing `containers` JSONB is kept untouched (still the raw collector output).

The cost is small (~100 bytes per container), the field is always present, and it eliminates the need for the UI to make N follow-up calls per workload to enrich its container display.

## Schema

### New table: `image_versions_registries`

```sql
CREATE TABLE image_versions_registries (
    hostname             TEXT PRIMARY KEY,                            -- exact ("docker.io") or "*-suffix" pattern
    rate_limit_per_sec   NUMERIC(6,2) NOT NULL CHECK (rate_limit_per_sec > 0),
    enabled              BOOLEAN NOT NULL DEFAULT TRUE,
    notes                TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO image_versions_registries (hostname, rate_limit_per_sec) VALUES
  ('docker.io',        1.0),
  ('ghcr.io',          5.0),
  ('quay.io',          5.0),
  ('gcr.io',           5.0),
  ('*-docker.pkg.dev', 5.0),
  ('registry.k8s.io',  5.0),
  ('public.ecr.aws',   5.0);
```

`hostname` matching is exact when the value contains no `*`. A leading `*` denotes a suffix match (the rest of the string must match the tail of the candidate hostname). Implemented in `internal/imageversions/registry/match.go` with exhaustive tests.

### Settings: new column

```sql
ALTER TABLE settings
  ADD COLUMN image_versions_enabled BOOLEAN NOT NULL DEFAULT FALSE;
```

Seeded at startup from `LONGUE_VUE_IMAGE_VERSIONS_ENABLED`, same pattern as `eol_enabled` and `mcp_enabled`.

### New table: `image_versions`

```sql
CREATE TABLE image_versions (
    image_repo       TEXT NOT NULL,                 -- canonical fully-qualified, e.g. "docker.io/library/nginx"
    variant          TEXT NOT NULL DEFAULT '',      -- "" for pure semver, otherwise "alpine", "debian-12", ...
    registry         TEXT NOT NULL,                 -- "docker.io", "quay.io", ...
    latest_tag       TEXT,                          -- highest tag matching the variant; NULL when undeterminable
    annotation       JSONB NOT NULL,                -- eol.Annotation struct, V3-ready
    source           TEXT NOT NULL,                 -- "registry" in V1; "endoflife"/"github"/... in V3
    last_checked_at  TIMESTAMPTZ NOT NULL,
    last_error       TEXT,                          -- last query error, NULL on success
    last_error_at    TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (image_repo, variant)
);

CREATE INDEX idx_image_versions_registry ON image_versions(registry);
CREATE INDEX idx_image_versions_last_checked ON image_versions(last_checked_at);
```

No FK to clusters or workloads — image versions are global per longue-vue install.

### Migration order

```
migrations/00NN_create_image_versions_registries.sql       # creates table + seeds defaults
migrations/00NN+1_add_image_versions_enabled_to_settings.sql
migrations/00NN+2_create_image_versions.sql
```

The `image_versions_registries` table must exist before the enricher tries to read it on its first tick, so it migrates first.

## Components

### `internal/imageversions/` package layout

```
enricher.go          // main loop, ticker + trigger channel, atomic running flag
discover.go          // SQL discovery: distinct image strings from workloads.containers + pods.containers
parse.go             // image ref parsing (using github.com/distribution/reference); tag parsing (version, variant, prerelease)
latest.go            // pattern-aware latest computation
types.go             // ImageVersion, ParsedTag, ParsedRef structs + sentinel errors
store_iface.go       // store interface (for testing)
registry/
  client.go          // generic OCI distribution client (auth challenge, pagination)
  match.go           // hostname matching: exact + leading-* suffix
  registries.go      // registry hostname → effective HTTP host translation (e.g., docker.io → registry-1.docker.io)
```

### Main loop

```go
func (e *Enricher) Run(ctx context.Context) {
    ticker := time.NewTicker(e.interval) // default 24h
    defer ticker.Stop()
    e.runTick(ctx) // first run on startup
    for {
        select {
        case <-ctx.Done():    return
        case <-ticker.C:      e.runTick(ctx)
        case <-e.triggerCh:   e.runTick(ctx)
        }
    }
}

func (e *Enricher) Trigger() (alreadyRunning bool) {
    select {
    case e.triggerCh <- struct{}{}:
        return false
    default:
        return true // already pending or in progress
    }
}
```

`triggerCh` is buffered size 1. An atomic `running` flag (set/cleared at the boundaries of `runTick`) ensures only one tick executes at a time. The flag is read by the `POST /refresh` handler to populate the `already_running` response field.

### Tick algorithm

1. **Gating**: read `settings.image_versions_enabled`. If false, return immediately. (Reads on every tick → toggle is hot, no restart needed.)
2. **Load active registries**: `SELECT * FROM image_versions_registries WHERE enabled = TRUE`. Build the in-memory matcher.
3. **Discover**: `SELECT DISTINCT image FROM (workloads.containers UNION pods.containers)` — extract every unique image string used in any cluster.
4. **Parse**: for each image string, derive `(image_repo, full_tag)`, then parse the tag into `(version, variant, prerelease)`. Skip non-parseable tags (`latest`, `master`, `sha-abc123`, `2024.01.15`, etc.) silently at debug log level.
5. **Group**: build `map[image_repo][]variant` so each repo is queried once per tick.
6. **Bounded parallel queries**: semaphore size 5. Per repo, the worker:
   - Looks up the registry config via the matcher.
   - Calls the OCI client `ListTags(ctx, repo)` (auth + pagination handled inside).
   - For each variant in use, runs `ComputeLatest(variant, allTags)` on the same response.
   - Emits one `ImageVersion` per (repo, variant) on the results channel.
7. **Upsert**: each emitted result is upserted in `image_versions`.
8. **Reap**: `DELETE FROM image_versions WHERE (image_repo, variant) NOT IN (set processed this tick)` — removes rows for images no longer referenced by any workload or pod.

A failure on one repo never fails the tick. Each worker is independent.

### Image ref parsing

Use `github.com/distribution/reference` (the OCI/containerd standard library, used across the K8s ecosystem). Avoids reinventing a parser and handles all corner cases (Docker Hub `library/` shortening, digests, ports in hostnames, etc.).

```go
ref, err := reference.ParseNormalizedNamed(imageStr)
// ref.Name() → "docker.io/library/nginx" (canonical)
// ref.(reference.Tagged).Tag() → "1.25.3-alpine"
```

Mapping examples:

| Input | `image_repo` | tag |
|---|---|---|
| `nginx` | `docker.io/library/nginx` | `latest` (skipped) |
| `nginx:1.25.3` | `docker.io/library/nginx` | `1.25.3` |
| `library/nginx:1.25.3-alpine` | `docker.io/library/nginx` | `1.25.3-alpine` |
| `quay.io/prometheus/prometheus:v2.45.0` | `quay.io/prometheus/prometheus` | `v2.45.0` |
| `nginx@sha256:abc...` | `docker.io/library/nginx` | (digest only, skipped) |
| `nginx:1.25@sha256:abc...` | `docker.io/library/nginx` | `1.25` (tag used, digest ignored) |

### Tag parsing

```go
type ParsedTag struct {
    Original     string         // "1.25.3-alpine"
    Version      semver.Version // 1.25.3
    Variant      string         // "alpine" ("" for pure semver)
    IsPrerelease bool           // true for -rc1, -beta, -alpha, -pre, -dev, -snapshot, -nightly
}
```

Algorithm:

1. Strip optional `v` prefix.
2. Greedy regex extract semver prefix: `^(\d+\.\d+\.\d+|\d+\.\d+|\d+)`. If no match, return error (tag is skipped).
3. Whatever follows the optional `-` is the suffix.
4. Classify the suffix:
   - Starts with one of `alpha`, `beta`, `rc`, `pre`, `dev`, `snapshot`, `nightly` → `IsPrerelease=true`, `Variant=""`.
   - Otherwise → `Variant=<suffix>`.

Examples:

| Tag | Version | Variant | Prerelease |
|---|---|---|---|
| `1.25.3` | 1.25.3 | `` | false |
| `v1.25.3` | 1.25.3 | `` | false |
| `1.25.3-alpine` | 1.25.3 | `alpine` | false |
| `1.25.3-alpine3.18` | 1.25.3 | `alpine3.18` | false |
| `1.25.3-debian-12` | 1.25.3 | `debian-12` | false |
| `1.25.3-rc1` | 1.25.3 | `` | true |
| `1.25.3-rc1-alpine` | (skipped — ambiguous) | — | — |
| `latest`, `master`, `sha-abc123`, `2024.01.15` | (skipped — not semver) | — | — |

### Latest computation

```go
func ComputeLatest(variant string, allTags []string) (string, error) {
    var candidates []ParsedTag
    for _, t := range allTags {
        p, err := ParseTag(t)
        if err != nil || p.IsPrerelease || p.Variant != variant {
            continue
        }
        candidates = append(candidates, p)
    }
    if len(candidates) == 0 {
        return "", nil // no match in this variant family
    }
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].Version.GT(candidates[j].Version)
    })
    return candidates[0].Original, nil
}
```

The function takes the variant directly (not the current tag). The tick algorithm has already grouped containers by `(image_repo, variant)` upstream, so the caller already knows which variant family to filter on. This keeps the function pure and side-effect-free, with a single responsibility.

The result is the highest matching tag in that variant family, regardless of any specific workload's current tag. The "is_behind" judgment is made later, at API/UI display time, by comparing each container's `current.Version` to the `latest_tag.Version`.

### Registry adapters

All seven seeded registries implement the OCI Distribution Spec for `/v2/<repo>/tags/list`. A single generic client handles them all; per-registry differences are limited to:

- Hostname translation (e.g., `docker.io` → `registry-1.docker.io` for HTTP).
- Anonymous Bearer-token acquisition via the standard `WWW-Authenticate` challenge (auto-detected).
- Pagination via the `Link: <...>; rel="next"` header (standard).

Pagination is capped at 50 pages (~5000 tags) to bound runaway queries on giant repos.

Per-registry rate limiting uses `golang.org/x/time/rate.Limiter` instances keyed by hostname, with limits read from `image_versions_registries` at the start of each tick.

HTTP client: standard library `http.Client`, 30s timeout, TLS strict, User-Agent `longue-vue/<version> (image-versions-enricher)`.

### Failure semantics

| Case | Behavior |
|---|---|
| Registry hostname not in allowlist | Silent skip (debug log), no row created |
| Tag not parseable (`latest`, `sha-abc`, etc.) | Silent skip, no row created |
| HTTP error (timeout, 5xx, 429) | Upsert with `last_error` and `last_error_at` set; `last_checked_at` updated; `latest_tag` keeps previous value (or NULL if never succeeded) |
| Repo 404 | Upsert with `last_error="repo not found"`, `latest_tag=NULL` |
| No tag in response matches the variant | Upsert with `latest_tag=NULL`, no error (just no match) |
| One repo fails | Other repos continue; tick never globally fails |

The absence of a row is the "no info available" signal in the UI; a row with `last_error` set means "we tried, it failed".

## API surface

### Read endpoints (any authenticated role)

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/image-versions` | Paginated list, one entry per `image_repo` with variants nested |
| `GET` | `/v1/image-versions/{image_repo}` | Detail for one repo, all variants. `image_repo` is URL-encoded |

List filters: `registry`, `image_repo` (substring), `variant`, `has_error` (bool), `last_checked_before` (timestamp). Pagination cursor on `image_repo`.

Sample list entry:

```json
{
  "image_repo": "docker.io/library/nginx",
  "registry": "docker.io",
  "variants": [
    {
      "variant": "",
      "latest_tag": "1.27.4",
      "annotation": { "latest_available": "1.27.4", "eol_status": "unknown" },
      "source": "registry",
      "last_checked_at": "2026-05-08T10:23:00Z",
      "last_error": null,
      "last_error_at": null
    },
    {
      "variant": "alpine",
      "latest_tag": "1.27.4-alpine",
      "annotation": { "latest_available": "1.27.4-alpine", "eol_status": "unknown" },
      "source": "registry",
      "last_checked_at": "2026-05-08T10:23:00Z",
      "last_error": null,
      "last_error_at": null
    }
  ]
}
```

### Action endpoint

| Method | Path | Description |
|---|---|---|
| `POST` | `/v1/image-versions/refresh` | Admin only. Triggers an immediate tick. Returns `202 {queued: bool, already_running: bool}`. Returns `409` if `image_versions_enabled=false` |

### Admin CRUD on registries

| Method | Path | Description |
|---|---|---|
| `GET` | `/v1/admin/image-versions/registries` | List all |
| `POST` | `/v1/admin/image-versions/registries` | Create (body: hostname, rate_limit_per_sec, enabled, notes) |
| `PATCH` | `/v1/admin/image-versions/registries/{hostname}` | Update rate_limit_per_sec / enabled / notes |
| `DELETE` | `/v1/admin/image-versions/registries/{hostname}` | Delete |

All admin scope. Audited automatically by `AuditMiddleware` (the routes are under `/v1/admin/*`). Errors as RFC 7807 `application/problem+json` using existing store sentinels (`ErrNotFound` → 404, `ErrConflict` → 409).

### Settings — existing endpoint, new field

`GET`/`PATCH /v1/admin/settings` already exists. The handler is updated to include and accept the new `image_versions_enabled` boolean.

### Workload and pod response enrichment

Existing list/get handlers for workloads and pods gain a sibling field `containers_versions`:

```json
{
  "name": "my-app",
  "containers": [
    {"name": "web", "image": "nginx:1.25.3-alpine", "image_id": "..."},
    {"name": "sidecar", "image": "envoy:1.28.0"}
  ],
  "containers_versions": {
    "web":     {"latest_tag": "1.27.4-alpine", "is_behind": true,  "last_checked_at": "2026-05-08T10:23:00Z"},
    "sidecar": {"latest_tag": "1.32.0",        "is_behind": true,  "last_checked_at": "2026-05-08T10:23:00Z"}
  }
}
```

`is_behind` is computed server-side as `current.Version < latest.Version` (semver compare). Containers whose image isn't enriched (non-parseable tag, registry not allowlisted, not yet processed) are absent from `containers_versions`.

### OpenAPI

All these endpoints are added to `api/openapi/openapi.yaml` (the contract source of truth, drift checked in CI). The settings endpoints stay hand-written (matches existing pattern per CLAUDE.md), the registry CRUD goes through codegen.

## UI surface

### Workload and pod detail pages (modified)

A reusable `ContainerVersionBadge.tsx` component renders next to each container row:

- ✓ green: up-to-date (`is_behind=false`)
- ↑ orange: behind, with delta tooltip ("v1.25.3 → v1.27.4")
- ⚠ gray: unknown (key absent from `containers_versions`)
- ⛔ red: error (`last_error` non-null, with the message in tooltip)

Tooltip shows the relative `last_checked_at` ("checked 12h ago").

### New page: image inventory `/images`

Linked from the main sidebar. Columns:

| `image_repo` | variants count | source | last_checked | used_by (workloads) | status |

Filters: search bar (substring on `image_repo`), `registry` dropdown, "errors only" toggle.

A row click opens the detail view `/images/{image_repo}` showing all variants alongside the list of workloads/pods that use this repo (with their cluster origin).

A "Refresh now" button is rendered top-right, **admin only**, with a discreet display of the global last-tick timestamp. Clicking it `POST`s to `/v1/image-versions/refresh`, shows a toast, and refetches after a short delay.

### New admin page: `/admin/image-registries`

UI pattern matching `/admin/users` and `/admin/cloud-accounts`:

- Table with `hostname` / `rate_limit_per_sec` / `enabled` (inline toggle) / `notes` / actions
- Add/edit modal with basic validation (rate_limit > 0, hostname non-empty)
- Delete button with confirmation

### Settings toggle

The existing `/admin/settings` page gains a new toggle "Image versions enrichment" alongside the EOL and MCP toggles, with a short description.

### RBAC in the UI

| Surface | viewer / auditor / editor | admin |
|---|---|---|
| Badges on workloads/pods | ✅ visible | ✅ visible |
| `/images` page (read) | ✅ visible | ✅ visible |
| "Refresh" button | ❌ hidden | ✅ visible |
| `/admin/image-registries` | ❌ not in sidebar | ✅ visible |
| Settings toggle | ❌ not on page | ✅ visible |

UI hiding complements backend enforcement. A non-admin hitting an admin URL directly receives a 403.

## Observability

### Prometheus metrics

| Metric | Type | Labels | Description |
|---|---|---|---|
| `imageversions_tick_total` | counter | `status="success\|failure"` | Ticks completed (manual + scheduled combined) |
| `imageversions_tick_duration_seconds` | histogram | — | Tick wall time |
| `imageversions_query_total` | counter | `registry`, `status="success\|error\|rate_limited\|not_found"` | Registry queries |
| `imageversions_query_duration_seconds` | histogram | `registry` | Query latency |
| `imageversions_known_total` | gauge | — | Rows in `image_versions` |
| `imageversions_with_error_total` | gauge | — | Rows with `last_error` non-null |
| `imageversions_last_tick_timestamp_seconds` | gauge | — | Timestamp of last completed tick |
| `imageversions_registries_enabled` | gauge | — | Count of `enabled=TRUE` rows in `image_versions_registries` |

Exposed via the unauthenticated `/metrics` endpoint (per CLAUDE.md).

### Logs

Aligned with existing structured logging in the project:

- **INFO, per tick**: start + final summary (`tick complete: discovered=X, queried=Y, succeeded=Z, errors=N, duration=...`).
- **WARN**: individual queries failing (rate limit, 5xx, response parse failure).
- **DEBUG**: image skips (non-parseable tag, registry not allowlisted) + per-query trace.

A failing registry query never logs at ERROR — it is expected behavior, not an incident. ERROR is reserved for system-level failures (panic, DB write failure).

## Testing strategy

### Unit (table-driven, target 100% on pure logic)

- `parse_test.go`: ~40 cases covering image ref forms (Docker Hub shortcuts, digests, ports, fully-qualified) and tag forms (semver variations, prereleases, variants, mixed, invalid).
- `latest_test.go`: per-variant latest computation, prerelease exclusion, mixed cases, empty registries, large registries (5000 tags).
- `match_test.go`: hostname exact + leading-`*` suffix.

### Registry integration (mocked via `httptest.Server`)

- Fixtures per registry covering: `WWW-Authenticate` challenge, token endpoint, tag list, multi-page pagination via `Link` header.
- Auth failure path, 5xx, 429, 404 repo not found.

### Enricher integration (real Postgres, consistent with `make test`)

- Full tick end-to-end against a mocked registry server.
- Reaping verified: a workload removed → its (repo, variant) row reaped on the next tick when no other workload uses it.
- Manual trigger while a tick is running → second trigger returns `already_running: true`.
- `image_versions_enabled=false` → tick is a no-op.

### Handler tests (full middleware stack)

- Read endpoints accessible to all authenticated roles.
- `/v1/admin/*` writes return 403 for non-admin.
- `/v1/image-versions/refresh` returns 202 / 409 according to feature gate state.

### Live E2E (gated, opt-in)

- Smoke tests against real public registries (Docker Hub, quay, GHCR), gated by `LONGUE_VUE_LIVE_TESTS=1`.
- Skipped in CI by default. Run ad-hoc when modifying registry adapters.

## V3 forward-compatibility

The V1 design is **purely additive** with respect to the future "rich" enrichment:

- `eol.Annotation` JSONB column is reused — V3 fills in `eol_status`, `support_end`, `latest_available` fields that already exist in the struct. No data migration.
- `source` column transitions from `"registry"` to `"endoflife"`/`"github"` per row when a richer resolver wins.
- The V1 registry resolver becomes the **fallback** for images not in the future image catalog.
- The future image-catalog table (mapping `image_repo` → endoflife product or GitHub releases repo) is independent: it doesn't exist in V1 and doesn't need to.

When V3 lands, the V1 work is not throwaway; it is the universal-fallback layer of the V3 resolver chain.

## Open questions / followups

- **Registry adapter for `*-docker.pkg.dev` regional hostnames** — verify that the OCI client actually treats them uniformly (untested, may need tweaks at impl time).
- **Pagination cap of 50 pages (~5000 tags)** — chosen heuristically. If real-world repos hit this (unlikely outside `library/ubuntu`), consider lifting or making configurable per registry.
- **`is_behind` semver comparison** is straightforward when tags are pure semver, less so for variants like `1.25.3-alpine3.18` vs `1.27.0-alpine3.19` (the alpine portion differs). The MVP comparison ignores the suffix and compares only the semver prefix; if this proves misleading, V2 can introduce variant-aware ordering.
