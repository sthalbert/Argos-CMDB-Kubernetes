package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestPG_ImageOriginResolutions_UpsertGet(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(), "TRUNCATE image_origin_resolutions")
	})

	now := time.Now().UTC().Truncate(time.Microsecond)
	origin := "ghcr.io/sthalbert/longue-vue-collector"
	via := "mirror.io"
	row, err := pg.UpsertImageOriginResolution(ctx, api.ImageOriginResolutionUpsert{
		MirrorImageRepo: "local-registry.io/containers/sthalbert/longue-vue-collector",
		Variant:         "",
		OriginImageRepo: &origin,
		ViaHostname:     &via,
		ResolvedAt:      now,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if row.OriginImageRepo == nil || *row.OriginImageRepo != origin {
		t.Fatalf("origin: want %q, got %v", origin, row.OriginImageRepo)
	}

	got, err := pg.GetImageOriginResolution(ctx,
		"local-registry.io/containers/sthalbert/longue-vue-collector", "")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.OriginImageRepo == nil || *got.OriginImageRepo != origin {
		t.Fatalf("get origin: want %q, got %v", origin, got.OriginImageRepo)
	}
	if got.ViaHostname == nil || *got.ViaHostname != via {
		t.Fatalf("via: want %q, got %v", via, got.ViaHostname)
	}
	if got.LastError != nil {
		t.Errorf("last_error: want nil, got %v", got.LastError)
	}
}

func TestPG_ImageOriginResolutions_FailureRow(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(), "TRUNCATE image_origin_resolutions")
	})

	now := time.Now().UTC().Truncate(time.Microsecond)
	errMsg := "missing_annotation"
	_, err := pg.UpsertImageOriginResolution(ctx, api.ImageOriginResolutionUpsert{
		MirrorImageRepo: "local-registry.io/x/y",
		Variant:         "",
		ResolvedAt:      now,
		LastError:       &errMsg,
		LastErrorAt:     &now,
	})
	if err != nil {
		t.Fatalf("upsert failure row: %v", err)
	}

	got, err := pg.GetImageOriginResolution(ctx, "local-registry.io/x/y", "")
	if err != nil {
		t.Fatalf("get failure row: %v", err)
	}
	if got.OriginImageRepo != nil {
		t.Errorf("expected NULL origin on failure row, got %v", got.OriginImageRepo)
	}
	if got.LastError == nil || *got.LastError != errMsg {
		t.Fatalf("last_error: want %q, got %v", errMsg, got.LastError)
	}
}

func TestPG_ImageOriginResolutions_NotFound(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	_, err := pg.GetImageOriginResolution(ctx, "nope", "")
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestPG_ImageOriginResolutions_DeleteNotIn(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(), "TRUNCATE image_origin_resolutions")
	})

	now := time.Now().UTC()
	origin := "ghcr.io/x"
	for _, mirror := range []string{"a/x", "b/x", "c/x"} {
		if _, err := pg.UpsertImageOriginResolution(ctx, api.ImageOriginResolutionUpsert{
			MirrorImageRepo: mirror, Variant: "", OriginImageRepo: &origin,
			ViaHostname: &origin, ResolvedAt: now,
		}); err != nil {
			t.Fatalf("seed %s: %v", mirror, err)
		}
	}
	deleted, err := pg.DeleteImageOriginResolutionsNotIn(ctx, [][2]string{
		{"a/x", ""},
		{"c/x", ""},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted: want 1, got %d", deleted)
	}
	if _, err := pg.GetImageOriginResolution(ctx, "b/x", ""); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("b/x should be gone, got %v", err)
	}
}

func TestPG_ImageOriginResolutions_DeleteAll(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(), "TRUNCATE image_origin_resolutions")
	})

	now := time.Now().UTC()
	origin := "ghcr.io/x"
	if _, err := pg.UpsertImageOriginResolution(ctx, api.ImageOriginResolutionUpsert{
		MirrorImageRepo: "a/x", Variant: "", OriginImageRepo: &origin,
		ViaHostname: &origin, ResolvedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	deleted, err := pg.DeleteImageOriginResolutionsNotIn(ctx, nil)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted: want 1, got %d", deleted)
	}
}
