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
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestImageVersions_UpsertAndGet(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(), "DELETE FROM image_versions")
	})

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
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if iv.LatestTag == nil || *iv.LatestTag != "1.27.4" {
		t.Fatalf("latest_tag mismatch: %+v", iv.LatestTag)
	}

	rows, err := pg.GetImageVersionsByRepo(ctx, "docker.io/library/nginx")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	// Upsert again — should update, not insert.
	latest2 := "1.27.5"
	_, err = pg.UpsertImageVersion(ctx, api.ImageVersionUpsert{
		ImageRepo:     "docker.io/library/nginx",
		Variant:       "",
		Registry:      "docker.io",
		LatestTag:     &latest2,
		Annotation:    ann,
		Source:        "registry",
		LastCheckedAt: now,
	})
	if err != nil {
		t.Fatalf("upsert (update path): %v", err)
	}
	rows2, _ := pg.GetImageVersionsByRepo(ctx, "docker.io/library/nginx")
	if len(rows2) != 1 || rows2[0].LatestTag == nil || *rows2[0].LatestTag != "1.27.5" {
		t.Fatalf("expected single row updated to 1.27.5, got %+v", rows2)
	}
}

func TestImageVersions_ReapNotIn(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(), "DELETE FROM image_versions")
	})

	ann := mustJSON(t, map[string]any{})
	now := time.Now().UTC()

	keep := "1.0.0"
	drop := "0.1.0"
	if _, err := pg.UpsertImageVersion(ctx, api.ImageVersionUpsert{
		ImageRepo: "docker.io/library/keep", Variant: "", Registry: "docker.io",
		LatestTag: &keep, Annotation: ann, Source: "registry", LastCheckedAt: now,
	}); err != nil {
		t.Fatalf("upsert keep: %v", err)
	}
	if _, err := pg.UpsertImageVersion(ctx, api.ImageVersionUpsert{
		ImageRepo: "docker.io/library/drop", Variant: "", Registry: "docker.io",
		LatestTag: &drop, Annotation: ann, Source: "registry", LastCheckedAt: now,
	}); err != nil {
		t.Fatalf("upsert drop: %v", err)
	}

	deleted, err := pg.DeleteImageVersionsNotIn(ctx, [][2]string{
		{"docker.io/library/keep", ""},
	})
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	rows, _ := pg.GetImageVersionsByRepo(ctx, "docker.io/library/drop")
	if len(rows) != 0 {
		t.Fatalf("expected drop row gone, got %+v", rows)
	}
	rows, _ = pg.GetImageVersionsByRepo(ctx, "docker.io/library/keep")
	if len(rows) != 1 {
		t.Fatalf("expected keep row preserved")
	}
}

func TestImageVersions_ReapAll_WhenKeepEmpty(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(), "DELETE FROM image_versions")
	})

	ann := mustJSON(t, map[string]any{})
	now := time.Now().UTC()
	latest := "1.0.0"
	if _, err := pg.UpsertImageVersion(ctx, api.ImageVersionUpsert{
		ImageRepo: "docker.io/library/foo", Variant: "", Registry: "docker.io",
		LatestTag: &latest, Annotation: ann, Source: "registry", LastCheckedAt: now,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	deleted, err := pg.DeleteImageVersionsNotIn(ctx, nil)
	if err != nil {
		t.Fatalf("reap all: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted on empty keep set, got %d", deleted)
	}
}

func TestImageVersions_DistinctImageRefs(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// Seed a cluster and namespace so workloads can be inserted (FK chain:
	// clusters → namespaces → workloads). The top-level newTestPG cleanup
	// truncates clusters CASCADE, so no explicit cleanup is needed here.
	nsID := mustSeedNamespaceForImageVersionsTest(t, pg)

	containers := mustJSON(t, []map[string]any{
		{"name": "web", "image": "nginx:1.25.3"},
		{"name": "side", "image": "quay.io/prometheus/prometheus:v2.45.0"},
	})
	_, err := pg.pool.Exec(ctx, `
INSERT INTO workloads (id, namespace_id, kind, name, containers, created_at, updated_at)
VALUES (gen_random_uuid(), $1, 'Deployment', 'app', $2, now(), now())`,
		nsID, containers)
	if err != nil {
		t.Fatalf("seed workload: %v", err)
	}

	refs, err := pg.DistinctImageRefs(ctx)
	if err != nil {
		t.Fatalf("distinct: %v", err)
	}
	seen := map[string]bool{}
	for _, r := range refs {
		seen[r] = true
	}
	if !seen["nginx:1.25.3"] || !seen["quay.io/prometheus/prometheus:v2.45.0"] {
		t.Fatalf("missing expected refs in %v", refs)
	}
}

// mustSeedNamespaceForImageVersionsTest creates a minimal cluster + namespace
// and returns the namespace UUID. Used only by DistinctImageRefs test.
func mustSeedNamespaceForImageVersionsTest(t *testing.T, pg *PG) string {
	t.Helper()
	ctx := context.Background()

	var clusterID string
	err := pg.pool.QueryRow(ctx, `
INSERT INTO clusters (id, name, created_at, updated_at)
VALUES (gen_random_uuid(), $1, now(), now())
RETURNING id::text`, t.Name()).Scan(&clusterID)
	if err != nil {
		t.Fatalf("seed cluster: %v", err)
	}

	var nsID string
	err = pg.pool.QueryRow(ctx, `
INSERT INTO namespaces (id, cluster_id, name, created_at, updated_at)
VALUES (gen_random_uuid(), $1, 'default', now(), now())
RETURNING id::text`, clusterID).Scan(&nsID)
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	return nsID
}

func TestImageVersions_ListByRepo_Pagination(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(), "DELETE FROM image_versions")
	})

	ann := mustJSON(t, map[string]any{})
	now := time.Now().UTC()
	latest := "1.0.0"
	for _, repo := range []string{
		"docker.io/library/a", "docker.io/library/b", "docker.io/library/c",
		"docker.io/library/d", "docker.io/library/e",
	} {
		for _, v := range []string{"", "alpine"} {
			_, err := pg.UpsertImageVersion(ctx, api.ImageVersionUpsert{
				ImageRepo: repo, Variant: v, Registry: "docker.io",
				LatestTag: &latest, Annotation: ann, Source: "registry", LastCheckedAt: now,
			})
			if err != nil {
				t.Fatalf("upsert: %v", err)
			}
		}
	}

	items, next, err := pg.ListImageVersionsByRepo(ctx, api.ImageVersionListParams{Limit: 2})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if len(items) != 2 || next == "" {
		t.Fatalf("page 1: items=%d next=%q (expected 2 items + non-empty cursor)", len(items), next)
	}
	if len(items[0].Variants) != 2 {
		t.Fatalf("expected both variants nested under first repo, got %d", len(items[0].Variants))
	}

	items2, next2, err := pg.ListImageVersionsByRepo(ctx, api.ImageVersionListParams{Limit: 10, Cursor: next})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(items2) != 3 || next2 != "" {
		t.Fatalf("page 2: items=%d next=%q (expected 3 items + empty cursor)", len(items2), next2)
	}
}
