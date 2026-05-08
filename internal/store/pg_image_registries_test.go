package store

import (
	"context"
	"errors"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestImageRegistries_SeedDefaults(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	list, err := pg.ListImageRegistries(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
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

//nolint:gocyclo // exercises the full CRUD lifecycle in one test for clarity
func TestImageRegistries_CreateGetUpdateDelete(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// Cleanup in case assertions fail before the explicit delete below.
	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(),
			"DELETE FROM image_versions_registries WHERE hostname = 'mirror.example.com'")
	})

	notes := "internal mirror"
	created, err := pg.CreateImageRegistry(ctx, api.ImageRegistryUpsert{
		Hostname:        "mirror.example.com",
		RateLimitPerSec: 2.5,
		Notes:           &notes,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.RateLimitPerSec != 2.5 || !created.Enabled {
		t.Fatalf("unexpected created: %+v", created)
	}

	got, err := pg.GetImageRegistry(ctx, "mirror.example.com")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Notes == nil || *got.Notes != "internal mirror" {
		t.Fatalf("notes mismatch: %+v", got.Notes)
	}

	off := false
	upd, err := pg.UpdateImageRegistry(ctx, "mirror.example.com",
		api.ImageRegistryPatch{Enabled: &off})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Enabled {
		t.Fatalf("expected disabled after patch")
	}

	if err := pg.DeleteImageRegistry(ctx, "mirror.example.com"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := pg.GetImageRegistry(ctx, "mirror.example.com"); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestImageRegistries_CreateConflict(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	_, err := pg.CreateImageRegistry(ctx, api.ImageRegistryUpsert{
		Hostname:        "docker.io",
		RateLimitPerSec: 1.0,
	})
	if !errors.Is(err, api.ErrConflict) {
		t.Fatalf("expected ErrConflict for existing hostname, got %v", err)
	}
}
