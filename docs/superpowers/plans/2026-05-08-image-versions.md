# Container image versions enrichment — V1 implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement V1 of the container-image versions enrichment for `longue-vue`: a periodic enricher that queries public registries for the latest available tag of each image used in K8s clusters, exposes the data via REST + UI, and provides admin CRUD over the supported-registries allowlist.

**Architecture:** New top-level `image_versions` table keyed by `(image_repo, variant)`, populated by a 24h goroutine that batches registry queries. Allowlist of registries lives in a separate DB table seeded by migration and mutable via admin CRUD. Workload/pod responses gain a `containers_versions` sibling field via a server-side join. Reuses the existing `eol.Annotation` JSONB struct so a future "rich" V3 (EOL/CVE) is purely additive.

**Tech Stack:** Go 1.25, Postgres (pgx/v5), `github.com/distribution/reference` for image-ref parsing, `golang.org/x/time/rate` for per-registry rate limiting, OpenAPI 3.1, Vite + React + TypeScript.

**Spec:** `docs/superpowers/specs/2026-05-08-image-versions-design.md`

**Migration sequence used in this plan:** `00038`, `00039`, `00040`. Verify these numbers are still correct at execution time (`ls migrations/ | tail -5`); if a new migration has landed since this plan was written, shift the numbers up uniformly.

---

## Task 1 — Migrations: registries allowlist, settings column, image_versions table

**Files:**
- Create: `migrations/00038_create_image_versions_registries.sql`
- Create: `migrations/00039_add_image_versions_enabled_to_settings.sql`
- Create: `migrations/00040_create_image_versions.sql`

- [ ] **Step 1: Verify the next migration number is 00038**

```bash
ls migrations/ | sort | tail -3
```
Expected: `00037_history_backfill.sql` is the last existing entry. If a higher number is present, add the delta uniformly to all three new files in this task.

- [ ] **Step 2: Write `migrations/00038_create_image_versions_registries.sql`**

```sql
-- +goose Up
CREATE TABLE image_versions_registries (
    hostname             TEXT PRIMARY KEY,
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

-- +goose Down
DROP TABLE image_versions_registries;
```

- [ ] **Step 3: Write `migrations/00039_add_image_versions_enabled_to_settings.sql`**

```sql
-- +goose Up
ALTER TABLE settings
  ADD COLUMN image_versions_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE settings
  DROP COLUMN image_versions_enabled;
```

- [ ] **Step 4: Write `migrations/00040_create_image_versions.sql`**

```sql
-- +goose Up
CREATE TABLE image_versions (
    image_repo       TEXT NOT NULL,
    variant          TEXT NOT NULL DEFAULT '',
    registry         TEXT NOT NULL,
    latest_tag       TEXT,
    annotation       JSONB NOT NULL,
    source           TEXT NOT NULL,
    last_checked_at  TIMESTAMPTZ NOT NULL,
    last_error       TEXT,
    last_error_at    TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (image_repo, variant)
);

CREATE INDEX idx_image_versions_registry ON image_versions(registry);
CREATE INDEX idx_image_versions_last_checked ON image_versions(last_checked_at);

-- +goose Down
DROP TABLE image_versions;
```

- [ ] **Step 5: Run migrations against a local DB to verify**

Assuming you have `PGX_TEST_DATABASE` exported and the `goose` CLI installed (or run via the embedded migrator the project uses):

```bash
make build-noui && PGX_TEST_DATABASE=$PGX_TEST_DATABASE go test -race -run '^TestPGMigrate$' ./internal/store/...
```
Expected: PASS. If no such test exists, manually apply via `goose -dir migrations postgres "$PGX_TEST_DATABASE" up` and verify the three new tables/columns exist with `psql ... -c "\d image_versions"`.

- [ ] **Step 6: Commit**

```bash
git add migrations/00038_create_image_versions_registries.sql \
        migrations/00039_add_image_versions_enabled_to_settings.sql \
        migrations/00040_create_image_versions.sql
git commit -m "feat(db): migrations for image_versions tables and settings flag"
```

---

## Task 2 — Settings: add `ImageVersionsEnabled` field

**Files:**
- Modify: `internal/api/store.go` (struct definitions around lines 713–730)
- Modify: `internal/store/pg.go` (settings upsert/scan — find via grep)

- [ ] **Step 1: Locate the settings DB methods**

```bash
grep -n -E 'func \(.*\) (Get|Update|Upsert)Settings' internal/store/pg.go
```
Expected: 1–3 matches. Note the line numbers; you'll edit them in step 4.

- [ ] **Step 2: Modify `internal/api/store.go` — add field to both structs**

Find the `Settings` struct (around line 713) and add the new field:

```go
type Settings struct {
    EOLEnabled              bool      `json:"eol_enabled"`
    MCPEnabled              bool      `json:"mcp_enabled"`
    TimeTravelEnabled       bool      `json:"time_travel_enabled"`
    TimeTravelRetentionDays int       `json:"time_travel_retention_days"`
    TimeTravelReaperEnabled bool      `json:"time_travel_reaper_enabled"`
    ImageVersionsEnabled    bool      `json:"image_versions_enabled"`
    UpdatedAt               time.Time `json:"updated_at"`
}

type SettingsPatch struct {
    EOLEnabled              *bool `json:"eol_enabled,omitempty"`
    MCPEnabled              *bool `json:"mcp_enabled,omitempty"`
    TimeTravelEnabled       *bool `json:"time_travel_enabled,omitempty"`
    TimeTravelRetentionDays *int  `json:"time_travel_retention_days,omitempty"`
    TimeTravelReaperEnabled *bool `json:"time_travel_reaper_enabled,omitempty"`
    ImageVersionsEnabled    *bool `json:"image_versions_enabled,omitempty"`
}
```

- [ ] **Step 3: Update settings DB methods in `internal/store/pg.go`**

For each of `GetSettings`, `UpdateSettings` (or `UpsertSettings`), add the `image_versions_enabled` column to: SELECT lists, INSERT/UPSERT column lists, RETURNING clauses, scan targets, and COALESCE clauses on the patch path. Mirror exactly what is done for `mcp_enabled`.

Example for the SELECT path (adapt to actual code):

```go
const q = `
SELECT id, eol_enabled, mcp_enabled, time_travel_enabled, time_travel_retention_days,
       time_travel_reaper_enabled, image_versions_enabled, updated_at
FROM settings WHERE id = 1`
// ...
err := row.Scan(&s.ID, &s.EOLEnabled, &s.MCPEnabled, &s.TimeTravelEnabled,
    &s.TimeTravelRetentionDays, &s.TimeTravelReaperEnabled, &s.ImageVersionsEnabled, &s.UpdatedAt)
```

For the patch path (`UpdateSettings`), add a `COALESCE($N::bool, image_versions_enabled)` argument; bind `$N` from `patch.ImageVersionsEnabled` (pgx will emit NULL when the pointer is nil).

- [ ] **Step 4: Add a regression test for the new field round-trip**

In `internal/store/pg_test.go` (or wherever existing settings tests live), add:

```go
func TestSettings_ImageVersionsEnabled_RoundTrip(t *testing.T) {
    pg := newTestPG(t)
    ctx := context.Background()

    s, err := pg.GetSettings(ctx)
    if err != nil { t.Fatalf("get: %v", err) }
    if s.ImageVersionsEnabled {
        t.Fatalf("expected default false, got true")
    }

    on := true
    if _, err := pg.UpdateSettings(ctx, api.SettingsPatch{ImageVersionsEnabled: &on}); err != nil {
        t.Fatalf("patch on: %v", err)
    }
    s2, _ := pg.GetSettings(ctx)
    if !s2.ImageVersionsEnabled {
        t.Fatalf("expected true after patch")
    }

    off := false
    if _, err := pg.UpdateSettings(ctx, api.SettingsPatch{ImageVersionsEnabled: &off}); err != nil {
        t.Fatalf("patch off: %v", err)
    }
    s3, _ := pg.GetSettings(ctx)
    if s3.ImageVersionsEnabled {
        t.Fatalf("expected false after second patch")
    }
}
```

- [ ] **Step 5: Run the test**

```bash
go test -race -run '^TestSettings_ImageVersionsEnabled_RoundTrip$' ./internal/store/...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/store.go internal/store/pg.go internal/store/pg_test.go
git commit -m "feat(settings): add image_versions_enabled toggle"
```

---

## Task 3 — Store: `image_versions_registries` CRUD

**Files:**
- Create: `internal/store/pg_image_registries.go`
- Create: `internal/store/pg_image_registries_test.go`
- Modify: `internal/api/store.go` (add types + Store interface methods)

- [ ] **Step 1: Add types and Store-interface methods to `internal/api/store.go`**

Add the new types near the bottom of the file:

```go
type ImageRegistry struct {
    Hostname         string    `json:"hostname"`
    RateLimitPerSec  float64   `json:"rate_limit_per_sec"`
    Enabled          bool      `json:"enabled"`
    Notes            *string   `json:"notes,omitempty"`
    CreatedAt        time.Time `json:"created_at"`
    UpdatedAt        time.Time `json:"updated_at"`
}

type ImageRegistryUpsert struct {
    Hostname         string  `json:"hostname"`
    RateLimitPerSec  float64 `json:"rate_limit_per_sec"`
    Enabled          *bool   `json:"enabled,omitempty"` // defaults to true
    Notes            *string `json:"notes,omitempty"`
}

type ImageRegistryPatch struct {
    RateLimitPerSec *float64 `json:"rate_limit_per_sec,omitempty"`
    Enabled         *bool    `json:"enabled,omitempty"`
    Notes           *string  `json:"notes,omitempty"`
}
```

Then locate the `Store` interface in the same file (search for `type Store interface`) and add:

```go
    // Image registries
    ListImageRegistries(ctx context.Context) ([]ImageRegistry, error)
    GetImageRegistry(ctx context.Context, hostname string) (ImageRegistry, error)
    CreateImageRegistry(ctx context.Context, in ImageRegistryUpsert) (ImageRegistry, error)
    UpdateImageRegistry(ctx context.Context, hostname string, p ImageRegistryPatch) (ImageRegistry, error)
    DeleteImageRegistry(ctx context.Context, hostname string) error
```

- [ ] **Step 2: Write the failing test `internal/store/pg_image_registries_test.go`**

```go
package store

import (
    "context"
    "testing"

    "github.com/sthalbert/longue-vue/internal/api"
)

func TestImageRegistries_SeedDefaults(t *testing.T) {
    pg := newTestPG(t)
    ctx := context.Background()

    list, err := pg.ListImageRegistries(ctx)
    if err != nil { t.Fatalf("list: %v", err) }
    if len(list) != 7 {
        t.Fatalf("expected 7 default rows, got %d", len(list))
    }

    seen := map[string]bool{}
    for _, r := range list {
        seen[r.Hostname] = true
        if !r.Enabled {
            t.Errorf("default %q should be enabled", r.Hostname)
        }
    }
    for _, want := range []string{
        "docker.io", "ghcr.io", "quay.io", "gcr.io",
        "*-docker.pkg.dev", "registry.k8s.io", "public.ecr.aws",
    } {
        if !seen[want] {
            t.Errorf("missing default registry %q", want)
        }
    }
}

func TestImageRegistries_CreateGetUpdateDelete(t *testing.T) {
    pg := newTestPG(t)
    ctx := context.Background()

    notes := "internal mirror"
    created, err := pg.CreateImageRegistry(ctx, api.ImageRegistryUpsert{
        Hostname:        "mirror.example.com",
        RateLimitPerSec: 2.5,
        Notes:           &notes,
    })
    if err != nil { t.Fatalf("create: %v", err) }
    if created.RateLimitPerSec != 2.5 || !created.Enabled {
        t.Fatalf("unexpected created: %+v", created)
    }

    got, err := pg.GetImageRegistry(ctx, "mirror.example.com")
    if err != nil { t.Fatalf("get: %v", err) }
    if got.Notes == nil || *got.Notes != "internal mirror" {
        t.Fatalf("notes mismatch: %+v", got.Notes)
    }

    off := false
    upd, err := pg.UpdateImageRegistry(ctx, "mirror.example.com",
        api.ImageRegistryPatch{Enabled: &off})
    if err != nil { t.Fatalf("update: %v", err) }
    if upd.Enabled {
        t.Fatalf("expected disabled after patch")
    }

    if err := pg.DeleteImageRegistry(ctx, "mirror.example.com"); err != nil {
        t.Fatalf("delete: %v", err)
    }
    if _, err := pg.GetImageRegistry(ctx, "mirror.example.com"); err == nil {
        t.Fatalf("expected ErrNotFound after delete")
    }
}

func TestImageRegistries_CreateConflict(t *testing.T) {
    pg := newTestPG(t)
    ctx := context.Background()
    _, err := pg.CreateImageRegistry(ctx, api.ImageRegistryUpsert{
        Hostname:        "docker.io",
        RateLimitPerSec: 1.0,
    })
    if err == nil {
        t.Fatalf("expected ErrConflict for existing hostname")
    }
}
```

- [ ] **Step 3: Run tests — they must fail (no implementation yet)**

```bash
go test -race -run '^TestImageRegistries_' ./internal/store/...
```
Expected: FAIL with "method ListImageRegistries undefined" or build errors.

- [ ] **Step 4: Implement `internal/store/pg_image_registries.go`**

```go
package store

import (
    "context"
    "errors"
    "fmt"

    "github.com/jackc/pgx/v5"

    "github.com/sthalbert/longue-vue/internal/api"
)

func scanImageRegistry(row pgx.Row) (api.ImageRegistry, error) {
    var r api.ImageRegistry
    if err := row.Scan(&r.Hostname, &r.RateLimitPerSec, &r.Enabled, &r.Notes,
        &r.CreatedAt, &r.UpdatedAt); err != nil {
        return api.ImageRegistry{}, err
    }
    return r, nil
}

func (p *PG) ListImageRegistries(ctx context.Context) ([]api.ImageRegistry, error) {
    const q = `
SELECT hostname, rate_limit_per_sec, enabled, notes, created_at, updated_at
FROM image_versions_registries
ORDER BY hostname`
    rows, err := p.pool.Query(ctx, q)
    if err != nil {
        return nil, fmt.Errorf("list image registries: %w", err)
    }
    defer rows.Close()
    var out []api.ImageRegistry
    for rows.Next() {
        r, err := scanImageRegistry(rows)
        if err != nil {
            return nil, fmt.Errorf("scan image registry: %w", err)
        }
        out = append(out, r)
    }
    return out, rows.Err()
}

func (p *PG) GetImageRegistry(ctx context.Context, hostname string) (api.ImageRegistry, error) {
    const q = `
SELECT hostname, rate_limit_per_sec, enabled, notes, created_at, updated_at
FROM image_versions_registries WHERE hostname = $1`
    r, err := scanImageRegistry(p.pool.QueryRow(ctx, q, hostname))
    if errors.Is(err, pgx.ErrNoRows) {
        return api.ImageRegistry{}, api.ErrNotFound
    }
    if err != nil {
        return api.ImageRegistry{}, fmt.Errorf("get image registry: %w", err)
    }
    return r, nil
}

func (p *PG) CreateImageRegistry(ctx context.Context, in api.ImageRegistryUpsert) (api.ImageRegistry, error) {
    enabled := true
    if in.Enabled != nil {
        enabled = *in.Enabled
    }
    const q = `
INSERT INTO image_versions_registries (hostname, rate_limit_per_sec, enabled, notes)
VALUES ($1, $2, $3, $4)
RETURNING hostname, rate_limit_per_sec, enabled, notes, created_at, updated_at`
    r, err := scanImageRegistry(p.pool.QueryRow(ctx, q, in.Hostname, in.RateLimitPerSec, enabled, in.Notes))
    if err != nil {
        if isUniqueViolation(err) { // existing helper in pg.go; if missing, inline check on pgconn.PgError code 23505
            return api.ImageRegistry{}, api.ErrConflict
        }
        return api.ImageRegistry{}, fmt.Errorf("create image registry: %w", err)
    }
    return r, nil
}

func (p *PG) UpdateImageRegistry(ctx context.Context, hostname string, patch api.ImageRegistryPatch) (api.ImageRegistry, error) {
    const q = `
UPDATE image_versions_registries SET
    rate_limit_per_sec = COALESCE($2, rate_limit_per_sec),
    enabled            = COALESCE($3, enabled),
    notes              = CASE WHEN $4::bool THEN $5 ELSE notes END,
    updated_at         = now()
WHERE hostname = $1
RETURNING hostname, rate_limit_per_sec, enabled, notes, created_at, updated_at`
    notesProvided := patch.Notes != nil
    r, err := scanImageRegistry(p.pool.QueryRow(ctx, q, hostname,
        patch.RateLimitPerSec, patch.Enabled, notesProvided, patch.Notes))
    if errors.Is(err, pgx.ErrNoRows) {
        return api.ImageRegistry{}, api.ErrNotFound
    }
    if err != nil {
        return api.ImageRegistry{}, fmt.Errorf("update image registry: %w", err)
    }
    return r, nil
}

func (p *PG) DeleteImageRegistry(ctx context.Context, hostname string) error {
    tag, err := p.pool.Exec(ctx, `DELETE FROM image_versions_registries WHERE hostname = $1`, hostname)
    if err != nil {
        return fmt.Errorf("delete image registry: %w", err)
    }
    if tag.RowsAffected() == 0 {
        return api.ErrNotFound
    }
    return nil
}
```

If the helper `isUniqueViolation` does not yet exist in `pg.go`, add it next to other helpers:

```go
import "github.com/jackc/pgx/v5/pgconn"

func isUniqueViolation(err error) bool {
    var pgErr *pgconn.PgError
    return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
```

- [ ] **Step 5: Run tests — they must pass**

```bash
go test -race -run '^TestImageRegistries_' ./internal/store/...
```
Expected: PASS for all three tests.

- [ ] **Step 6: Commit**

```bash
git add internal/api/store.go internal/store/pg_image_registries.go internal/store/pg_image_registries_test.go internal/store/pg.go
git commit -m "feat(store): CRUD for image_versions_registries"
```

---

## Task 4 — Store: `image_versions` CRUD + reaping + discovery

**Files:**
- Create: `internal/store/pg_image_versions.go`
- Create: `internal/store/pg_image_versions_test.go`
- Modify: `internal/api/store.go` (add types + Store interface methods)

- [ ] **Step 1: Add types and interface methods to `internal/api/store.go`**

```go
type ImageVersion struct {
    ImageRepo     string          `json:"image_repo"`
    Variant       string          `json:"variant"`
    Registry      string          `json:"registry"`
    LatestTag     *string         `json:"latest_tag,omitempty"`
    Annotation    json.RawMessage `json:"annotation"`
    Source        string          `json:"source"`
    LastCheckedAt time.Time       `json:"last_checked_at"`
    LastError     *string         `json:"last_error,omitempty"`
    LastErrorAt   *time.Time      `json:"last_error_at,omitempty"`
    CreatedAt     time.Time       `json:"created_at"`
}

type ImageVersionUpsert struct {
    ImageRepo     string
    Variant       string
    Registry      string
    LatestTag     *string
    Annotation    json.RawMessage
    Source        string
    LastCheckedAt time.Time
    LastError     *string
    LastErrorAt   *time.Time
}

type ImageVersionListParams struct {
    Limit             int
    Cursor            string
    Registry          string
    ImageRepoLike     string  // substring match, case-insensitive
    Variant           string
    HasError          *bool
    LastCheckedBefore *time.Time
}

type ImageVersionRepoView struct {
    ImageRepo string         `json:"image_repo"`
    Registry  string         `json:"registry"`
    Variants  []ImageVersion `json:"variants"`
}
```

Append to the `Store` interface:

```go
    // Image versions
    UpsertImageVersion(ctx context.Context, in ImageVersionUpsert) (ImageVersion, error)
    GetImageVersionsByRepo(ctx context.Context, imageRepo string) ([]ImageVersion, error)
    ListImageVersionsByRepo(ctx context.Context, p ImageVersionListParams) (items []ImageVersionRepoView, nextCursor string, err error)
    DeleteImageVersionsNotIn(ctx context.Context, keep [][2]string) (int64, error)
    DistinctImageRefs(ctx context.Context) ([]string, error)
```

- [ ] **Step 2: Write the failing tests `internal/store/pg_image_versions_test.go`**

```go
package store

import (
    "context"
    "encoding/json"
    "testing"
    "time"

    "github.com/sthalbert/longue-vue/internal/api"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
    t.Helper()
    b, err := json.Marshal(v)
    if err != nil { t.Fatalf("marshal: %v", err) }
    return b
}

func TestImageVersions_UpsertAndGet(t *testing.T) {
    pg := newTestPG(t)
    ctx := context.Background()

    ann := mustJSON(t, map[string]any{"latest_available": "1.27.4"})
    latest := "1.27.4"
    now := time.Now().UTC()

    iv, err := pg.UpsertImageVersion(ctx, api.ImageVersionUpsert{
        ImageRepo:     "docker.io/library/nginx",
        Variant:       "",
        Registry:      "docker.io",
        LatestTag:     &latest,
        Annotation:    ann,
        Source:        "registry",
        LastCheckedAt: now,
    })
    if err != nil { t.Fatalf("upsert: %v", err) }
    if iv.LatestTag == nil || *iv.LatestTag != "1.27.4" {
        t.Fatalf("latest_tag mismatch")
    }

    rows, err := pg.GetImageVersionsByRepo(ctx, "docker.io/library/nginx")
    if err != nil { t.Fatalf("get: %v", err) }
    if len(rows) != 1 {
        t.Fatalf("expected 1 row, got %d", len(rows))
    }
}

func TestImageVersions_ReapNotIn(t *testing.T) {
    pg := newTestPG(t)
    ctx := context.Background()
    ann := mustJSON(t, map[string]any{})
    now := time.Now().UTC()

    keep := "1.0.0"
    drop := "0.1.0"
    _, _ = pg.UpsertImageVersion(ctx, api.ImageVersionUpsert{
        ImageRepo: "docker.io/library/keep", Variant: "", Registry: "docker.io",
        LatestTag: &keep, Annotation: ann, Source: "registry", LastCheckedAt: now,
    })
    _, _ = pg.UpsertImageVersion(ctx, api.ImageVersionUpsert{
        ImageRepo: "docker.io/library/drop", Variant: "", Registry: "docker.io",
        LatestTag: &drop, Annotation: ann, Source: "registry", LastCheckedAt: now,
    })

    deleted, err := pg.DeleteImageVersionsNotIn(ctx, [][2]string{
        {"docker.io/library/keep", ""},
    })
    if err != nil { t.Fatalf("reap: %v", err) }
    if deleted != 1 {
        t.Fatalf("expected 1 deleted, got %d", deleted)
    }

    rows, _ := pg.GetImageVersionsByRepo(ctx, "docker.io/library/drop")
    if len(rows) != 0 {
        t.Fatalf("expected drop row gone")
    }
}

func TestImageVersions_DistinctImageRefs(t *testing.T) {
    pg := newTestPG(t)
    ctx := context.Background()

    // Insert workloads/pods with containers JSONB. Use the existing test
    // helpers to seed a cluster + workload.
    cluster := mustSeedCluster(t, pg) // helper assumed to exist; if not, inline a minimal insert

    containers := mustJSON(t, []map[string]any{
        {"name": "web", "image": "nginx:1.25.3"},
        {"name": "side", "image": "quay.io/prometheus/prometheus:v2.45.0"},
    })
    _, err := pg.pool.Exec(ctx, `
INSERT INTO workloads (id, cluster_id, namespace_id, kind, name, layer, containers)
VALUES (gen_random_uuid(), $1, NULL, 'Deployment', 'app', 'unknown', $2)`,
        cluster.ID, containers)
    if err != nil { t.Fatalf("seed workload: %v", err) }

    refs, err := pg.DistinctImageRefs(ctx)
    if err != nil { t.Fatalf("distinct: %v", err) }
    seen := map[string]bool{}
    for _, r := range refs {
        seen[r] = true
    }
    if !seen["nginx:1.25.3"] || !seen["quay.io/prometheus/prometheus:v2.45.0"] {
        t.Fatalf("missing expected refs in %v", refs)
    }
}
```

If `mustSeedCluster` does not exist as a helper, create it inline as needed by inspecting how other tests in `pg_test.go` insert clusters; the schema is documented in CLAUDE.md.

- [ ] **Step 3: Run tests — they must fail**

```bash
go test -race -run '^TestImageVersions_' ./internal/store/...
```
Expected: FAIL ("method UpsertImageVersion undefined" etc.).

- [ ] **Step 4: Implement `internal/store/pg_image_versions.go`**

```go
package store

import (
    "context"
    "encoding/base64"
    "encoding/json"
    "errors"
    "fmt"
    "strings"
    "time"

    "github.com/jackc/pgx/v5"

    "github.com/sthalbert/longue-vue/internal/api"
)

func scanImageVersion(row pgx.Row) (api.ImageVersion, error) {
    var iv api.ImageVersion
    var ann []byte
    if err := row.Scan(
        &iv.ImageRepo, &iv.Variant, &iv.Registry, &iv.LatestTag, &ann,
        &iv.Source, &iv.LastCheckedAt, &iv.LastError, &iv.LastErrorAt, &iv.CreatedAt,
    ); err != nil {
        return api.ImageVersion{}, err
    }
    iv.Annotation = json.RawMessage(ann)
    return iv, nil
}

func (p *PG) UpsertImageVersion(ctx context.Context, in api.ImageVersionUpsert) (api.ImageVersion, error) {
    const q = `
INSERT INTO image_versions
  (image_repo, variant, registry, latest_tag, annotation, source,
   last_checked_at, last_error, last_error_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (image_repo, variant) DO UPDATE SET
  registry        = EXCLUDED.registry,
  latest_tag      = EXCLUDED.latest_tag,
  annotation      = EXCLUDED.annotation,
  source          = EXCLUDED.source,
  last_checked_at = EXCLUDED.last_checked_at,
  last_error      = EXCLUDED.last_error,
  last_error_at   = EXCLUDED.last_error_at
RETURNING image_repo, variant, registry, latest_tag, annotation, source,
          last_checked_at, last_error, last_error_at, created_at`
    iv, err := scanImageVersion(p.pool.QueryRow(ctx, q,
        in.ImageRepo, in.Variant, in.Registry, in.LatestTag, []byte(in.Annotation),
        in.Source, in.LastCheckedAt, in.LastError, in.LastErrorAt))
    if err != nil {
        return api.ImageVersion{}, fmt.Errorf("upsert image version: %w", err)
    }
    return iv, nil
}

func (p *PG) GetImageVersionsByRepo(ctx context.Context, imageRepo string) ([]api.ImageVersion, error) {
    const q = `
SELECT image_repo, variant, registry, latest_tag, annotation, source,
       last_checked_at, last_error, last_error_at, created_at
FROM image_versions WHERE image_repo = $1
ORDER BY variant`
    rows, err := p.pool.Query(ctx, q, imageRepo)
    if err != nil {
        return nil, fmt.Errorf("get image versions by repo: %w", err)
    }
    defer rows.Close()
    var out []api.ImageVersion
    for rows.Next() {
        iv, err := scanImageVersion(rows)
        if err != nil {
            return nil, fmt.Errorf("scan: %w", err)
        }
        out = append(out, iv)
    }
    return out, rows.Err()
}

func (p *PG) ListImageVersionsByRepo(ctx context.Context, lp api.ImageVersionListParams) ([]api.ImageVersionRepoView, string, error) {
    if lp.Limit <= 0 || lp.Limit > 200 {
        lp.Limit = 50
    }

    var conds []string
    var args []any
    if lp.Registry != "" {
        args = append(args, lp.Registry)
        conds = append(conds, fmt.Sprintf("registry = $%d", len(args)))
    }
    if lp.ImageRepoLike != "" {
        args = append(args, "%"+escapeLike(lp.ImageRepoLike)+"%")
        conds = append(conds, fmt.Sprintf("image_repo ILIKE $%d ESCAPE '\\'", len(args)))
    }
    if lp.Variant != "" {
        args = append(args, lp.Variant)
        conds = append(conds, fmt.Sprintf("variant = $%d", len(args)))
    }
    if lp.HasError != nil {
        if *lp.HasError {
            conds = append(conds, "last_error IS NOT NULL")
        } else {
            conds = append(conds, "last_error IS NULL")
        }
    }
    if lp.LastCheckedBefore != nil {
        args = append(args, *lp.LastCheckedBefore)
        conds = append(conds, fmt.Sprintf("last_checked_at < $%d", len(args)))
    }
    if lp.Cursor != "" {
        decoded, err := decodeImageRepoCursor(lp.Cursor)
        if err != nil {
            return nil, "", fmt.Errorf("invalid cursor: %w", err)
        }
        args = append(args, decoded)
        conds = append(conds, fmt.Sprintf("image_repo > $%d", len(args)))
    }

    where := ""
    if len(conds) > 0 {
        where = "WHERE " + strings.Join(conds, " AND ")
    }
    args = append(args, lp.Limit+1)
    limitIdx := len(args)

    q := fmt.Sprintf(`
SELECT image_repo, variant, registry, latest_tag, annotation, source,
       last_checked_at, last_error, last_error_at, created_at
FROM image_versions
%s
ORDER BY image_repo, variant
LIMIT $%d`, where, limitIdx)

    rows, err := p.pool.Query(ctx, q, args...)
    if err != nil {
        return nil, "", fmt.Errorf("list image versions: %w", err)
    }
    defer rows.Close()

    grouped := map[string]*api.ImageVersionRepoView{}
    var order []string
    for rows.Next() {
        iv, err := scanImageVersion(rows)
        if err != nil {
            return nil, "", fmt.Errorf("scan: %w", err)
        }
        v, ok := grouped[iv.ImageRepo]
        if !ok {
            v = &api.ImageVersionRepoView{ImageRepo: iv.ImageRepo, Registry: iv.Registry}
            grouped[iv.ImageRepo] = v
            order = append(order, iv.ImageRepo)
        }
        v.Variants = append(v.Variants, iv)
    }

    next := ""
    items := make([]api.ImageVersionRepoView, 0, len(order))
    for _, k := range order {
        items = append(items, *grouped[k])
    }
    if len(items) > lp.Limit {
        items = items[:lp.Limit]
        next = encodeImageRepoCursor(items[len(items)-1].ImageRepo)
    }
    return items, next, nil
}

func (p *PG) DeleteImageVersionsNotIn(ctx context.Context, keep [][2]string) (int64, error) {
    if len(keep) == 0 {
        tag, err := p.pool.Exec(ctx, `DELETE FROM image_versions`)
        if err != nil {
            return 0, fmt.Errorf("delete all image versions: %w", err)
        }
        return tag.RowsAffected(), nil
    }
    repos := make([]string, len(keep))
    variants := make([]string, len(keep))
    for i, k := range keep {
        repos[i] = k[0]
        variants[i] = k[1]
    }
    const q = `
DELETE FROM image_versions
WHERE (image_repo, variant) NOT IN (
    SELECT * FROM unnest($1::text[], $2::text[])
)`
    tag, err := p.pool.Exec(ctx, q, repos, variants)
    if err != nil {
        return 0, fmt.Errorf("reap image versions: %w", err)
    }
    return tag.RowsAffected(), nil
}

func (p *PG) DistinctImageRefs(ctx context.Context) ([]string, error) {
    const q = `
SELECT DISTINCT image FROM (
    SELECT jsonb_array_elements(containers)->>'image' AS image FROM workloads
    WHERE containers IS NOT NULL AND jsonb_typeof(containers) = 'array'
    UNION
    SELECT jsonb_array_elements(containers)->>'image' AS image FROM pods
    WHERE containers IS NOT NULL AND jsonb_typeof(containers) = 'array'
) u
WHERE image IS NOT NULL AND image <> ''`
    rows, err := p.pool.Query(ctx, q)
    if err != nil {
        return nil, fmt.Errorf("distinct image refs: %w", err)
    }
    defer rows.Close()
    var out []string
    for rows.Next() {
        var s string
        if err := rows.Scan(&s); err != nil {
            return nil, fmt.Errorf("scan ref: %w", err)
        }
        out = append(out, s)
    }
    return out, rows.Err()
}

func encodeImageRepoCursor(repo string) string {
    return base64.URLEncoding.EncodeToString([]byte(repo))
}

func decodeImageRepoCursor(c string) (string, error) {
    b, err := base64.URLEncoding.DecodeString(c)
    if err != nil {
        return "", err
    }
    return string(b), nil
}

// Reuse existing escapeLike in pg.go if present; otherwise:
// var _ = errors.New // guard import in case unused on a particular branch
var _ = errors.New
```

If `escapeLike` does not exist in `pg.go`, add it next to other helpers:

```go
func escapeLike(s string) string {
    s = strings.ReplaceAll(s, `\`, `\\`)
    s = strings.ReplaceAll(s, `%`, `\%`)
    s = strings.ReplaceAll(s, `_`, `\_`)
    return s
}
```

- [ ] **Step 5: Run tests — they must pass**

```bash
go test -race -run '^TestImageVersions_' ./internal/store/...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/store.go internal/store/pg_image_versions.go internal/store/pg_image_versions_test.go internal/store/pg.go
git commit -m "feat(store): image_versions CRUD, list, reaping, and ref discovery"
```

---

## Task 5 — Image ref + tag parsing

**Files:**
- Create: `internal/imageversions/parse.go`
- Create: `internal/imageversions/parse_test.go`

- [ ] **Step 1: Add `github.com/distribution/reference` to go.mod**

```bash
go get github.com/distribution/reference
go mod tidy
```

- [ ] **Step 2: Write the failing tests `internal/imageversions/parse_test.go`**

```go
package imageversions

import "testing"

func TestParseImageRef(t *testing.T) {
    tests := []struct {
        in       string
        wantRepo string
        wantTag  string
        wantSkip bool
    }{
        {"nginx", "docker.io/library/nginx", "", true},                    // implicit :latest -> skip
        {"nginx:latest", "docker.io/library/nginx", "", true},             // explicit :latest -> skip
        {"nginx:1.25.3", "docker.io/library/nginx", "1.25.3", false},
        {"library/nginx:1.25.3-alpine", "docker.io/library/nginx", "1.25.3-alpine", false},
        {"docker.io/library/nginx:1.27.0", "docker.io/library/nginx", "1.27.0", false},
        {"quay.io/prometheus/prometheus:v2.45.0", "quay.io/prometheus/prometheus", "v2.45.0", false},
        {"ghcr.io/foo/bar:1.0.0", "ghcr.io/foo/bar", "1.0.0", false},
        {"nginx@sha256:abc123def4567890abc123def4567890abc123def4567890abc123def4567890ab", "", "", true}, // digest only -> skip
        {"nginx:1.25@sha256:abc123def4567890abc123def4567890abc123def4567890abc123def4567890ab", "docker.io/library/nginx", "1.25", false},
        {"", "", "", true},
        {"@@invalid@@", "", "", true},
    }
    for _, tc := range tests {
        t.Run(tc.in, func(t *testing.T) {
            ref, err := ParseImageRef(tc.in)
            if tc.wantSkip {
                if err == nil {
                    t.Fatalf("expected skip/error, got %+v", ref)
                }
                return
            }
            if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
            if ref.ImageRepo != tc.wantRepo {
                t.Errorf("repo: want %q, got %q", tc.wantRepo, ref.ImageRepo)
            }
            if ref.Tag != tc.wantTag {
                t.Errorf("tag: want %q, got %q", tc.wantTag, ref.Tag)
            }
        })
    }
}

func TestParseTag(t *testing.T) {
    tests := []struct {
        in           string
        wantVersion  string // semver-ish prefix as captured
        wantVariant  string
        wantPre      bool
        wantSkip     bool
    }{
        {"1.25.3", "1.25.3", "", false, false},
        {"v1.25.3", "1.25.3", "", false, false},
        {"1.25.3-alpine", "1.25.3", "alpine", false, false},
        {"1.25.3-alpine3.18", "1.25.3", "alpine3.18", false, false},
        {"1.25.3-debian-12", "1.25.3", "debian-12", false, false},
        {"1.25.3-rc1", "1.25.3", "", true, false},
        {"1.25.3-beta", "1.25.3", "", true, false},
        {"1.25.3-alpha.2", "1.25.3", "", true, false},
        {"1.25.3-rc1-alpine", "", "", false, true}, // ambiguous -> skip
        {"latest", "", "", false, true},
        {"master", "", "", false, true},
        {"main", "", "", false, true},
        {"sha-abc123", "", "", false, true},
        {"2024.01.15", "", "", false, true},
        {"", "", "", false, true},
    }
    for _, tc := range tests {
        t.Run(tc.in, func(t *testing.T) {
            p, err := ParseTag(tc.in)
            if tc.wantSkip {
                if err == nil {
                    t.Fatalf("expected skip, got %+v", p)
                }
                return
            }
            if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
            if p.Version.String() != tc.wantVersion {
                t.Errorf("version: want %q, got %q", tc.wantVersion, p.Version.String())
            }
            if p.Variant != tc.wantVariant {
                t.Errorf("variant: want %q, got %q", tc.wantVariant, p.Variant)
            }
            if p.IsPrerelease != tc.wantPre {
                t.Errorf("prerelease: want %v, got %v", tc.wantPre, p.IsPrerelease)
            }
        })
    }
}
```

- [ ] **Step 3: Run tests — they must fail**

```bash
go test -race -run '^TestParse' ./internal/imageversions/...
```
Expected: FAIL (package doesn't exist).

- [ ] **Step 4: Implement `internal/imageversions/parse.go`**

```go
// Package imageversions enriches container images used in Kubernetes clusters
// with the latest available tag from their source registry.
package imageversions

import (
    "errors"
    "fmt"
    "regexp"
    "strings"

    "github.com/distribution/reference"
    "golang.org/x/mod/semver"
)

var (
    ErrSkip = errors.New("imageversions: skip")
)

// Ref is the canonical, parsed form of an image reference.
type Ref struct {
    ImageRepo string // e.g. "docker.io/library/nginx" (always fully qualified)
    Registry  string // e.g. "docker.io"
    Tag       string // e.g. "1.25.3-alpine"
}

// ParseImageRef parses a raw image string into a canonical Ref.
// Returns ErrSkip if the image cannot be enriched (no tag, digest-only,
// invalid format, etc.).
func ParseImageRef(s string) (Ref, error) {
    if s == "" {
        return Ref{}, fmt.Errorf("%w: empty image string", ErrSkip)
    }
    named, err := reference.ParseNormalizedNamed(s)
    if err != nil {
        return Ref{}, fmt.Errorf("%w: %v", ErrSkip, err)
    }
    full := named.Name() // canonical: "docker.io/library/nginx"
    registry := reference.Domain(named)
    tag := ""
    if t, ok := named.(reference.Tagged); ok {
        tag = t.Tag()
    }
    if tag == "" || tag == "latest" {
        return Ref{ImageRepo: full, Registry: registry}, fmt.Errorf("%w: no usable tag (%q)", ErrSkip, tag)
    }
    return Ref{ImageRepo: full, Registry: registry, Tag: tag}, nil
}

// ParsedTag captures the semver prefix, optional variant suffix, and
// whether the tag is a prerelease.
type ParsedTag struct {
    Original     string
    Version      Version
    Variant      string
    IsPrerelease bool
}

// Version is a thin wrapper around the golang.org/x/mod/semver string form
// (e.g., "v1.25.3"). We use string compare via semver.Compare for ordering.
type Version struct {
    raw string // canonical "vX.Y.Z" form
}

func (v Version) String() string {
    return strings.TrimPrefix(v.raw, "v")
}

// GT reports whether v is strictly greater than other.
func (v Version) GT(other Version) bool {
    return semver.Compare(v.raw, other.raw) > 0
}

var (
    semverPrefixRe = regexp.MustCompile(`^v?(\d+(?:\.\d+){0,2})`)
    prereleaseStarts = []string{"alpha", "beta", "rc", "pre", "dev", "snapshot", "nightly"}
)

// ParseTag classifies a tag into (version, variant, prerelease).
// Returns ErrSkip when the tag does not begin with a semver-shaped number,
// or when it ambiguously combines a prerelease and a variant.
func ParseTag(s string) (ParsedTag, error) {
    if s == "" {
        return ParsedTag{}, fmt.Errorf("%w: empty tag", ErrSkip)
    }
    m := semverPrefixRe.FindStringSubmatchIndex(s)
    if m == nil {
        return ParsedTag{}, fmt.Errorf("%w: no semver prefix in %q", ErrSkip, s)
    }
    versionStr := s[m[2]:m[3]]
    rest := s[m[1]:]

    // Pad to 3 components so semver lib accepts it.
    full := versionStr
    parts := strings.Split(full, ".")
    for len(parts) < 3 {
        parts = append(parts, "0")
    }
    canonical := "v" + strings.Join(parts, ".")
    if !semver.IsValid(canonical) {
        return ParsedTag{}, fmt.Errorf("%w: not a valid semver: %q", ErrSkip, canonical)
    }

    pt := ParsedTag{
        Original: s,
        Version:  Version{raw: canonical},
    }

    if rest == "" {
        return pt, nil
    }
    // Trim a leading separator dash.
    suffix := strings.TrimPrefix(rest, "-")
    if suffix == rest {
        // No dash separator after the semver -> not parseable for our purposes.
        return ParsedTag{}, fmt.Errorf("%w: unexpected suffix shape %q", ErrSkip, rest)
    }

    // Detect prerelease.
    isPre := false
    lower := strings.ToLower(suffix)
    for _, p := range prereleaseStarts {
        if strings.HasPrefix(lower, p) {
            isPre = true
            break
        }
    }

    if isPre {
        // Reject mixed "rc1-alpine" style as ambiguous.
        if strings.Contains(suffix, "-") {
            return ParsedTag{}, fmt.Errorf("%w: ambiguous prerelease+variant: %q", ErrSkip, s)
        }
        pt.IsPrerelease = true
        return pt, nil
    }

    pt.Variant = suffix
    return pt, nil
}
```

- [ ] **Step 5: Run tests — they must pass**

```bash
go test -race -run '^TestParse' ./internal/imageversions/...
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/imageversions/parse.go internal/imageversions/parse_test.go go.mod go.sum
git commit -m "feat(imageversions): image ref and tag parsing"
```

---

## Task 6 — Latest computation

**Files:**
- Create: `internal/imageversions/latest.go`
- Create: `internal/imageversions/latest_test.go`

- [ ] **Step 1: Write the failing test `internal/imageversions/latest_test.go`**

```go
package imageversions

import "testing"

func TestComputeLatest(t *testing.T) {
    tags := []string{
        "latest", "stable", "master",
        "1.24.0", "1.25.0", "1.25.3", "1.27.0", "1.27.4",
        "1.27.5-rc1",
        "1.25.3-alpine", "1.27.0-alpine", "1.27.4-alpine",
        "1.25.3-debian-12", "1.27.4-debian-12",
        "sha-abc123",
    }

    cases := []struct {
        name    string
        variant string
        want    string
    }{
        {"pure semver", "", "1.27.4"},
        {"alpine variant", "alpine", "1.27.4-alpine"},
        {"debian-12 variant", "debian-12", "1.27.4-debian-12"},
        {"unknown variant", "windows-server-core", ""},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got, err := ComputeLatest(tc.variant, tags)
            if err != nil {
                t.Fatalf("unexpected error: %v", err)
            }
            if got != tc.want {
                t.Errorf("variant=%q: want %q, got %q", tc.variant, tc.want, got)
            }
        })
    }
}

func TestComputeLatest_EmptyTags(t *testing.T) {
    got, err := ComputeLatest("", nil)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if got != "" {
        t.Errorf("expected empty result, got %q", got)
    }
}

func TestComputeLatest_AllPrerelease(t *testing.T) {
    got, _ := ComputeLatest("", []string{"1.0.0-rc1", "1.0.0-rc2", "1.0.0-beta"})
    if got != "" {
        t.Errorf("expected empty when only prereleases, got %q", got)
    }
}
```

- [ ] **Step 2: Run tests — they must fail**

```bash
go test -race -run '^TestComputeLatest' ./internal/imageversions/...
```
Expected: FAIL (function not defined).

- [ ] **Step 3: Implement `internal/imageversions/latest.go`**

```go
package imageversions

import "sort"

// ComputeLatest returns the highest non-prerelease tag whose variant matches.
// Returns "" (no error) when no tag in the input matches the variant family.
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
        return "", nil
    }
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].Version.GT(candidates[j].Version)
    })
    return candidates[0].Original, nil
}
```

- [ ] **Step 4: Run tests — they must pass**

```bash
go test -race -run '^TestComputeLatest' ./internal/imageversions/...
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/imageversions/latest.go internal/imageversions/latest_test.go
git commit -m "feat(imageversions): pattern-aware latest tag computation"
```

---

## Task 7 — Hostname matching (exact + leading-`*` suffix)

**Files:**
- Create: `internal/imageversions/registry/match.go`
- Create: `internal/imageversions/registry/match_test.go`

- [ ] **Step 1: Write the failing test `internal/imageversions/registry/match_test.go`**

```go
package registry

import "testing"

func TestMatchHostname(t *testing.T) {
    cases := []struct {
        pattern string
        host    string
        want    bool
    }{
        {"docker.io", "docker.io", true},
        {"docker.io", "ghcr.io", false},
        {"*-docker.pkg.dev", "europe-west1-docker.pkg.dev", true},
        {"*-docker.pkg.dev", "us-central1-docker.pkg.dev", true},
        {"*-docker.pkg.dev", "docker.pkg.dev", false}, // suffix only, requires non-empty leading
        {"*-docker.pkg.dev", "docker.io", false},
        {"*", "anything", true}, // wildcard-only matches anything non-empty
        {"*", "", false},
        {"", "x", false},
    }
    for _, tc := range cases {
        t.Run(tc.pattern+"|"+tc.host, func(t *testing.T) {
            got := Match(tc.pattern, tc.host)
            if got != tc.want {
                t.Errorf("Match(%q, %q): want %v, got %v", tc.pattern, tc.host, tc.want, got)
            }
        })
    }
}
```

- [ ] **Step 2: Run test — it must fail**

```bash
go test -race -run '^TestMatchHostname$' ./internal/imageversions/registry/
```
Expected: FAIL.

- [ ] **Step 3: Implement `internal/imageversions/registry/match.go`**

```go
// Package registry contains the OCI-distribution registry client and the
// hostname allowlist matcher used by the imageversions enricher.
package registry

import "strings"

// Match reports whether host satisfies the given pattern.
// Patterns are either exact hostnames ("docker.io") or "*<suffix>" where
// the leading "*" matches any non-empty string. "*" alone matches anything
// non-empty.
func Match(pattern, host string) bool {
    if pattern == "" || host == "" {
        return false
    }
    if !strings.HasPrefix(pattern, "*") {
        return pattern == host
    }
    suffix := pattern[1:]
    if suffix == "" {
        return host != ""
    }
    if !strings.HasSuffix(host, suffix) {
        return false
    }
    return len(host) > len(suffix)
}
```

- [ ] **Step 4: Run test — it must pass**

```bash
go test -race -run '^TestMatchHostname$' ./internal/imageversions/registry/
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/imageversions/registry/match.go internal/imageversions/registry/match_test.go
git commit -m "feat(imageversions/registry): hostname pattern matcher"
```

---

## Task 8 — Generic OCI registry client (auth + pagination)

**Files:**
- Create: `internal/imageversions/registry/client.go`
- Create: `internal/imageversions/registry/client_test.go`

- [ ] **Step 1: Write the failing test `internal/imageversions/registry/client_test.go`**

```go
package registry

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

// fakeRegistry serves a /v2/<repo>/tags/list endpoint with optional bearer-auth
// challenge and Link-header pagination.
func newFakeRegistry(t *testing.T, requireAuth bool, pages [][]string) *httptest.Server {
    var tokenIssuer *httptest.Server
    if requireAuth {
        tokenIssuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            json.NewEncoder(w).Encode(map[string]any{"token": "fake-token", "expires_in": 300})
        }))
        t.Cleanup(tokenIssuer.Close)
    }
    var srv *httptest.Server
    srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !strings.HasPrefix(r.URL.Path, "/v2/") || !strings.HasSuffix(r.URL.Path, "/tags/list") {
            http.NotFound(w, r)
            return
        }
        if requireAuth && r.Header.Get("Authorization") != "Bearer fake-token" {
            w.Header().Set("WWW-Authenticate",
                `Bearer realm="`+tokenIssuer.URL+`",service="fake",scope="repository:foo:pull"`)
            http.Error(w, "auth required", http.StatusUnauthorized)
            return
        }
        page := 0
        if p := r.URL.Query().Get("page"); p == "1" {
            page = 1
        }
        if page >= len(pages) {
            json.NewEncoder(w).Encode(map[string]any{"name": "foo", "tags": []string{}})
            return
        }
        if page+1 < len(pages) {
            w.Header().Set("Link", `<`+srv.URL+`/v2/foo/tags/list?page=1>; rel="next"`)
        }
        json.NewEncoder(w).Encode(map[string]any{"name": "foo", "tags": pages[page]})
    }))
    t.Cleanup(srv.Close)
    return srv
}

func TestListTags_NoAuth(t *testing.T) {
    srv := newFakeRegistry(t, false, [][]string{{"1.0.0", "1.1.0"}})
    c := NewClient()
    tags, err := c.ListTags(context.Background(), srv.URL, "foo")
    if err != nil { t.Fatalf("list: %v", err) }
    if len(tags) != 2 || tags[0] != "1.0.0" || tags[1] != "1.1.0" {
        t.Fatalf("unexpected tags: %v", tags)
    }
}

func TestListTags_BearerAuth(t *testing.T) {
    srv := newFakeRegistry(t, true, [][]string{{"1.0.0"}})
    c := NewClient()
    tags, err := c.ListTags(context.Background(), srv.URL, "foo")
    if err != nil { t.Fatalf("list: %v", err) }
    if len(tags) != 1 || tags[0] != "1.0.0" {
        t.Fatalf("unexpected tags: %v", tags)
    }
}

func TestListTags_Pagination(t *testing.T) {
    srv := newFakeRegistry(t, false, [][]string{
        {"1.0.0", "1.1.0"},
        {"1.2.0"},
    })
    c := NewClient()
    tags, err := c.ListTags(context.Background(), srv.URL, "foo")
    if err != nil { t.Fatalf("list: %v", err) }
    if len(tags) != 3 {
        t.Fatalf("expected 3 tags after pagination, got %d", len(tags))
    }
}
```

- [ ] **Step 2: Run tests — they must fail**

```bash
go test -race -run '^TestListTags' ./internal/imageversions/registry/
```
Expected: FAIL.

- [ ] **Step 3: Implement `internal/imageversions/registry/client.go`**

```go
package registry

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "regexp"
    "strings"
    "time"
)

const (
    maxPages    = 50
    httpTimeout = 30 * time.Second
)

// Client is a thin OCI-distribution client supporting anonymous-bearer
// auth and Link-header pagination.
type Client struct {
    http      *http.Client
    userAgent string
}

func NewClient() *Client {
    return &Client{
        http:      &http.Client{Timeout: httpTimeout},
        userAgent: "longue-vue (image-versions-enricher)",
    }
}

// ListTags fetches all tags for repo from the given registryURL
// (e.g., "https://registry-1.docker.io"). Paginated responses are
// followed until the Link header is exhausted or maxPages is reached.
func (c *Client) ListTags(ctx context.Context, registryURL, repo string) ([]string, error) {
    next := strings.TrimRight(registryURL, "/") + "/v2/" + repo + "/tags/list?n=100"
    var token string
    var allTags []string
    for page := 0; page < maxPages && next != ""; page++ {
        req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
        if err != nil {
            return nil, fmt.Errorf("build request: %w", err)
        }
        req.Header.Set("User-Agent", c.userAgent)
        req.Header.Set("Accept", "application/json")
        if token != "" {
            req.Header.Set("Authorization", "Bearer "+token)
        }
        resp, err := c.http.Do(req)
        if err != nil {
            return nil, fmt.Errorf("http: %w", err)
        }
        if resp.StatusCode == http.StatusUnauthorized && token == "" {
            chal := resp.Header.Get("WWW-Authenticate")
            resp.Body.Close()
            t, err := c.fetchToken(ctx, chal)
            if err != nil {
                return nil, fmt.Errorf("token: %w", err)
            }
            token = t
            continue // retry same URL with token
        }
        if resp.StatusCode == http.StatusNotFound {
            resp.Body.Close()
            return nil, ErrRepoNotFound
        }
        if resp.StatusCode == http.StatusTooManyRequests {
            resp.Body.Close()
            return nil, ErrRateLimited
        }
        if resp.StatusCode >= 400 {
            body, _ := io.ReadAll(resp.Body)
            resp.Body.Close()
            return nil, fmt.Errorf("registry status %d: %s", resp.StatusCode, string(body))
        }

        var body struct {
            Tags []string `json:"tags"`
        }
        if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
            resp.Body.Close()
            return nil, fmt.Errorf("decode: %w", err)
        }
        link := resp.Header.Get("Link")
        resp.Body.Close()
        allTags = append(allTags, body.Tags...)
        next = parseNextLink(link, registryURL)
    }
    return allTags, nil
}

var (
    ErrRepoNotFound = fmt.Errorf("registry: repo not found")
    ErrRateLimited  = fmt.Errorf("registry: rate limited")
)

var bearerRealmRe = regexp.MustCompile(`Bearer\s+(.+)`)

// fetchToken interprets a WWW-Authenticate Bearer challenge and fetches
// an anonymous token.
func (c *Client) fetchToken(ctx context.Context, challenge string) (string, error) {
    m := bearerRealmRe.FindStringSubmatch(challenge)
    if m == nil {
        return "", fmt.Errorf("not a bearer challenge: %q", challenge)
    }
    params := parseChallengeParams(m[1])
    realm := params["realm"]
    if realm == "" {
        return "", fmt.Errorf("missing realm in challenge: %q", challenge)
    }
    u, err := url.Parse(realm)
    if err != nil {
        return "", fmt.Errorf("parse realm: %w", err)
    }
    q := u.Query()
    if s, ok := params["service"]; ok {
        q.Set("service", s)
    }
    if s, ok := params["scope"]; ok {
        q.Set("scope", s)
    }
    u.RawQuery = q.Encode()

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
    if err != nil {
        return "", err
    }
    req.Header.Set("User-Agent", c.userAgent)
    resp, err := c.http.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    if resp.StatusCode >= 400 {
        return "", fmt.Errorf("token fetch status %d", resp.StatusCode)
    }
    var tok struct {
        Token       string `json:"token"`
        AccessToken string `json:"access_token"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
        return "", err
    }
    if tok.Token != "" {
        return tok.Token, nil
    }
    return tok.AccessToken, nil
}

// parseChallengeParams parses comma-separated key="value" pairs.
func parseChallengeParams(s string) map[string]string {
    out := map[string]string{}
    for _, part := range splitChallenge(s) {
        eq := strings.Index(part, "=")
        if eq < 0 {
            continue
        }
        k := strings.TrimSpace(part[:eq])
        v := strings.Trim(strings.TrimSpace(part[eq+1:]), `"`)
        out[k] = v
    }
    return out
}

// splitChallenge splits on commas that are not inside quotes.
func splitChallenge(s string) []string {
    var out []string
    var cur strings.Builder
    inQuotes := false
    for _, r := range s {
        switch {
        case r == '"':
            inQuotes = !inQuotes
            cur.WriteRune(r)
        case r == ',' && !inQuotes:
            out = append(out, cur.String())
            cur.Reset()
        default:
            cur.WriteRune(r)
        }
    }
    if cur.Len() > 0 {
        out = append(out, cur.String())
    }
    return out
}

var nextLinkRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

// parseNextLink extracts the next-page URL from a Link header. base is
// used only as a fallback context (the URL is typically absolute).
func parseNextLink(header, base string) string {
    if header == "" {
        return ""
    }
    m := nextLinkRe.FindStringSubmatch(header)
    if m == nil {
        return ""
    }
    candidate := m[1]
    if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
        return candidate
    }
    return strings.TrimRight(base, "/") + candidate
}
```

- [ ] **Step 4: Run tests — they must pass**

```bash
go test -race -run '^TestListTags' ./internal/imageversions/registry/
```
Expected: PASS for all three cases.

- [ ] **Step 5: Commit**

```bash
git add internal/imageversions/registry/client.go internal/imageversions/registry/client_test.go
git commit -m "feat(imageversions/registry): OCI client with bearer auth and pagination"
```

---

## Task 9 — Registry hostname → effective HTTP host translation

**Files:**
- Create: `internal/imageversions/registry/registries.go`
- Create: `internal/imageversions/registry/registries_test.go`

- [ ] **Step 1: Write the failing test `internal/imageversions/registry/registries_test.go`**

```go
package registry

import "testing"

func TestEffectiveHost(t *testing.T) {
    cases := map[string]struct {
        wantURL    string
        wantRepo   string
    }{
        "docker.io/library/nginx":              {"https://registry-1.docker.io", "library/nginx"},
        "ghcr.io/foo/bar":                      {"https://ghcr.io", "foo/bar"},
        "quay.io/prometheus/prometheus":        {"https://quay.io", "prometheus/prometheus"},
        "gcr.io/google-containers/etcd":        {"https://gcr.io", "google-containers/etcd"},
        "registry.k8s.io/kube-apiserver":       {"https://registry.k8s.io", "kube-apiserver"},
        "public.ecr.aws/amazonlinux/foo":       {"https://public.ecr.aws", "amazonlinux/foo"},
        "europe-west1-docker.pkg.dev/p/r/i":    {"https://europe-west1-docker.pkg.dev", "p/r/i"},
    }
    for repo, exp := range cases {
        t.Run(repo, func(t *testing.T) {
            url, path, err := EffectiveHost(repo)
            if err != nil { t.Fatalf("err: %v", err) }
            if url != exp.wantURL || path != exp.wantRepo {
                t.Errorf("got (%q, %q), want (%q, %q)", url, path, exp.wantURL, exp.wantRepo)
            }
        })
    }
}
```

- [ ] **Step 2: Run test — it must fail**

```bash
go test -race -run '^TestEffectiveHost$' ./internal/imageversions/registry/
```
Expected: FAIL.

- [ ] **Step 3: Implement `internal/imageversions/registry/registries.go`**

```go
package registry

import (
    "fmt"
    "strings"
)

// hostTranslate maps a canonical registry hostname (as stored in image_repo
// or in the image_versions_registries table) to the effective HTTPS URL
// the OCI client should call. Most registries are identity; Docker Hub is
// the well-known exception.
var hostTranslate = map[string]string{
    "docker.io": "https://registry-1.docker.io",
}

// EffectiveHost takes a fully-qualified image_repo (e.g. "docker.io/library/nginx")
// and returns (registryURL, repoPath) such that the OCI client can call
// <registryURL>/v2/<repoPath>/tags/list.
func EffectiveHost(imageRepo string) (string, string, error) {
    slash := strings.Index(imageRepo, "/")
    if slash < 0 {
        return "", "", fmt.Errorf("invalid image_repo (no slash): %q", imageRepo)
    }
    host := imageRepo[:slash]
    repo := imageRepo[slash+1:]
    if mapped, ok := hostTranslate[host]; ok {
        return mapped, repo, nil
    }
    return "https://" + host, repo, nil
}
```

- [ ] **Step 4: Run test — it must pass**

```bash
go test -race -run '^TestEffectiveHost$' ./internal/imageversions/registry/
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/imageversions/registry/registries.go internal/imageversions/registry/registries_test.go
git commit -m "feat(imageversions/registry): hostname → effective HTTPS host"
```

---

## Task 10 — Enricher loop, tick algorithm, trigger channel

**Files:**
- Create: `internal/imageversions/types.go`
- Create: `internal/imageversions/enricher.go`
- Create: `internal/imageversions/enricher_test.go`

- [ ] **Step 1: Define the store-facing interface and types in `internal/imageversions/types.go`**

```go
package imageversions

import (
    "context"

    "github.com/sthalbert/longue-vue/internal/api"
)

// Store is the subset of api.Store used by the enricher. Defining a narrow
// interface here keeps tests trivial and the dependency direction clean.
type Store interface {
    GetSettings(ctx context.Context) (api.Settings, error)
    ListImageRegistries(ctx context.Context) ([]api.ImageRegistry, error)
    DistinctImageRefs(ctx context.Context) ([]string, error)
    UpsertImageVersion(ctx context.Context, in api.ImageVersionUpsert) (api.ImageVersion, error)
    DeleteImageVersionsNotIn(ctx context.Context, keep [][2]string) (int64, error)
}

// TagsLister abstracts the OCI client for testing.
type TagsLister interface {
    ListTags(ctx context.Context, registryURL, repoPath string) ([]string, error)
}
```

- [ ] **Step 2: Write the failing test `internal/imageversions/enricher_test.go`**

```go
package imageversions

import (
    "context"
    "encoding/json"
    "errors"
    "sync"
    "testing"
    "time"

    "github.com/sthalbert/longue-vue/internal/api"
)

type fakeStore struct {
    mu                sync.Mutex
    settings          api.Settings
    registries        []api.ImageRegistry
    refs              []string
    upserted          []api.ImageVersionUpsert
    reaped            [][][2]string
    upsertErr         error
}

func (s *fakeStore) GetSettings(_ context.Context) (api.Settings, error) {
    s.mu.Lock(); defer s.mu.Unlock()
    return s.settings, nil
}
func (s *fakeStore) ListImageRegistries(_ context.Context) ([]api.ImageRegistry, error) {
    return s.registries, nil
}
func (s *fakeStore) DistinctImageRefs(_ context.Context) ([]string, error) {
    return s.refs, nil
}
func (s *fakeStore) UpsertImageVersion(_ context.Context, in api.ImageVersionUpsert) (api.ImageVersion, error) {
    s.mu.Lock(); defer s.mu.Unlock()
    s.upserted = append(s.upserted, in)
    return api.ImageVersion{ImageRepo: in.ImageRepo, Variant: in.Variant}, s.upsertErr
}
func (s *fakeStore) DeleteImageVersionsNotIn(_ context.Context, keep [][2]string) (int64, error) {
    s.mu.Lock(); defer s.mu.Unlock()
    s.reaped = append(s.reaped, keep)
    return 0, nil
}

type fakeLister struct {
    byRepo map[string][]string
    err    error
}

func (l *fakeLister) ListTags(_ context.Context, _ string, repoPath string) ([]string, error) {
    if l.err != nil { return nil, l.err }
    return l.byRepo[repoPath], nil
}

func TestEnricher_Tick_Disabled(t *testing.T) {
    s := &fakeStore{settings: api.Settings{ImageVersionsEnabled: false}}
    e := NewEnricher(s, &fakeLister{}, time.Hour)
    e.RunTick(context.Background())
    if len(s.upserted) != 0 {
        t.Fatalf("expected no upserts when disabled, got %d", len(s.upserted))
    }
}

func TestEnricher_Tick_HappyPath(t *testing.T) {
    enabled := true
    s := &fakeStore{
        settings:   api.Settings{ImageVersionsEnabled: enabled},
        registries: []api.ImageRegistry{{Hostname: "docker.io", RateLimitPerSec: 1.0, Enabled: true}},
        refs:       []string{"nginx:1.25.3", "nginx:1.25.3-alpine"},
    }
    l := &fakeLister{byRepo: map[string][]string{
        "library/nginx": {"1.25.3", "1.27.4", "1.27.4-alpine", "1.27.5-rc1"},
    }}
    e := NewEnricher(s, l, time.Hour)
    e.RunTick(context.Background())

    if len(s.upserted) != 2 {
        t.Fatalf("expected 2 upserts (one per variant), got %d: %+v", len(s.upserted), s.upserted)
    }
    seen := map[string]string{}
    for _, u := range s.upserted {
        var lt string
        if u.LatestTag != nil {
            lt = *u.LatestTag
        }
        seen[u.Variant] = lt
    }
    if seen[""] != "1.27.4" {
        t.Errorf("variant=\"\" expected 1.27.4, got %q", seen[""])
    }
    if seen["alpine"] != "1.27.4-alpine" {
        t.Errorf("variant=alpine expected 1.27.4-alpine, got %q", seen["alpine"])
    }
    if len(s.reaped) != 1 {
        t.Fatalf("expected one reap call, got %d", len(s.reaped))
    }
}

func TestEnricher_Tick_RegistryError_DoesNotFailTick(t *testing.T) {
    enabled := true
    s := &fakeStore{
        settings:   api.Settings{ImageVersionsEnabled: enabled},
        registries: []api.ImageRegistry{{Hostname: "docker.io", RateLimitPerSec: 1.0, Enabled: true}},
        refs:       []string{"nginx:1.25.3"},
    }
    l := &fakeLister{err: errors.New("network down")}
    e := NewEnricher(s, l, time.Hour)
    e.RunTick(context.Background())

    if len(s.upserted) != 1 {
        t.Fatalf("expected 1 upsert with error info, got %d", len(s.upserted))
    }
    if s.upserted[0].LastError == nil || *s.upserted[0].LastError == "" {
        t.Fatalf("expected last_error populated")
    }
    if s.upserted[0].LatestTag != nil {
        t.Fatalf("expected latest_tag nil on error")
    }
}

func TestEnricher_Trigger_DedupesWhilePending(t *testing.T) {
    s := &fakeStore{settings: api.Settings{ImageVersionsEnabled: false}}
    e := NewEnricher(s, &fakeLister{}, time.Hour)
    if running := e.Trigger(); running {
        t.Fatalf("first trigger should not report running")
    }
    if running := e.Trigger(); !running {
        t.Fatalf("second back-to-back trigger should report running/pending")
    }
}

func TestEnricher_AnnotationShape(t *testing.T) {
    enabled := true
    latest := "1.27.4"
    ann, _ := buildAnnotation(&latest, nil)
    var m map[string]any
    if err := json.Unmarshal(ann, &m); err != nil {
        t.Fatalf("annotation must be valid JSON: %v", err)
    }
    if m["latest_available"] != "1.27.4" {
        t.Errorf("expected latest_available, got %v", m)
    }
    _ = enabled
}
```

- [ ] **Step 3: Run tests — they must fail**

```bash
go test -race -run '^TestEnricher' ./internal/imageversions/...
```
Expected: FAIL (Enricher not defined).

- [ ] **Step 4: Implement `internal/imageversions/enricher.go`**

```go
package imageversions

import (
    "context"
    "encoding/json"
    "errors"
    "log/slog"
    "sync"
    "sync/atomic"
    "time"

    "golang.org/x/time/rate"

    "github.com/sthalbert/longue-vue/internal/api"
    "github.com/sthalbert/longue-vue/internal/imageversions/registry"
)

const sourceRegistry = "registry"

// Enricher periodically queries public registries for the latest tag of
// each image used in K8s clusters.
type Enricher struct {
    store    Store
    lister   TagsLister
    interval time.Duration

    triggerCh chan struct{}
    running   atomic.Bool
}

func NewEnricher(s Store, lister TagsLister, interval time.Duration) *Enricher {
    return &Enricher{
        store:     s,
        lister:    lister,
        interval:  interval,
        triggerCh: make(chan struct{}, 1),
    }
}

// Run blocks until ctx is cancelled, executing one tick per interval and
// also responding to manual triggers.
func (e *Enricher) Run(ctx context.Context) error {
    ticker := time.NewTicker(e.interval)
    defer ticker.Stop()
    e.RunTick(ctx)
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-ticker.C:
            e.RunTick(ctx)
        case <-e.triggerCh:
            e.RunTick(ctx)
        }
    }
}

// Trigger queues an immediate tick. Returns true if a tick is already
// running or pending (and the new trigger was a no-op).
func (e *Enricher) Trigger() (alreadyRunning bool) {
    if e.running.Load() {
        return true
    }
    select {
    case e.triggerCh <- struct{}{}:
        return false
    default:
        return true
    }
}

// IsRunning reports whether a tick is currently in progress.
func (e *Enricher) IsRunning() bool {
    return e.running.Load()
}

// RunTick executes one full enrichment cycle. Exposed for tests; the long
// loop in Run() calls it.
func (e *Enricher) RunTick(ctx context.Context) {
    if !e.running.CompareAndSwap(false, true) {
        return
    }
    defer e.running.Store(false)

    settings, err := e.store.GetSettings(ctx)
    if err != nil {
        slog.Warn("imageversions: get settings failed", slog.String("err", err.Error()))
        return
    }
    if !settings.ImageVersionsEnabled {
        return
    }

    regs, err := e.store.ListImageRegistries(ctx)
    if err != nil {
        slog.Warn("imageversions: list registries failed", slog.String("err", err.Error()))
        return
    }
    enabledRegs := make([]api.ImageRegistry, 0, len(regs))
    for _, r := range regs {
        if r.Enabled {
            enabledRegs = append(enabledRegs, r)
        }
    }

    refs, err := e.store.DistinctImageRefs(ctx)
    if err != nil {
        slog.Warn("imageversions: distinct refs failed", slog.String("err", err.Error()))
        return
    }

    type variantKey struct {
        Repo    string
        Variant string
    }
    discovered := map[string]map[string]struct{}{} // repo -> variants
    repoRegistry := map[string]string{}            // repo -> hostname matched
    for _, raw := range refs {
        ref, err := ParseImageRef(raw)
        if err != nil {
            slog.Debug("imageversions: skip ref", slog.String("ref", raw), slog.String("reason", err.Error()))
            continue
        }
        // match registry against enabled allowlist
        var matched *api.ImageRegistry
        for i := range enabledRegs {
            if registry.Match(enabledRegs[i].Hostname, ref.Registry) {
                matched = &enabledRegs[i]
                break
            }
        }
        if matched == nil {
            slog.Debug("imageversions: registry not allowlisted",
                slog.String("registry", ref.Registry), slog.String("ref", raw))
            continue
        }
        pt, err := ParseTag(ref.Tag)
        if err != nil {
            slog.Debug("imageversions: skip tag", slog.String("ref", raw), slog.String("reason", err.Error()))
            continue
        }
        if _, ok := discovered[ref.ImageRepo]; !ok {
            discovered[ref.ImageRepo] = map[string]struct{}{}
        }
        discovered[ref.ImageRepo][pt.Variant] = struct{}{}
        repoRegistry[ref.ImageRepo] = ref.Registry
    }

    // Build per-registry rate limiters.
    limiters := map[string]*rate.Limiter{}
    for _, r := range enabledRegs {
        limiters[r.Hostname] = rate.NewLimiter(rate.Limit(r.RateLimitPerSec), 1)
    }
    pickLimiter := func(reg string) *rate.Limiter {
        for h, l := range limiters {
            if registry.Match(h, reg) {
                return l
            }
        }
        return nil
    }

    // Bounded parallel work: 5 workers.
    type result struct {
        upsert api.ImageVersionUpsert
    }
    sem := make(chan struct{}, 5)
    results := make(chan result)
    var wg sync.WaitGroup

    for repo, variants := range discovered {
        repo, variants := repo, variants
        wg.Add(1)
        go func() {
            defer wg.Done()
            sem <- struct{}{}
            defer func() { <-sem }()

            reg := repoRegistry[repo]
            url, repoPath, err := registry.EffectiveHost(repo)
            if err != nil {
                emitError(results, repo, variants, reg, err)
                return
            }
            if l := pickLimiter(reg); l != nil {
                if err := l.Wait(ctx); err != nil {
                    return
                }
            }
            tags, err := e.lister.ListTags(ctx, url, repoPath)
            if err != nil {
                emitError(results, repo, variants, reg, err)
                return
            }
            now := time.Now().UTC()
            for v := range variants {
                latest, _ := ComputeLatest(v, tags)
                ann, _ := buildAnnotation(strPtrIfNonEmpty(latest), nil)
                up := api.ImageVersionUpsert{
                    ImageRepo:     repo,
                    Variant:       v,
                    Registry:      reg,
                    LatestTag:     strPtrIfNonEmpty(latest),
                    Annotation:    ann,
                    Source:        sourceRegistry,
                    LastCheckedAt: now,
                }
                results <- result{upsert: up}
            }
        }()
    }

    go func() { wg.Wait(); close(results) }()

    var processed [][2]string
    for r := range results {
        if _, err := e.store.UpsertImageVersion(ctx, r.upsert); err != nil {
            slog.Warn("imageversions: upsert failed",
                slog.String("repo", r.upsert.ImageRepo),
                slog.String("err", err.Error()))
        }
        processed = append(processed, [2]string{r.upsert.ImageRepo, r.upsert.Variant})
    }

    if _, err := e.store.DeleteImageVersionsNotIn(ctx, processed); err != nil {
        slog.Warn("imageversions: reap failed", slog.String("err", err.Error()))
    }
}

func emitError(results chan<- struct{ upsert api.ImageVersionUpsert }, repo string, variants map[string]struct{}, reg string, err error) {
    now := time.Now().UTC()
    msg := err.Error()
    var classified string
    switch {
    case errors.Is(err, registry.ErrRepoNotFound):
        classified = "repo not found"
    case errors.Is(err, registry.ErrRateLimited):
        classified = "rate limited"
    default:
        classified = msg
    }
    ann, _ := buildAnnotation(nil, &classified)
    for v := range variants {
        results <- struct{ upsert api.ImageVersionUpsert }{
            upsert: api.ImageVersionUpsert{
                ImageRepo:     repo,
                Variant:       v,
                Registry:      reg,
                LatestTag:     nil,
                Annotation:    ann,
                Source:        sourceRegistry,
                LastCheckedAt: now,
                LastError:     &classified,
                LastErrorAt:   &now,
            },
        }
    }
}

func strPtrIfNonEmpty(s string) *string {
    if s == "" {
        return nil
    }
    return &s
}

// buildAnnotation produces an eol.Annotation-shaped JSONB. In V1 we only
// fill the latest_available field (and a sentinel eol_status="unknown").
// Future V3 work will populate richer fields; the schema is forward-compatible.
//
// IMPLEMENTATION NOTE: prefer using the actual `eol.Annotation` struct from
// internal/eol/types.go for type safety. Inspect the struct's fields and json
// tags before this implementation step and adapt the assignments below.
// The map[string]any version below is a working fallback if importing the
// struct creates an inconvenient cycle.
func buildAnnotation(latestAvailable *string, errMsg *string) (json.RawMessage, error) {
    obj := map[string]any{
        "eol_status": "unknown",
    }
    if latestAvailable != nil {
        obj["latest_available"] = *latestAvailable
    }
    if errMsg != nil {
        obj["error"] = *errMsg
    }
    return json.Marshal(obj)
}
```

- [ ] **Step 5: Run tests — they must pass**

```bash
go test -race -run '^TestEnricher' ./internal/imageversions/...
```
Expected: PASS for all five tests.

- [ ] **Step 6: Commit**

```bash
git add internal/imageversions/types.go internal/imageversions/enricher.go internal/imageversions/enricher_test.go go.mod go.sum
git commit -m "feat(imageversions): periodic enricher with trigger and reaping"
```

---

## Task 11 — Wire enricher into `cmd/longue-vue/main.go`

**Files:**
- Modify: `cmd/longue-vue/main.go` (add `maybeStartImageVersionsEnricher` near `maybeStartEOLEnricher`)

- [ ] **Step 1: Locate `maybeStartEOLEnricher`**

```bash
grep -n 'maybeStartEOLEnricher' cmd/longue-vue/main.go
```
Expected: at least one definition near line 1063 and one call site near `func main`.

- [ ] **Step 2: Add the new startup function below `maybeStartEOLEnricher`**

```go
func maybeStartImageVersionsEnricher(ctx context.Context, s api.Store, wg *sync.WaitGroup) (*imageversions.Enricher, error) {
    if envVal := os.Getenv("LONGUE_VUE_IMAGE_VERSIONS_ENABLED"); envVal != "" {
        enabled, err := strconv.ParseBool(envVal)
        if err != nil {
            return nil, fmt.Errorf("LONGUE_VUE_IMAGE_VERSIONS_ENABLED: %w", err)
        }
        if _, err := s.UpdateSettings(ctx, api.SettingsPatch{ImageVersionsEnabled: &enabled}); err != nil {
            return nil, fmt.Errorf("seed image_versions_enabled: %w", err)
        }
    }
    interval, err := parseDurationEnv("LONGUE_VUE_IMAGE_VERSIONS_INTERVAL", 24*time.Hour)
    if err != nil {
        return nil, err
    }
    client := registry.NewClient()
    enricher := imageversions.NewEnricher(s, client, interval)

    wg.Add(1)
    go func() {
        defer wg.Done()
        if err := enricher.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
            slog.Error("imageversions enricher exited with error", slog.String("error", err.Error()))
        }
    }()
    slog.Info("imageversions enricher goroutine started",
        slog.String("interval", interval.String()))
    return enricher, nil
}
```

Add the matching imports at the top of `main.go`:

```go
"github.com/sthalbert/longue-vue/internal/imageversions"
"github.com/sthalbert/longue-vue/internal/imageversions/registry"
```

- [ ] **Step 3: Call it from `main`**

In the same place where `maybeStartEOLEnricher` is invoked, add:

```go
imgVersionsEnricher, err := maybeStartImageVersionsEnricher(ctx, pg, &wg)
if err != nil {
    return fmt.Errorf("image versions enricher: %w", err)
}
_ = imgVersionsEnricher // captured later for the refresh handler in Task 12
```

**Note:** keep `imgVersionsEnricher` in scope alongside the other long-lived components (e.g., `cloudAuth`, `auditWrap`); we'll wire it into the refresh handler in Task 12. If the existing function is monolithic, declare the variable at the same scope as `pg` so it's reachable when registering routes.

- [ ] **Step 4: Verify the binary still builds**

```bash
make build-noui
```
Expected: success, no compile errors.

- [ ] **Step 5: Commit**

```bash
git add cmd/longue-vue/main.go
git commit -m "feat(main): start image versions enricher goroutine at boot"
```

---

## Task 12 — API: list, detail, and refresh handlers

**Files:**
- Create: `internal/api/image_version_handlers.go`
- Create: `internal/api/image_version_handlers_test.go`

- [ ] **Step 1: Write the failing tests `internal/api/image_version_handlers_test.go`**

```go
package api

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestHandleListImageVersions_Empty(t *testing.T) {
    s := newFakeStoreForTest(t) // existing pattern in this package; see other *_test.go
    h := HandleListImageVersions(s)
    req := httptest.NewRequest(http.MethodGet, "/v1/image-versions", nil)
    w := httptest.NewRecorder()
    h.ServeHTTP(w, req)
    if w.Code != http.StatusOK {
        t.Fatalf("status: want 200, got %d", w.Code)
    }
    var body map[string]any
    if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
        t.Fatalf("decode: %v", err)
    }
    if _, ok := body["items"]; !ok {
        t.Fatalf("missing items field in %v", body)
    }
}

func TestHandleRefresh_FeatureDisabled(t *testing.T) {
    s := newFakeStoreForTest(t)
    // assume seed default false
    enr := &fakeRefresher{enabled: false}
    h := HandleRefreshImageVersions(s, enr)
    req := httptest.NewRequest(http.MethodPost, "/v1/image-versions/refresh", nil)
    w := httptest.NewRecorder()
    h.ServeHTTP(w, req)
    if w.Code != http.StatusConflict {
        t.Fatalf("status: want 409, got %d", w.Code)
    }
}

func TestHandleRefresh_Triggered(t *testing.T) {
    s := newFakeStoreForTest(t)
    enr := &fakeRefresher{enabled: true}
    enableSettings(t, s, "image_versions_enabled", true)

    h := HandleRefreshImageVersions(s, enr)
    req := httptest.NewRequest(http.MethodPost, "/v1/image-versions/refresh", nil)
    w := httptest.NewRecorder()
    h.ServeHTTP(w, req)
    if w.Code != http.StatusAccepted {
        t.Fatalf("status: want 202, got %d", w.Code)
    }
    if !enr.triggered {
        t.Fatalf("expected enricher.Trigger to be called")
    }
}

type fakeRefresher struct {
    enabled    bool
    triggered  bool
    isRunning  bool
}

func (f *fakeRefresher) Trigger() bool { f.triggered = true; return f.isRunning }
func (f *fakeRefresher) IsRunning() bool { return f.isRunning }
```

If `newFakeStoreForTest` and `enableSettings` helpers don't exist in this package, copy the pattern from the nearest existing `*_handlers_test.go` (search for `httptest.NewRecorder` examples).

- [ ] **Step 2: Run tests — they must fail**

```bash
go test -race -run '^TestHandleListImageVersions|^TestHandleRefresh' ./internal/api/
```
Expected: FAIL.

- [ ] **Step 3: Implement `internal/api/image_version_handlers.go`**

```go
package api

import (
    "encoding/json"
    "net/http"
    "net/url"
    "strconv"
    "time"
)

// EnricherTrigger abstracts the methods used by HandleRefresh.
// Defined here so handlers don't pull in the imageversions package types directly.
type EnricherTrigger interface {
    Trigger() bool
    IsRunning() bool
}

func HandleListImageVersions(s Store) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        q := r.URL.Query()
        params := ImageVersionListParams{
            Limit:         parseIntDefault(q.Get("limit"), 50),
            Cursor:        q.Get("cursor"),
            Registry:      q.Get("registry"),
            ImageRepoLike: q.Get("image_repo"),
            Variant:       q.Get("variant"),
        }
        if v := q.Get("has_error"); v != "" {
            b, err := strconv.ParseBool(v)
            if err != nil {
                writeProblem(w, http.StatusBadRequest, "Bad Request", "has_error must be a boolean")
                return
            }
            params.HasError = &b
        }
        if v := q.Get("last_checked_before"); v != "" {
            t, err := time.Parse(time.RFC3339, v)
            if err != nil {
                writeProblem(w, http.StatusBadRequest, "Bad Request", "last_checked_before must be RFC 3339")
                return
            }
            params.LastCheckedBefore = &t
        }

        items, next, err := s.ListImageVersionsByRepo(r.Context(), params)
        if err != nil {
            writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
            return
        }
        writeJSON(w, http.StatusOK, map[string]any{
            "items":       items,
            "next_cursor": next,
        })
    })
}

func HandleGetImageVersion(s Store) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        repo := r.PathValue("image_repo")
        if decoded, err := url.PathUnescape(repo); err == nil {
            repo = decoded
        }
        rows, err := s.GetImageVersionsByRepo(r.Context(), repo)
        if err != nil {
            writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
            return
        }
        if len(rows) == 0 {
            writeProblem(w, http.StatusNotFound, "Not Found", "image_repo not found")
            return
        }
        view := ImageVersionRepoView{
            ImageRepo: rows[0].ImageRepo,
            Registry:  rows[0].Registry,
            Variants:  rows,
        }
        writeJSON(w, http.StatusOK, view)
    })
}

func HandleRefreshImageVersions(s Store, enr EnricherTrigger) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        settings, err := s.GetSettings(r.Context())
        if err != nil {
            writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
            return
        }
        if !settings.ImageVersionsEnabled {
            writeProblem(w, http.StatusConflict, "Conflict", "image_versions_enabled is false")
            return
        }
        running := enr.Trigger()
        writeJSON(w, http.StatusAccepted, map[string]any{
            "queued":          !running,
            "already_running": running,
        })
    })
}

func parseIntDefault(s string, def int) int {
    if s == "" { return def }
    n, err := strconv.Atoi(s)
    if err != nil || n <= 0 { return def }
    return n
}
```

If `writeJSON` does not exist in this package, search adjacent files; cloud_account_handlers.go has `writeProblem`, the JSON helper is likely in the same file or in a shared helper file.

- [ ] **Step 4: Run tests — they must pass**

```bash
go test -race -run '^TestHandleListImageVersions|^TestHandleRefresh' ./internal/api/
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/image_version_handlers.go internal/api/image_version_handlers_test.go
git commit -m "feat(api): GET/POST handlers for image versions list/detail/refresh"
```

---

## Task 13 — API: admin CRUD on `image_versions_registries`

**Files:**
- Create: `internal/api/image_registry_handlers.go`
- Create: `internal/api/image_registry_handlers_test.go`

- [ ] **Step 1: Write the failing tests `internal/api/image_registry_handlers_test.go`**

```go
package api

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestHandleListImageRegistries(t *testing.T) {
    s := newFakeStoreForTest(t)
    seedDefaultRegistries(t, s) // helper: insert the 7 defaults

    h := HandleListImageRegistries(s)
    req := httptest.NewRequest(http.MethodGet, "/v1/admin/image-versions/registries", nil)
    w := httptest.NewRecorder()
    h.ServeHTTP(w, req)
    if w.Code != http.StatusOK {
        t.Fatalf("status: want 200, got %d", w.Code)
    }
    var body map[string]any
    if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil { t.Fatalf("decode: %v", err) }
    items, _ := body["items"].([]any)
    if len(items) != 7 {
        t.Fatalf("expected 7 defaults, got %d", len(items))
    }
}

func TestHandleCreateImageRegistry(t *testing.T) {
    s := newFakeStoreForTest(t)
    h := HandleCreateImageRegistry(s)
    body, _ := json.Marshal(map[string]any{
        "hostname":            "mirror.example.com",
        "rate_limit_per_sec":  2.5,
    })
    req := httptest.NewRequest(http.MethodPost, "/v1/admin/image-versions/registries", bytes.NewReader(body))
    w := httptest.NewRecorder()
    h.ServeHTTP(w, req)
    if w.Code != http.StatusCreated {
        t.Fatalf("status: want 201, got %d", w.Code)
    }
}

func TestHandleCreateImageRegistry_BadInput(t *testing.T) {
    s := newFakeStoreForTest(t)
    h := HandleCreateImageRegistry(s)
    body, _ := json.Marshal(map[string]any{"hostname": ""})
    req := httptest.NewRequest(http.MethodPost, "/v1/admin/image-versions/registries", bytes.NewReader(body))
    w := httptest.NewRecorder()
    h.ServeHTTP(w, req)
    if w.Code != http.StatusBadRequest {
        t.Fatalf("status: want 400, got %d", w.Code)
    }
}
```

`seedDefaultRegistries(t, s)` helper: inserts the seven defaults via `s.CreateImageRegistry`. Inline it in the test file.

- [ ] **Step 2: Run tests — they must fail**

```bash
go test -race -run '^TestHandleListImageRegistries|^TestHandleCreateImageRegistry' ./internal/api/
```
Expected: FAIL.

- [ ] **Step 3: Implement `internal/api/image_registry_handlers.go`**

```go
package api

import (
    "encoding/json"
    "errors"
    "net/http"
)

func HandleListImageRegistries(s Store) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        items, err := s.ListImageRegistries(r.Context())
        if err != nil {
            writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
            return
        }
        writeJSON(w, http.StatusOK, map[string]any{"items": items})
    })
}

func HandleCreateImageRegistry(s Store) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var in ImageRegistryUpsert
        if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
            writeProblem(w, http.StatusBadRequest, "Bad Request", err.Error())
            return
        }
        if in.Hostname == "" {
            writeProblem(w, http.StatusBadRequest, "Bad Request", "hostname is required")
            return
        }
        if in.RateLimitPerSec <= 0 {
            writeProblem(w, http.StatusBadRequest, "Bad Request", "rate_limit_per_sec must be > 0")
            return
        }
        out, err := s.CreateImageRegistry(r.Context(), in)
        switch {
        case errors.Is(err, ErrConflict):
            writeProblem(w, http.StatusConflict, "Conflict", "hostname already exists")
            return
        case err != nil:
            writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
            return
        }
        writeJSON(w, http.StatusCreated, out)
    })
}

func HandleUpdateImageRegistry(s Store) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        host := r.PathValue("hostname")
        if host == "" {
            writeProblem(w, http.StatusBadRequest, "Bad Request", "hostname is required")
            return
        }
        var p ImageRegistryPatch
        if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
            writeProblem(w, http.StatusBadRequest, "Bad Request", err.Error())
            return
        }
        if p.RateLimitPerSec != nil && *p.RateLimitPerSec <= 0 {
            writeProblem(w, http.StatusBadRequest, "Bad Request", "rate_limit_per_sec must be > 0")
            return
        }
        out, err := s.UpdateImageRegistry(r.Context(), host, p)
        switch {
        case errors.Is(err, ErrNotFound):
            writeProblem(w, http.StatusNotFound, "Not Found", "registry not found")
            return
        case err != nil:
            writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
            return
        }
        writeJSON(w, http.StatusOK, out)
    })
}

func HandleDeleteImageRegistry(s Store) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        host := r.PathValue("hostname")
        if host == "" {
            writeProblem(w, http.StatusBadRequest, "Bad Request", "hostname is required")
            return
        }
        err := s.DeleteImageRegistry(r.Context(), host)
        switch {
        case errors.Is(err, ErrNotFound):
            writeProblem(w, http.StatusNotFound, "Not Found", "registry not found")
            return
        case err != nil:
            writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
            return
        }
        w.WriteHeader(http.StatusNoContent)
    })
}
```

- [ ] **Step 4: Run tests — they must pass**

```bash
go test -race -run '^TestHandleListImageRegistries|^TestHandleCreateImageRegistry' ./internal/api/
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/image_registry_handlers.go internal/api/image_registry_handlers_test.go
git commit -m "feat(api): admin CRUD on image_versions_registries"
```

---

## Task 14 — Workload/pod response enrichment with `containers_versions`

**Files:**
- Modify: `internal/api/workload_handlers.go` (or wherever `GET /v1/workloads/{id}` is)
- Modify: `internal/api/pod_handlers.go` (similarly)
- Modify: `internal/api/store.go` (add types)

- [ ] **Step 1: Add the response field types to `internal/api/store.go`**

```go
type ContainerVersionInfo struct {
    LatestTag     string    `json:"latest_tag"`
    IsBehind      bool      `json:"is_behind"`
    LastCheckedAt time.Time `json:"last_checked_at"`
}

// ContainersVersions maps container.name -> ContainerVersionInfo. Keys are
// absent when the image is not enriched (non-parseable tag, registry
// outside allowlist, or not yet processed).
type ContainersVersions map[string]ContainerVersionInfo
```

Add to existing `Workload` and `Pod` response structs (search for them in `internal/api/store.go` or related files):

```go
type Workload struct {
    // ...existing fields...
    ContainersVersions ContainersVersions `json:"containers_versions,omitempty"`
}

type Pod struct {
    // ...existing fields...
    ContainersVersions ContainersVersions `json:"containers_versions,omitempty"`
}
```

- [ ] **Step 2: Add a helper in `internal/api` to compute the enrichment**

Create `internal/api/containers_versions_enrich.go`:

```go
package api

import (
    "context"

    "github.com/sthalbert/longue-vue/internal/imageversions"
)

// EnrichContainersVersions joins each container's image against image_versions
// and returns the populated map. Missing entries (non-parseable, not allowlisted,
// not yet processed) are simply absent.
func EnrichContainersVersions(ctx context.Context, s Store, containers []map[string]any) ContainersVersions {
    out := ContainersVersions{}
    for _, c := range containers {
        name, _ := c["name"].(string)
        img, _ := c["image"].(string)
        if name == "" || img == "" {
            continue
        }
        ref, err := imageversions.ParseImageRef(img)
        if err != nil { continue }
        cur, err := imageversions.ParseTag(ref.Tag)
        if err != nil { continue }

        rows, err := s.GetImageVersionsByRepo(ctx, ref.ImageRepo)
        if err != nil { continue }
        for _, row := range rows {
            if row.Variant != cur.Variant { continue }
            if row.LatestTag == nil { continue }
            latest, err := imageversions.ParseTag(*row.LatestTag)
            if err != nil { continue }
            out[name] = ContainerVersionInfo{
                LatestTag:     *row.LatestTag,
                IsBehind:      latest.Version.GT(cur.Version),
                LastCheckedAt: row.LastCheckedAt,
            }
            break
        }
    }
    return out
}
```

- [ ] **Step 3: Call `EnrichContainersVersions` in workload/pod GET handlers**

Locate `HandleGetWorkload` (or equivalent) in `internal/api/workload_handlers.go`. After the workload is fetched and just before it's marshalled, add:

```go
wl.ContainersVersions = EnrichContainersVersions(r.Context(), s, wl.Containers)
```

(Replace `wl.Containers` with the actual field path; `containers` is JSONB and the Go side stores it as `[]map[string]any` per existing codegen.)

Do the same in the pods handler. For LIST endpoints (`HandleListWorkloads`, `HandleListPods`) it's a judgment call: the spec says enrichment "always present" but joining for every workload in a list might be expensive. **For V1, perform the join on detail (GET by id) only.** If list views need it later, batch via `IN (...)` query.

- [ ] **Step 4: Add a regression test**

In `internal/api/workload_handlers_test.go` (or similar), add a test that:
1. Inserts a workload with `containers=[{"name":"web","image":"nginx:1.25.3"}]`.
2. Inserts a row in `image_versions` with `image_repo=docker.io/library/nginx, variant="", latest_tag="1.27.4"`.
3. Calls `GET /v1/workloads/{id}` and asserts `containers_versions.web.latest_tag == "1.27.4"` and `is_behind == true`.

Code skeleton (adapt to existing helpers):

```go
func TestGetWorkload_ContainersVersions(t *testing.T) {
    s := newTestPGAsStore(t) // existing pattern
    ctx := context.Background()

    // seed workload + image_versions
    cluster := mustSeedCluster(t, s)
    workloadID := mustSeedWorkload(t, s, cluster.ID, []map[string]any{
        {"name": "web", "image": "nginx:1.25.3"},
    })
    latest := "1.27.4"
    _, err := s.UpsertImageVersion(ctx, ImageVersionUpsert{
        ImageRepo:     "docker.io/library/nginx",
        Variant:       "",
        Registry:      "docker.io",
        LatestTag:     &latest,
        Annotation:    json.RawMessage(`{"latest_available":"1.27.4"}`),
        Source:        "registry",
        LastCheckedAt: time.Now().UTC(),
    })
    if err != nil { t.Fatalf("upsert: %v", err) }

    h := HandleGetWorkload(s)
    req := httptest.NewRequest(http.MethodGet, "/v1/workloads/"+workloadID, nil)
    req.SetPathValue("id", workloadID)
    w := httptest.NewRecorder()
    h.ServeHTTP(w, req)
    if w.Code != http.StatusOK {
        t.Fatalf("status: %d, body: %s", w.Code, w.Body.String())
    }

    var resp Workload
    if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil { t.Fatalf("decode: %v", err) }
    info, ok := resp.ContainersVersions["web"]
    if !ok {
        t.Fatalf("expected web container enriched, got: %+v", resp.ContainersVersions)
    }
    if info.LatestTag != "1.27.4" || !info.IsBehind {
        t.Errorf("unexpected enrichment: %+v", info)
    }
}
```

- [ ] **Step 5: Run the test**

```bash
go test -race -run '^TestGetWorkload_ContainersVersions$' ./internal/api/
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/store.go internal/api/containers_versions_enrich.go internal/api/workload_handlers.go internal/api/pod_handlers.go internal/api/workload_handlers_test.go
git commit -m "feat(api): enrich workload/pod GETs with containers_versions"
```

---

## Task 15 — Mount routes + audit middleware

**Files:**
- Modify: `cmd/longue-vue/main.go` (route registration block, currently around lines 419–451)

- [ ] **Step 1: Find the existing admin-routes block and add new mounts**

Just below the `cloud-accounts` block (or wherever admin routes are grouped):

```go
// ---- image versions (read endpoints, any authenticated role) ----
mux.Handle("GET /v1/image-versions",
    cloudAuth(auditWrap(api.HandleListImageVersions(pg))))
mux.Handle("GET /v1/image-versions/{image_repo}",
    cloudAuth(auditWrap(api.HandleGetImageVersion(pg))))
mux.Handle("POST /v1/image-versions/refresh",
    requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(
        api.HandleRefreshImageVersions(pg, imgVersionsEnricher)))))

// ---- image versions registries CRUD (admin only) ----
mux.Handle("GET /v1/admin/image-versions/registries",
    requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleListImageRegistries(pg)))))
mux.Handle("POST /v1/admin/image-versions/registries",
    requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleCreateImageRegistry(pg)))))
mux.Handle("PATCH /v1/admin/image-versions/registries/{hostname}",
    requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleUpdateImageRegistry(pg)))))
mux.Handle("DELETE /v1/admin/image-versions/registries/{hostname}",
    requireScope(auth.ScopeAdmin)(cloudAuth(auditWrap(api.HandleDeleteImageRegistry(pg)))))
```

- [ ] **Step 2: Build, smoke-test the binary**

```bash
make build-noui
./longue-vue --help 2>&1 | head
```
Expected: builds, `--help` output unchanged.

- [ ] **Step 3: Manual integration check (optional but recommended)**

If you have a running PG, point `LONGUE_VUE_DSN` and run `./longue-vue` then:

```bash
curl -s -H 'Authorization: Bearer longue_vue_pat_…' http://localhost:8080/v1/image-versions | jq
```
Expected: `{"items": [], "next_cursor": ""}` (no enricher data yet — we'll exercise the loop in Task 16).

- [ ] **Step 4: Commit**

```bash
git add cmd/longue-vue/main.go
git commit -m "feat(api): mount image-versions and registries routes"
```

---

## Task 16 — Enricher integration test against real Postgres

**Files:**
- Create: `internal/imageversions/integration_test.go`

- [ ] **Step 1: Write the integration test**

```go
//go:build integration

package imageversions_test

import (
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/sthalbert/longue-vue/internal/api"
    "github.com/sthalbert/longue-vue/internal/imageversions"
    "github.com/sthalbert/longue-vue/internal/imageversions/registry"
    "github.com/sthalbert/longue-vue/internal/store"
)

func TestEnricher_Integration_FullCycle(t *testing.T) {
    pg := store.NewTestPG(t) // exported helper; if not, copy from store/pg_test.go's newTestPG
    ctx := context.Background()

    // Seed: enable the feature; insert a workload with two nginx tags.
    on := true
    if _, err := pg.UpdateSettings(ctx, api.SettingsPatch{ImageVersionsEnabled: &on}); err != nil {
        t.Fatalf("settings: %v", err)
    }
    cluster := store.MustSeedClusterForTest(t, pg) // helper assumed
    _ = store.MustSeedWorkloadForTest(t, pg, cluster.ID, []map[string]any{
        {"name": "web", "image": "nginx:1.25.3"},
        {"name": "edge", "image": "nginx:1.25.3-alpine"},
    })

    // Stand up a fake registry that responds at .../library/nginx/tags/list.
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(map[string]any{
            "name": "library/nginx",
            "tags": []string{"1.25.3", "1.27.4", "1.27.4-alpine", "latest"},
        })
    }))
    t.Cleanup(srv.Close)

    // Inject the fake URL into the Docker Hub mapping by patching EffectiveHost
    // at the imageversions/registry layer. Easiest path: have the test register
    // a custom hostname that points at the fake server, and seed the workload's
    // image with that hostname instead.
    //
    // Here we use a registry hostname trick: create a fresh registry row whose
    // hostname is the host:port of srv, with a trivially-true rate limit, then
    // seed the workload's image string as "<host>:<port>/library/nginx:1.25.3".
    // Repoint the workload + registry accordingly to keep the test hermetic.

    t.Skip("Hermetic full-cycle test requires injecting registry URL; tracked as followup. " +
        "Use unit tests in enricher_test.go for now and run live smoke from the e2e gated suite.")
}
```

The full-cycle integration test is non-trivial because the OCI client constructs URLs from `EffectiveHost`. For V1, **rely on**: (a) the unit tests in `enricher_test.go` (Task 10) for the loop logic, (b) a live smoke test (Task 22) for end-to-end against real registries. The skip-with-comment above documents the gap explicitly.

- [ ] **Step 2: Run with the integration build tag**

```bash
go test -race -tags=integration ./internal/imageversions/...
```
Expected: PASS (the test is skipped but the build tag is exercised; no compile errors).

- [ ] **Step 3: Commit**

```bash
git add internal/imageversions/integration_test.go
git commit -m "test(imageversions): integration scaffold and skip-with-rationale"
```

---

## Task 17 — OpenAPI schemas and paths

**Files:**
- Modify: `api/openapi/openapi.yaml`

- [ ] **Step 1: Add new schemas under `components.schemas`**

Find the `components.schemas` section (around line 2466). Add:

```yaml
    ImageRegistry:
      type: object
      required: [hostname, rate_limit_per_sec, enabled, created_at, updated_at]
      properties:
        hostname:           { type: string, description: "Exact hostname or '*<suffix>' wildcard" }
        rate_limit_per_sec: { type: number, format: float, minimum: 0.01 }
        enabled:            { type: boolean }
        notes:              { type: string, nullable: true }
        created_at:         { type: string, format: date-time }
        updated_at:         { type: string, format: date-time }

    ImageRegistryUpsert:
      type: object
      required: [hostname, rate_limit_per_sec]
      properties:
        hostname:           { type: string }
        rate_limit_per_sec: { type: number, format: float, minimum: 0.01 }
        enabled:            { type: boolean }
        notes:              { type: string }

    ImageRegistryPatch:
      type: object
      properties:
        rate_limit_per_sec: { type: number, format: float, minimum: 0.01 }
        enabled:            { type: boolean }
        notes:              { type: string, nullable: true }

    ImageVersionVariant:
      type: object
      required: [variant, source, last_checked_at]
      properties:
        variant:         { type: string, description: "Empty string for pure semver" }
        latest_tag:      { type: string, nullable: true }
        annotation:      { type: object, additionalProperties: true }
        source:           { type: string, enum: [registry] }
        last_checked_at:  { type: string, format: date-time }
        last_error:       { type: string, nullable: true }
        last_error_at:    { type: string, format: date-time, nullable: true }

    ImageVersion:
      type: object
      required: [image_repo, registry, variants]
      properties:
        image_repo: { type: string }
        registry:    { type: string }
        variants:
          type: array
          items: { $ref: '#/components/schemas/ImageVersionVariant' }

    ImageVersionList:
      type: object
      required: [items]
      properties:
        items:
          type: array
          items: { $ref: '#/components/schemas/ImageVersion' }
        next_cursor: { type: string }

    ImageVersionRefreshResponse:
      type: object
      required: [queued, already_running]
      properties:
        queued:          { type: boolean }
        already_running: { type: boolean }

    ContainerVersionInfo:
      type: object
      required: [latest_tag, is_behind, last_checked_at]
      properties:
        latest_tag:      { type: string }
        is_behind:       { type: boolean }
        last_checked_at: { type: string, format: date-time }
```

- [ ] **Step 2: Add `containers_versions` to `WorkloadMutable` and `PodMutable`**

In each schema (around lines 3244 and 3113), add a property:

```yaml
        containers_versions:
          type: object
          description: Map of container name to its latest-version info. Absent keys mean the image is not yet enriched (non-parseable tag, registry not in allowlist, or not yet processed).
          additionalProperties: { $ref: '#/components/schemas/ContainerVersionInfo' }
```

- [ ] **Step 3: Add new paths under `paths`**

Append to the `paths` section:

```yaml
  /v1/image-versions:
    get:
      summary: List enriched container images with their latest tags
      tags: [image-versions]
      parameters:
        - { name: limit, in: query, schema: { type: integer, default: 50, maximum: 200 } }
        - { name: cursor, in: query, schema: { type: string } }
        - { name: registry, in: query, schema: { type: string } }
        - { name: image_repo, in: query, schema: { type: string } }
        - { name: variant, in: query, schema: { type: string } }
        - { name: has_error, in: query, schema: { type: boolean } }
        - { name: last_checked_before, in: query, schema: { type: string, format: date-time } }
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema: { $ref: '#/components/schemas/ImageVersionList' }
      security: [{ BearerAuth: [] }, { CookieAuth: [] }]

  /v1/image-versions/{image_repo}:
    get:
      summary: Get one image's enrichment (all variants)
      tags: [image-versions]
      parameters:
        - { name: image_repo, in: path, required: true, schema: { type: string } }
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema: { $ref: '#/components/schemas/ImageVersion' }
        '404': { $ref: '#/components/responses/ProblemNotFound' }
      security: [{ BearerAuth: [] }, { CookieAuth: [] }]

  /v1/image-versions/refresh:
    post:
      summary: Trigger an immediate enrichment cycle (admin only)
      tags: [image-versions, admin]
      responses:
        '202':
          description: Accepted
          content:
            application/json:
              schema: { $ref: '#/components/schemas/ImageVersionRefreshResponse' }
        '409': { $ref: '#/components/responses/ProblemConflict' }
      security: [{ BearerAuth: [admin] }, { CookieAuth: [admin] }]

  /v1/admin/image-versions/registries:
    get:
      summary: List the supported registries allowlist
      tags: [admin, image-versions]
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema:
                type: object
                required: [items]
                properties:
                  items:
                    type: array
                    items: { $ref: '#/components/schemas/ImageRegistry' }
      security: [{ BearerAuth: [admin] }, { CookieAuth: [admin] }]
    post:
      summary: Add a registry to the allowlist
      tags: [admin, image-versions]
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/ImageRegistryUpsert' }
      responses:
        '201':
          description: Created
          content:
            application/json:
              schema: { $ref: '#/components/schemas/ImageRegistry' }
        '400': { $ref: '#/components/responses/ProblemBadRequest' }
        '409': { $ref: '#/components/responses/ProblemConflict' }
      security: [{ BearerAuth: [admin] }, { CookieAuth: [admin] }]

  /v1/admin/image-versions/registries/{hostname}:
    patch:
      summary: Update a registry's rate limit, enabled flag, or notes
      tags: [admin, image-versions]
      parameters:
        - { name: hostname, in: path, required: true, schema: { type: string } }
      requestBody:
        required: true
        content:
          application/json:
            schema: { $ref: '#/components/schemas/ImageRegistryPatch' }
      responses:
        '200':
          description: OK
          content:
            application/json:
              schema: { $ref: '#/components/schemas/ImageRegistry' }
        '404': { $ref: '#/components/responses/ProblemNotFound' }
      security: [{ BearerAuth: [admin] }, { CookieAuth: [admin] }]
    delete:
      summary: Remove a registry from the allowlist
      tags: [admin, image-versions]
      parameters:
        - { name: hostname, in: path, required: true, schema: { type: string } }
      responses:
        '204': { description: No Content }
        '404': { $ref: '#/components/responses/ProblemNotFound' }
      security: [{ BearerAuth: [admin] }, { CookieAuth: [admin] }]
```

If the response refs `ProblemBadRequest`, `ProblemConflict`, `ProblemNotFound` don't exist yet, find similar existing endpoints and copy their inline error-response shape.

- [ ] **Step 4: Run the OpenAPI drift check**

```bash
make check
```
Expected: PASS. If `make check` doesn't include OpenAPI drift, run the codegen step explicitly (look for a target like `make openapi` or `make codegen`).

- [ ] **Step 5: Commit**

```bash
git add api/openapi/openapi.yaml
git commit -m "docs(openapi): add image-versions and registries endpoints"
```

---

## Task 18 — UI: API client extensions and types

**Files:**
- Modify: `ui/src/api.ts`

- [ ] **Step 1: Add types and functions**

```ts
// ---- image versions ----
export type ContainerVersionInfo = {
  latest_tag: string
  is_behind: boolean
  last_checked_at: string
}

export type ImageVersionVariant = {
  variant: string
  latest_tag: string | null
  annotation: Record<string, unknown>
  source: 'registry'
  last_checked_at: string
  last_error: string | null
  last_error_at: string | null
}

export type ImageVersion = {
  image_repo: string
  registry: string
  variants: ImageVersionVariant[]
}

export type ImageVersionList = {
  items: ImageVersion[]
  next_cursor?: string
}

export async function listImageVersions(opts?: {
  limit?: number; cursor?: string; registry?: string; image_repo?: string;
  variant?: string; has_error?: boolean
}): Promise<ImageVersionList> {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(opts ?? {})) {
    if (v !== undefined && v !== null) params.set(k, String(v))
  }
  const r = await fetch(`/v1/image-versions?${params.toString()}`, { credentials: 'include' })
  if (!r.ok) throw new Error(`listImageVersions: ${r.status}`)
  return r.json()
}

export async function getImageVersion(imageRepo: string): Promise<ImageVersion> {
  const r = await fetch(`/v1/image-versions/${encodeURIComponent(imageRepo)}`, { credentials: 'include' })
  if (!r.ok) throw new Error(`getImageVersion: ${r.status}`)
  return r.json()
}

export async function refreshImageVersions(): Promise<{ queued: boolean; already_running: boolean }> {
  const r = await fetch(`/v1/image-versions/refresh`, { method: 'POST', credentials: 'include' })
  if (!r.ok) throw new Error(`refreshImageVersions: ${r.status}`)
  return r.json()
}

// ---- image registries (admin) ----
export type ImageRegistry = {
  hostname: string
  rate_limit_per_sec: number
  enabled: boolean
  notes: string | null
  created_at: string
  updated_at: string
}

export async function listImageRegistries(): Promise<{ items: ImageRegistry[] }> {
  const r = await fetch(`/v1/admin/image-versions/registries`, { credentials: 'include' })
  if (!r.ok) throw new Error(`listImageRegistries: ${r.status}`)
  return r.json()
}
export async function createImageRegistry(body: {
  hostname: string; rate_limit_per_sec: number; enabled?: boolean; notes?: string
}): Promise<ImageRegistry> {
  const r = await fetch(`/v1/admin/image-versions/registries`, {
    method: 'POST', credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!r.ok) throw new Error(`createImageRegistry: ${r.status}`)
  return r.json()
}
export async function updateImageRegistry(hostname: string, patch: {
  rate_limit_per_sec?: number; enabled?: boolean; notes?: string | null
}): Promise<ImageRegistry> {
  const r = await fetch(`/v1/admin/image-versions/registries/${encodeURIComponent(hostname)}`, {
    method: 'PATCH', credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })
  if (!r.ok) throw new Error(`updateImageRegistry: ${r.status}`)
  return r.json()
}
export async function deleteImageRegistry(hostname: string): Promise<void> {
  const r = await fetch(`/v1/admin/image-versions/registries/${encodeURIComponent(hostname)}`, {
    method: 'DELETE', credentials: 'include',
  })
  if (!r.ok) throw new Error(`deleteImageRegistry: ${r.status}`)
}
```

- [ ] **Step 2: Type-check**

```bash
make ui-check
```
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add ui/src/api.ts
git commit -m "feat(ui/api): add image-versions and registries client functions"
```

---

## Task 19 — UI: `ContainerVersionBadge` component

**Files:**
- Create: `ui/src/components/ContainerVersionBadge.tsx`

- [ ] **Step 1: Implement the component**

```tsx
import React from 'react'
import type { ContainerVersionInfo } from '../api'

type Props = {
  /** ContainerVersionInfo for this container, or undefined if unknown. */
  info?: ContainerVersionInfo
  /** Last error string from the registry call, if any. */
  lastError?: string | null
}

function relTime(iso?: string): string {
  if (!iso) return ''
  const t = new Date(iso).getTime()
  const diffH = Math.round((Date.now() - t) / 3_600_000)
  if (diffH < 1) return 'less than 1h ago'
  if (diffH < 24) return `${diffH}h ago`
  return `${Math.round(diffH / 24)}d ago`
}

export function ContainerVersionBadge({ info, lastError }: Props) {
  if (lastError) {
    return (
      <span className="badge badge-error" title={`Error: ${lastError}`}>
        ⛔ error
      </span>
    )
  }
  if (!info) {
    return (
      <span className="badge badge-unknown" title="Latest version unknown for this image">
        ⚠ unknown
      </span>
    )
  }
  if (info.is_behind) {
    return (
      <span
        className="badge badge-behind"
        title={`Latest available: ${info.latest_tag} (checked ${relTime(info.last_checked_at)})`}
      >
        ↑ behind
      </span>
    )
  }
  return (
    <span
      className="badge badge-ok"
      title={`Up to date with ${info.latest_tag} (checked ${relTime(info.last_checked_at)})`}
    >
      ✓ up-to-date
    </span>
  )
}
```

- [ ] **Step 2: Type-check + visual smoke**

```bash
make ui-check && make ui-dev
```
Then load the dev server in a browser. (No regression yet — the badge isn't wired into a page until Task 20.)

- [ ] **Step 3: Commit**

```bash
git add ui/src/components/ContainerVersionBadge.tsx
git commit -m "feat(ui): ContainerVersionBadge component"
```

---

## Task 20 — UI: workload and pod page integration

**Files:**
- Modify: existing workload detail page (e.g., `ui/src/pages/WorkloadDetail.tsx`)
- Modify: existing pod detail page (e.g., `ui/src/pages/PodDetail.tsx`)

- [ ] **Step 1: Find the existing pages**

```bash
grep -rl 'containers' ui/src/pages/ | head
```
Expected: 1–3 matches identifying the workload and pod detail pages.

- [ ] **Step 2: Add the badge to each container row**

In `ui/src/pages/WorkloadDetail.tsx` (adapt names to what's actually there), at the top imports:

```tsx
import { ContainerVersionBadge } from '../components/ContainerVersionBadge'
```

Inside the JSX rendering containers (search for the `containers.map` usage), add the badge next to each entry:

```tsx
{workload.containers.map((c) => {
  const info = workload.containers_versions?.[c.name]
  return (
    <div key={c.name} className="container-row">
      <span>{c.name}</span>
      <code>{c.image}</code>
      <ContainerVersionBadge info={info} />
    </div>
  )
})}
```

Repeat the same pattern in `PodDetail.tsx`.

- [ ] **Step 3: Type-check and visual verification**

```bash
make ui-check && make ui-dev
```
Browse to a workload page; verify the badge renders. With no enrichment yet (the enricher hasn't run), expect ⚠ unknown for every container — that's correct.

- [ ] **Step 4: Commit**

```bash
git add ui/src/pages/WorkloadDetail.tsx ui/src/pages/PodDetail.tsx
git commit -m "feat(ui): show ContainerVersionBadge on workload/pod pages"
```

---

## Task 21 — UI: image inventory page (`/images`) and detail page

**Files:**
- Create: `ui/src/pages/Images.tsx`
- Create: `ui/src/pages/ImageDetail.tsx`
- Modify: `ui/src/App.tsx` (routing) and the sidebar component (find via grep `NavLink|sidebar`)

- [ ] **Step 1: Implement the inventory page**

```tsx
// ui/src/pages/Images.tsx
import React, { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listImageVersions, refreshImageVersions, type ImageVersion } from '../api'
import { useAuth } from '../auth' // existing pattern: tells us if user is admin

export function ImagesPage() {
  const { isAdmin } = useAuth()
  const [items, setItems] = useState<ImageVersion[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')
  const [errorsOnly, setErrorsOnly] = useState(false)

  const reload = async () => {
    setLoading(true)
    try {
      const r = await listImageVersions({
        image_repo: search || undefined,
        has_error: errorsOnly ? true : undefined,
      })
      setItems(r.items)
      setError(null)
    } catch (e) { setError(String(e)) }
    finally { setLoading(false) }
  }

  useEffect(() => { reload() /* eslint-disable-line */ }, [search, errorsOnly])

  const onRefresh = async () => {
    try {
      await refreshImageVersions()
      // Give the enricher a few seconds, then reload.
      setTimeout(reload, 3000)
    } catch (e) { setError(String(e)) }
  }

  return (
    <div className="images-page">
      <div className="header">
        <h1>Container images</h1>
        {isAdmin && (
          <button onClick={onRefresh}>Refresh now</button>
        )}
      </div>
      <div className="filters">
        <input placeholder="Search image_repo…"
               value={search}
               onChange={e => setSearch(e.target.value)} />
        <label>
          <input type="checkbox"
                 checked={errorsOnly}
                 onChange={e => setErrorsOnly(e.target.checked)} />
          Errors only
        </label>
      </div>
      {error && <div className="error">{error}</div>}
      {loading ? <div>Loading…</div> : (
        <table>
          <thead>
            <tr>
              <th>Image</th>
              <th>Variants</th>
              <th>Registry</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {items.map(iv => {
              const hasErr = iv.variants.some(v => v.last_error)
              return (
                <tr key={iv.image_repo}>
                  <td><Link to={`/images/${encodeURIComponent(iv.image_repo)}`}>{iv.image_repo}</Link></td>
                  <td>{iv.variants.length}</td>
                  <td>{iv.registry}</td>
                  <td>{hasErr ? '⛔ error' : '✓'}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Implement the detail page**

```tsx
// ui/src/pages/ImageDetail.tsx
import React, { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { getImageVersion, type ImageVersion } from '../api'

export function ImageDetailPage() {
  const { imageRepo = '' } = useParams<{ imageRepo: string }>()
  const [iv, setIv] = useState<ImageVersion | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    getImageVersion(decodeURIComponent(imageRepo))
      .then(setIv)
      .catch(e => setError(String(e)))
  }, [imageRepo])

  if (error) return <div className="error">{error}</div>
  if (!iv) return <div>Loading…</div>

  return (
    <div className="image-detail-page">
      <h1>{iv.image_repo}</h1>
      <p>Registry: {iv.registry}</p>
      <table>
        <thead>
          <tr>
            <th>Variant</th>
            <th>Latest tag</th>
            <th>Source</th>
            <th>Last checked</th>
            <th>Error</th>
          </tr>
        </thead>
        <tbody>
          {iv.variants.map((v, i) => (
            <tr key={i}>
              <td>{v.variant || '(default)'}</td>
              <td>{v.latest_tag ?? '—'}</td>
              <td>{v.source}</td>
              <td>{v.last_checked_at}</td>
              <td>{v.last_error || ''}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
```

- [ ] **Step 3: Wire the routes in `ui/src/App.tsx`**

Inside the existing `<Routes>` block:

```tsx
<Route path="/images" element={<ImagesPage />} />
<Route path="/images/:imageRepo" element={<ImageDetailPage />} />
```

Add the imports at the top.

- [ ] **Step 4: Add a sidebar entry**

Find the sidebar component (search for an existing entry like `NavLink to="/cloud-accounts"`). Add a sibling:

```tsx
<NavLink to="/images">Images</NavLink>
```

- [ ] **Step 5: Type-check and visual verification**

```bash
make ui-check && make ui-dev
```
Browse to `/images` and `/images/<repo>`.

- [ ] **Step 6: Commit**

```bash
git add ui/src/pages/Images.tsx ui/src/pages/ImageDetail.tsx ui/src/App.tsx ui/src/components/Sidebar.tsx
git commit -m "feat(ui): images inventory page, detail page, and sidebar link"
```

---

## Task 22 — UI: admin image-registries page + settings toggle

**Files:**
- Create: `ui/src/pages/admin/ImageRegistries.tsx`
- Modify: `ui/src/pages/admin/Settings.tsx` (or wherever the settings toggles live)
- Modify: `ui/src/App.tsx` and the admin tab/sidebar component

- [ ] **Step 1: Implement the admin page**

```tsx
// ui/src/pages/admin/ImageRegistries.tsx
import React, { useEffect, useState } from 'react'
import {
  listImageRegistries, createImageRegistry,
  updateImageRegistry, deleteImageRegistry, type ImageRegistry,
} from '../../api'

export function ImageRegistriesAdminPage() {
  const [items, setItems] = useState<ImageRegistry[]>([])
  const [error, setError] = useState<string | null>(null)
  const [newHostname, setNewHostname] = useState('')
  const [newRate, setNewRate] = useState('5')

  const reload = async () => {
    try {
      const r = await listImageRegistries()
      setItems(r.items)
      setError(null)
    } catch (e) { setError(String(e)) }
  }
  useEffect(() => { reload() }, [])

  const onCreate = async () => {
    if (!newHostname || !Number(newRate)) return
    try {
      await createImageRegistry({ hostname: newHostname, rate_limit_per_sec: Number(newRate) })
      setNewHostname('')
      setNewRate('5')
      reload()
    } catch (e) { setError(String(e)) }
  }
  const onToggleEnabled = async (host: string, enabled: boolean) => {
    try { await updateImageRegistry(host, { enabled }); reload() }
    catch (e) { setError(String(e)) }
  }
  const onDelete = async (host: string) => {
    if (!confirm(`Delete ${host}?`)) return
    try { await deleteImageRegistry(host); reload() }
    catch (e) { setError(String(e)) }
  }

  return (
    <div className="admin-image-registries">
      <h2>Image registries</h2>
      {error && <div className="error">{error}</div>}
      <table>
        <thead><tr>
          <th>Hostname</th><th>Rate limit (req/s)</th><th>Enabled</th><th>Notes</th><th>Actions</th>
        </tr></thead>
        <tbody>
          {items.map(r => (
            <tr key={r.hostname}>
              <td><code>{r.hostname}</code></td>
              <td>{r.rate_limit_per_sec}</td>
              <td>
                <input type="checkbox" checked={r.enabled}
                       onChange={e => onToggleEnabled(r.hostname, e.target.checked)} />
              </td>
              <td>{r.notes ?? ''}</td>
              <td><button onClick={() => onDelete(r.hostname)}>Delete</button></td>
            </tr>
          ))}
        </tbody>
      </table>

      <h3>Add a registry</h3>
      <div className="add-form">
        <input placeholder="hostname (e.g., docker.io or *.example.com)"
               value={newHostname} onChange={e => setNewHostname(e.target.value)} />
        <input type="number" min="0.1" step="0.1"
               value={newRate} onChange={e => setNewRate(e.target.value)} />
        <button onClick={onCreate}>Add</button>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Add the settings toggle**

Find the existing settings page (likely `ui/src/pages/admin/Settings.tsx` based on the existing pattern with EOL/MCP). Add a new toggle stanza:

```tsx
<label>
  <input type="checkbox"
         checked={settings.image_versions_enabled ?? false}
         onChange={e => patch({ image_versions_enabled: e.target.checked })} />
  Image versions enrichment
</label>
<p className="hint">Periodically queries public registries for the latest version of each image used in your clusters.</p>
```

(Adapt to the actual `patch` / state-update pattern in that file.)

- [ ] **Step 3: Wire the route + admin tab**

In `App.tsx` admin block (find existing admin routes near line 259):

```tsx
<Route path="/admin/image-registries" element={<ImageRegistriesAdminPage />} />
```

In the admin tabs/sidebar component, add a sibling tab:

```tsx
<NavLink to="/admin/image-registries">Image registries</NavLink>
```

- [ ] **Step 4: Type-check and visual verification**

```bash
make ui-check && make ui-dev
```
As an admin user, browse to `/admin/image-registries` — see the 7 default rows. As a non-admin, the tab should not appear.

- [ ] **Step 5: Commit**

```bash
git add ui/src/pages/admin/ImageRegistries.tsx ui/src/pages/admin/Settings.tsx ui/src/App.tsx
git commit -m "feat(ui/admin): image registries CRUD page and settings toggle"
```

---

## Task 23 — Live smoke test (gated, optional but recommended)

**Files:**
- Create: `internal/imageversions/registry/live_test.go`

- [ ] **Step 1: Write the gated smoke test**

```go
//go:build live

package registry_test

import (
    "context"
    "os"
    "testing"

    "github.com/sthalbert/longue-vue/internal/imageversions/registry"
)

func skipUnlessLive(t *testing.T) {
    if os.Getenv("LONGUE_VUE_LIVE_TESTS") != "1" {
        t.Skip("set LONGUE_VUE_LIVE_TESTS=1 to enable live smoke tests")
    }
}

func TestLive_DockerHub_Nginx(t *testing.T) {
    skipUnlessLive(t)
    c := registry.NewClient()
    tags, err := c.ListTags(context.Background(), "https://registry-1.docker.io", "library/nginx")
    if err != nil { t.Fatalf("dockerhub: %v", err) }
    if len(tags) < 10 {
        t.Fatalf("expected many nginx tags, got %d", len(tags))
    }
}

func TestLive_Quay_Prometheus(t *testing.T) {
    skipUnlessLive(t)
    c := registry.NewClient()
    tags, err := c.ListTags(context.Background(), "https://quay.io", "prometheus/prometheus")
    if err != nil { t.Fatalf("quay: %v", err) }
    if len(tags) < 5 {
        t.Fatalf("expected several prometheus tags, got %d", len(tags))
    }
}
```

- [ ] **Step 2: Run with the live tag (one-time check)**

```bash
LONGUE_VUE_LIVE_TESTS=1 go test -tags=live -run '^TestLive_' ./internal/imageversions/registry/
```
Expected: PASS (assuming network access and no Docker Hub rate-limit issues).

- [ ] **Step 3: Commit**

```bash
git add internal/imageversions/registry/live_test.go
git commit -m "test(imageversions/registry): live smoke against public registries (gated)"
```

---

## Task 24 — Documentation: ADR-0020 + user docs + CLAUDE.md update

**Files:**
- Create: `docs/adr/0020-image-versions-enrichment.md`
- Create: `docs/image-versions.md`
- Modify: `CLAUDE.md` (add a brief paragraph)

- [ ] **Step 1: Draft the ADR**

```bash
ls docs/adr/ | tail -3
```
Confirm the next ADR number is 0020. If higher (e.g., a new ADR has landed), shift accordingly.

Create `docs/adr/0020-image-versions-enrichment.md` summarizing the spec and decisions:

```markdown
# ADR-0020 — Container image versions enrichment (V1)

## Status
Accepted

## Context
ADR-0012 deferred container image enrichment to "v2 scope" because matching arbitrary image tags to endoflife.date products requires registry-aware parsing and a heuristic mapping layer. V1 of that work delivers the simplest useful slice: the latest available tag per image, sourced directly from the originating public registry, without any image-to-product mapping.

## Decision
- New top-level `image_versions` table keyed by `(image_repo, variant)`.
- Periodic enricher in `internal/imageversions/`, default 24h, with a manual `POST /v1/image-versions/refresh` admin trigger.
- Allowlist of registries lives in DB (`image_versions_registries`), seeded by migration with the seven major public registries; admin CRUD handles overrides at runtime.
- The annotation column reuses `eol.Annotation` so a future "rich" enrichment (V3) can populate EOL fields without a schema migration.
- Workload/pod GET responses gain a `containers_versions` sibling field via a server-side join.

## Consequences
- New surface: 5 endpoints, 2 tables, 1 settings field, 3 new UI pages.
- Coverage limited to public registries; private registries deferred (V2).
- Variant-aware comparisons use the semver prefix only — variant suffix differences (e.g., `alpine3.18` vs `alpine3.19`) aren't ordered. Acceptable in V1; revisit if confusing in practice.

## References
- Spec: `docs/superpowers/specs/2026-05-08-image-versions-design.md`
- Plan: `docs/superpowers/plans/2026-05-08-image-versions.md`
```

- [ ] **Step 2: Write user-facing docs `docs/image-versions.md`**

```markdown
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

- **Workload / pod detail pages** — each container row gets a status badge (✓ up-to-date, ↑ behind, ⚠ unknown, ⛔ error).
- **`/images` page** — global inventory of all images currently used, with their latest available tag, registry, and last-checked timestamp.

## Out of scope (V1)

Private registries, tag-pattern policies, EOL/CVE enrichment, and GitHub releases lookup are explicitly deferred to V2/V3.

## Triggering a refresh

Admins can click **Refresh now** on the `/images` page (or `POST /v1/image-versions/refresh`) to run the enrichment cycle immediately.
```

- [ ] **Step 3: Update `CLAUDE.md`**

Add a short paragraph in the "Settings + feature toggles" section:

```markdown
**Image versions enricher (`image_versions_enabled`, ADR-0020):** queries public registries for the latest tag of each container image used in workloads/pods. Default interval 24h (`LONGUE_VUE_IMAGE_VERSIONS_INTERVAL`). Allowlist of registries is in `image_versions_registries` (DB-backed, admin CRUD). Reuses `eol.Annotation` JSONB so a richer V3 (EOL/CVE) is purely additive.
```

- [ ] **Step 4: Final test sweep**

```bash
make check
```
Expected: all unit + integration tests PASS, OpenAPI drift PASSES, linter PASSES.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0020-image-versions-enrichment.md docs/image-versions.md CLAUDE.md
git commit -m "docs: ADR-0020, user docs, and CLAUDE.md update for image versions"
```

---

## Task 25 — Prometheus metrics

**Files:**
- Create: `internal/imageversions/metrics.go`
- Modify: `internal/imageversions/enricher.go` (instrument tick + per-query)
- Modify: `internal/imageversions/registry/client.go` (instrument outbound calls)
- Modify: `cmd/longue-vue/main.go` (register collectors with the existing Prometheus registry)
- Create: `internal/imageversions/metrics_test.go`

- [ ] **Step 1: Locate the Prometheus registry used by the EOL enricher**

```bash
grep -n -E 'prometheus.NewRegistry|MustRegister' cmd/longue-vue/main.go internal/eol/*.go | head
```
Expected: identifies whether the project uses the default prom registry, a private one, or a shared `*prometheus.Registry` instance. The new collectors must use the same one (the spec says metrics are exposed via `/metrics`, which is unauthenticated — confirm it's the global one or note the registry passed in).

- [ ] **Step 2: Implement `internal/imageversions/metrics.go`**

```go
package imageversions

import "github.com/prometheus/client_golang/prometheus"

// Metrics bundles the Prometheus collectors for the image versions enricher.
// Constructed once at startup and passed into the enricher and the registry client.
type Metrics struct {
    TickTotal             *prometheus.CounterVec
    TickDuration          prometheus.Histogram
    QueryTotal            *prometheus.CounterVec
    QueryDuration         *prometheus.HistogramVec
    KnownTotal            prometheus.Gauge
    WithErrorTotal        prometheus.Gauge
    LastTickTimestamp     prometheus.Gauge
    RegistriesEnabledTotal prometheus.Gauge
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
    m := &Metrics{
        TickTotal: prometheus.NewCounterVec(
            prometheus.CounterOpts{Name: "imageversions_tick_total", Help: "Image-versions enricher ticks completed."},
            []string{"status"},
        ),
        TickDuration: prometheus.NewHistogram(
            prometheus.HistogramOpts{Name: "imageversions_tick_duration_seconds", Help: "Tick wall time in seconds.",
                Buckets: prometheus.DefBuckets},
        ),
        QueryTotal: prometheus.NewCounterVec(
            prometheus.CounterOpts{Name: "imageversions_query_total", Help: "Registry tag-list queries."},
            []string{"registry", "status"},
        ),
        QueryDuration: prometheus.NewHistogramVec(
            prometheus.HistogramOpts{Name: "imageversions_query_duration_seconds", Help: "Per-registry query latency in seconds.",
                Buckets: prometheus.DefBuckets},
            []string{"registry"},
        ),
        KnownTotal: prometheus.NewGauge(
            prometheus.GaugeOpts{Name: "imageversions_known_total", Help: "Rows in image_versions."},
        ),
        WithErrorTotal: prometheus.NewGauge(
            prometheus.GaugeOpts{Name: "imageversions_with_error_total", Help: "Rows in image_versions with last_error set."},
        ),
        LastTickTimestamp: prometheus.NewGauge(
            prometheus.GaugeOpts{Name: "imageversions_last_tick_timestamp_seconds", Help: "Timestamp of the last completed tick (Unix seconds)."},
        ),
        RegistriesEnabledTotal: prometheus.NewGauge(
            prometheus.GaugeOpts{Name: "imageversions_registries_enabled", Help: "Count of enabled rows in image_versions_registries."},
        ),
    }
    reg.MustRegister(
        m.TickTotal, m.TickDuration, m.QueryTotal, m.QueryDuration,
        m.KnownTotal, m.WithErrorTotal, m.LastTickTimestamp, m.RegistriesEnabledTotal,
    )
    return m
}
```

- [ ] **Step 3: Plumb `Metrics` into `Enricher` and `registry.Client`**

In `internal/imageversions/enricher.go`, add a `metrics *Metrics` field to `Enricher`, accept it in `NewEnricher`, and instrument:

```go
// At the start of RunTick:
start := time.Now()
defer func() {
    e.metrics.TickDuration.Observe(time.Since(start).Seconds())
    e.metrics.LastTickTimestamp.Set(float64(time.Now().Unix()))
}()

// On early return paths (settings disabled, list registries failure):
e.metrics.TickTotal.WithLabelValues("failure").Inc()  // for failures
e.metrics.TickTotal.WithLabelValues("success").Inc()  // at the end of the happy path

// After Listing registries:
e.metrics.RegistriesEnabledTotal.Set(float64(len(enabledRegs)))

// After processing all (during DeleteImageVersionsNotIn invocation), update the
// known/with-error gauges by querying the store. (Add a new tiny store helper
// `CountImageVersions(ctx) (total, withError int64, err error)` if needed,
// or compute from rows already in memory if the enricher knows them.)
```

If you add `CountImageVersions`, ship it in this same task — keep the SQL trivial:

```sql
SELECT
  COUNT(*) AS total,
  COUNT(*) FILTER (WHERE last_error IS NOT NULL) AS with_error
FROM image_versions
```

In `internal/imageversions/registry/client.go`, accept an optional `*Metrics`-shaped observer (define a small interface to avoid an import cycle, since `registry` is a sub-package). Wrap each `c.http.Do` in metric observations:

```go
// Pseudocode — adapt to where the request is sent
qStart := time.Now()
resp, err := c.http.Do(req)
elapsed := time.Since(qStart).Seconds()
status := "success"
switch {
case errors.Is(err, ErrRateLimited): status = "rate_limited"
case errors.Is(err, ErrRepoNotFound): status = "not_found"
case err != nil: status = "error"
}
if c.observer != nil {
    c.observer.ObserveQuery(registryHost, status, elapsed)
}
```

Define the observer interface in `registry/client.go`:

```go
type QueryObserver interface {
    ObserveQuery(registry, status string, durationSeconds float64)
}
```

And in `imageversions/metrics.go`, make `Metrics` satisfy it:

```go
func (m *Metrics) ObserveQuery(registry, status string, dur float64) {
    m.QueryTotal.WithLabelValues(registry, status).Inc()
    m.QueryDuration.WithLabelValues(registry).Observe(dur)
}
```

- [ ] **Step 4: Wire metrics in `cmd/longue-vue/main.go`**

In `maybeStartImageVersionsEnricher`, accept (or import) the project's `prometheus.Registerer` (the default global registry is fine if that's what the rest of the project uses):

```go
metrics := imageversions.NewMetrics(prometheus.DefaultRegisterer)
client := registry.NewClientWithObserver(metrics) // new constructor variant accepting an observer
enricher := imageversions.NewEnricher(s, client, interval, metrics)
```

Add the new constructor `registry.NewClientWithObserver(o QueryObserver) *Client` next to `NewClient()`; both share the same struct, only the `observer` field differs.

- [ ] **Step 5: Write the metrics smoke test `internal/imageversions/metrics_test.go`**

```go
package imageversions

import (
    "testing"

    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetrics_RegisterAndIncrement(t *testing.T) {
    reg := prometheus.NewRegistry()
    m := NewMetrics(reg)

    m.TickTotal.WithLabelValues("success").Inc()
    m.ObserveQuery("docker.io", "success", 0.123)
    m.RegistriesEnabledTotal.Set(7)

    if v := testutil.ToFloat64(m.TickTotal.WithLabelValues("success")); v != 1 {
        t.Errorf("tick_total: want 1, got %v", v)
    }
    if v := testutil.ToFloat64(m.QueryTotal.WithLabelValues("docker.io", "success")); v != 1 {
        t.Errorf("query_total: want 1, got %v", v)
    }
    if v := testutil.ToFloat64(m.RegistriesEnabledTotal); v != 7 {
        t.Errorf("registries_enabled: want 7, got %v", v)
    }
}
```

- [ ] **Step 6: Run the test**

```bash
go test -race -run '^TestMetrics_RegisterAndIncrement$' ./internal/imageversions/
```
Expected: PASS.

- [ ] **Step 7: Verify metrics show up at `/metrics`**

Boot the server and hit:

```bash
curl -s http://localhost:8080/metrics | grep -E '^imageversions_' | head -20
```
Expected: at least the gauges (`imageversions_known_total`, `imageversions_registries_enabled`) appear with zero values immediately, and counters/histograms appear after the first tick.

- [ ] **Step 8: Commit**

```bash
git add internal/imageversions/metrics.go internal/imageversions/metrics_test.go internal/imageversions/enricher.go internal/imageversions/registry/client.go internal/store/pg_image_versions.go cmd/longue-vue/main.go
git commit -m "feat(imageversions): Prometheus metrics for ticks and registry queries"
```

---

## Final verification checklist

After all 24 tasks are complete, run:

- [ ] `make check` — all green
- [ ] Manual smoke (with `PGX_TEST_DATABASE` and a running PG):
  - Boot the server with `LONGUE_VUE_IMAGE_VERSIONS_ENABLED=true LONGUE_VUE_IMAGE_VERSIONS_INTERVAL=1m`
  - Have at least one workload using `nginx:1.25.3` in the DB
  - After ~1 minute, `curl /v1/image-versions` returns a populated `items[]`
  - The UI `/images` page shows `docker.io/library/nginx` with a recent `latest_tag`
  - Workload detail page shows the ↑ behind badge
- [ ] Admin CRUD: add a custom registry via the UI, list, edit, delete — all 200/201/204 responses
- [ ] Refresh button (admin) → toast shows "queued"; calling twice in quick succession shows `already_running: true` in the second response
- [ ] Disabling the toggle stops new ticks and the refresh button returns 409
