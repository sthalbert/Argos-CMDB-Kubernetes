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

//nolint:gocyclo // straight-line seed-then-assert flow; flat structure is clearer than factored helpers
func TestEnrichContainersVersions_ResolvedOrigin(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	ctx := context.Background()

	// Seed the origin row in image_versions.
	latest := "0.27"
	if _, err := s.UpsertImageVersion(ctx, ImageVersionUpsert{
		ImageRepo:     "ghcr.io/sthalbert/longue-vue-collector",
		Variant:       "",
		Registry:      "ghcr.io",
		LatestTag:     &latest,
		Annotation:    json.RawMessage(`{}`),
		Source:        "registry",
		LastCheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed image_versions: %v", err)
	}

	// Seed the resolution row keyed on the pod-ref's image_repo.
	origin := "ghcr.io/sthalbert/longue-vue-collector"
	via := testMirrorHostname
	if _, err := s.UpsertImageOriginResolution(ctx, ImageOriginResolutionUpsert{
		MirrorImageRepo: "local.example.com/containers/sthalbert/longue-vue-collector",
		Variant:         "",
		OriginImageRepo: &origin,
		ViaHostname:     &via,
		ResolvedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed origin_resolutions: %v", err)
	}

	out := EnrichContainersVersions(ctx, s, []map[string]any{
		{"name": "lv", "image": "local.example.com/containers/sthalbert/longue-vue-collector:0.26"},
	})

	v, ok := out["lv"]
	if !ok {
		t.Fatalf("want enriched 'lv', got %v", out)
	}
	if v.OriginStatus == nil || *v.OriginStatus != Resolved {
		t.Errorf("origin_status: want resolved, got %v", v.OriginStatus)
	}
	if v.OriginImageRepo == nil || *v.OriginImageRepo != origin {
		t.Errorf("origin_image_repo: want %q, got %v", origin, v.OriginImageRepo)
	}
	if v.LatestTag == nil || *v.LatestTag != "0.27" {
		t.Errorf("latest_tag: want 0.27, got %v", v.LatestTag)
	}
	if v.IsBehind == nil || !*v.IsBehind {
		t.Errorf("is_behind: want true (0.26 < 0.27), got %v", v.IsBehind)
	}
}

func TestEnrichContainersVersions_UnresolvedOrigin(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	ctx := context.Background()

	errMsg := "missing_annotation"
	now := time.Now().UTC()
	if _, err := s.UpsertImageOriginResolution(ctx, ImageOriginResolutionUpsert{
		MirrorImageRepo: "local.example.com/x/y",
		Variant:         "",
		ResolvedAt:      now,
		LastError:       &errMsg,
		LastErrorAt:     &now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	out := EnrichContainersVersions(ctx, s, []map[string]any{
		{"name": "lv", "image": "local.example.com/x/y:1.0.0"},
	})
	v, ok := out["lv"]
	if !ok {
		t.Fatalf("want unresolved entry for 'lv', got %v", out)
	}
	if v.OriginStatus == nil || *v.OriginStatus != Unresolved {
		t.Errorf("origin_status: want unresolved, got %v", v.OriginStatus)
	}
	if v.OriginError == nil || *v.OriginError != errMsg {
		t.Errorf("origin_error: want %q, got %v", errMsg, v.OriginError)
	}
	if v.LatestTag != nil {
		t.Errorf("latest_tag should be nil on unresolved, got %v", v.LatestTag)
	}
}
