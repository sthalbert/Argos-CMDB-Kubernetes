package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestEnrichContainersVersions(t *testing.T) {
	t.Parallel()

	s := newMemStore()
	ctx := context.Background()
	latest := "1.27.4"
	_, err := s.UpsertImageVersion(ctx, ImageVersionUpsert{
		ImageRepo:     "docker.io/library/nginx",
		Variant:       "",
		Registry:      "docker.io",
		LatestTag:     &latest,
		Annotation:    json.RawMessage(`{}`),
		Source:        "registry",
		LastCheckedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("upsert image version: %v", err)
	}

	out := EnrichContainersVersions(ctx, s, []map[string]any{
		{"name": "web", "image": "nginx:1.25.3"},
		{"name": "side", "image": "nginx:latest"},         // skipped: no usable semver tag
		{"name": "alt", "image": "harbor.corp/foo:1.0.0"}, // not in image_versions
	})

	// "web" should be enriched: nginx:1.25.3 → latest 1.27.4 (is_behind=true)
	v, ok := out["web"]
	if !ok {
		t.Fatalf("expected 'web' to be enriched, got map: %v", out)
	}
	if v.LatestTag == nil || *v.LatestTag != "1.27.4" {
		t.Errorf("web.LatestTag: want 1.27.4, got %v", v.LatestTag)
	}
	if v.IsBehind == nil || !*v.IsBehind {
		t.Errorf("web.IsBehind: want true (1.25.3 < 1.27.4)")
	}

	// "side" has a :latest tag — ParseImageRef rejects it (ErrSkip).
	if _, ok := out["side"]; ok {
		t.Errorf("'side' should not be enriched (image is :latest)")
	}

	// "alt" is not in image_versions — no row returned.
	if _, ok := out["alt"]; ok {
		t.Errorf("'alt' should not be enriched (no image_versions row)")
	}
}

func TestEnrichContainersVersions_NotBehind(t *testing.T) {
	t.Parallel()

	s := newMemStore()
	ctx := context.Background()
	latest := "1.25.3"
	_, err := s.UpsertImageVersion(ctx, ImageVersionUpsert{
		ImageRepo:     "docker.io/library/nginx",
		Variant:       "",
		Registry:      "docker.io",
		LatestTag:     &latest,
		Annotation:    json.RawMessage(`{}`),
		Source:        "registry",
		LastCheckedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("upsert image version: %v", err)
	}

	out := EnrichContainersVersions(ctx, s, []map[string]any{
		{"name": "web", "image": "nginx:1.25.3"},
	})

	v, ok := out["web"]
	if !ok {
		t.Fatalf("expected 'web' to be enriched")
	}
	if v.IsBehind == nil || *v.IsBehind {
		t.Errorf("web.IsBehind: want false (1.25.3 == 1.25.3)")
	}
}

func TestEnrichContainersVersions_Variant(t *testing.T) {
	t.Parallel()

	s := newMemStore()
	ctx := context.Background()

	// Seed only the alpine variant.
	latestAlpine := "1.27.4-alpine"
	_, err := s.UpsertImageVersion(ctx, ImageVersionUpsert{
		ImageRepo:     "docker.io/library/nginx",
		Variant:       "alpine",
		Registry:      "docker.io",
		LatestTag:     &latestAlpine,
		Annotation:    json.RawMessage(`{}`),
		Source:        "registry",
		LastCheckedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("upsert image version: %v", err)
	}

	// nginx:1.25.3 (no variant) should NOT match the alpine variant row.
	out := EnrichContainersVersions(ctx, s, []map[string]any{
		{"name": "web", "image": "nginx:1.25.3"},
		{"name": "alpine", "image": "nginx:1.25.3-alpine"},
	})

	if _, ok := out["web"]; ok {
		t.Errorf("'web' (no-variant image) should not match alpine variant row")
	}

	v, ok := out["alpine"]
	if !ok {
		t.Fatalf("expected 'alpine' container to be enriched")
	}
	if v.LatestTag == nil || *v.LatestTag != "1.27.4-alpine" {
		t.Errorf("alpine.LatestTag: want 1.27.4-alpine, got %v", v.LatestTag)
	}
}

func TestEnrichContainersVersions_EmptyContainers(t *testing.T) {
	t.Parallel()

	s := newMemStore()
	ctx := context.Background()
	out := EnrichContainersVersions(ctx, s, nil)
	if out != nil {
		t.Errorf("empty containers should return nil, got %v", out)
	}
}

func TestEnrichContainersVersions_MissingNameOrImage(t *testing.T) {
	t.Parallel()

	s := newMemStore()
	ctx := context.Background()
	out := EnrichContainersVersions(ctx, s, []map[string]any{
		{"name": ""},
		{"image": "nginx:1.25.3"},
		{"name": "web"},
	})
	if out != nil {
		t.Errorf("containers with missing name/image should return nil, got %v", out)
	}
}
