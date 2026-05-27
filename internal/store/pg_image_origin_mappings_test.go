package store

import (
	"context"
	"errors"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestPG_FindImageOrigin(t *testing.T) {
	ctx := context.Background()
	pg := newTestPG(t)

	// Seed a mapping directly via the store layer once Create is wired
	// up; until then, insert via raw SQL to keep this test independent.
	_, err := pg.pool.Exec(ctx,
		`INSERT INTO image_origin_mappings (image_name, public_registry)
		 VALUES ('grafana/alloy', 'docker.io')`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Run("hit", func(t *testing.T) {
		got, err := pg.FindImageOrigin(ctx, "grafana/alloy")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got != "docker.io" {
			t.Fatalf("want docker.io, got %q", got)
		}
	})

	t.Run("miss", func(t *testing.T) {
		_, err := pg.FindImageOrigin(ctx, "does/not/exist")
		if !errors.Is(err, api.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})
}

//nolint:gocyclo // table-driven test with many subtests; each branch is trivial
func TestPG_CreateAndGetImageOriginMapping(t *testing.T) {
	ctx := context.Background()
	pg := newTestPG(t)

	in := api.ImageOriginMappingCreate{
		ImageName:      "grafana/alloy",
		PublicRegistry: "docker.io",
	}
	created, err := pg.CreateImageOriginMapping(ctx, in, "user-alice")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ImageName != "grafana/alloy" || created.PublicRegistry != "docker.io" {
		t.Fatalf("unexpected created row: %+v", created)
	}
	if created.CreatedBy == nil || *created.CreatedBy != "user-alice" {
		t.Fatalf("created_by not stamped: %+v", created.CreatedBy)
	}
	if created.CreatedAt.IsZero() {
		t.Fatalf("created_at not stamped")
	}

	got, err := pg.GetImageOriginMapping(ctx, "grafana/alloy")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ImageName != created.ImageName {
		t.Fatalf("get mismatch: %+v", got)
	}

	// Duplicate insert → ErrConflict.
	if _, err := pg.CreateImageOriginMapping(ctx, in, "user-bob"); !errors.Is(err, api.ErrConflict) {
		t.Fatalf("want ErrConflict on duplicate, got %v", err)
	}

	// Get on missing → ErrNotFound.
	if _, err := pg.GetImageOriginMapping(ctx, "does/not/exist"); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

//nolint:gocyclo // table-driven test with many subtests; each branch is trivial
func TestPG_PatchAndDeleteImageOriginMapping(t *testing.T) {
	ctx := context.Background()
	pg := newTestPG(t)

	_, err := pg.CreateImageOriginMapping(ctx, api.ImageOriginMappingCreate{
		ImageName: "grafana/alloy", PublicRegistry: "docker.io",
	}, "user-alice")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	t.Run("patch registry only", func(t *testing.T) {
		newReg := "ghcr.io"
		out, err := pg.PatchImageOriginMapping(ctx, "grafana/alloy",
			api.ImageOriginMappingPatch{PublicRegistry: &newReg}, "user-bob")
		if err != nil {
			t.Fatalf("patch: %v", err)
		}
		if out.PublicRegistry != "ghcr.io" {
			t.Fatalf("want ghcr.io, got %q", out.PublicRegistry)
		}
		if out.UpdatedBy == nil || *out.UpdatedBy != "user-bob" {
			t.Fatalf("updated_by not stamped: %+v", out.UpdatedBy)
		}
	})

	t.Run("patch notes set then clear", func(t *testing.T) {
		setNotes := "owned by platform team"
		out, err := pg.PatchImageOriginMapping(ctx, "grafana/alloy",
			api.ImageOriginMappingPatch{Notes: &setNotes}, "user-bob")
		if err != nil {
			t.Fatalf("patch set notes: %v", err)
		}
		if out.Notes == nil || *out.Notes != "owned by platform team" {
			t.Fatalf("notes not set: %+v", out.Notes)
		}
		clearNotes := ""
		out, err = pg.PatchImageOriginMapping(ctx, "grafana/alloy",
			api.ImageOriginMappingPatch{Notes: &clearNotes}, "user-bob")
		if err != nil {
			t.Fatalf("patch clear notes: %v", err)
		}
		if out.Notes != nil {
			t.Fatalf("want notes cleared, got %+v", out.Notes)
		}
	})

	t.Run("patch missing → ErrNotFound", func(t *testing.T) {
		newReg := "ghcr.io"
		_, err := pg.PatchImageOriginMapping(ctx, "does/not/exist",
			api.ImageOriginMappingPatch{PublicRegistry: &newReg}, "user-bob")
		if !errors.Is(err, api.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		if err := pg.DeleteImageOriginMapping(ctx, "grafana/alloy"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := pg.GetImageOriginMapping(ctx, "grafana/alloy"); !errors.Is(err, api.ErrNotFound) {
			t.Fatalf("want ErrNotFound after delete, got %v", err)
		}
		if err := pg.DeleteImageOriginMapping(ctx, "grafana/alloy"); !errors.Is(err, api.ErrNotFound) {
			t.Fatalf("want ErrNotFound on second delete, got %v", err)
		}
	})
}

//nolint:gocognit,gocyclo // table-driven test with multiple subtests; each branch is trivial
func TestPG_ListImageOriginMappings(t *testing.T) {
	ctx := context.Background()
	pg := newTestPG(t)

	seed := []struct {
		name string
		reg  string
	}{
		{"grafana/alloy", "docker.io"},
		{"grafana/mimir", "docker.io"},
		{"prometheus/prometheus", "quay.io"},
		{"fluxcd/source-controller", "ghcr.io"},
	}
	for _, s := range seed {
		if _, err := pg.CreateImageOriginMapping(ctx,
			api.ImageOriginMappingCreate{ImageName: s.name, PublicRegistry: s.reg},
			"seed"); err != nil {
			t.Fatalf("seed %s: %v", s.name, err)
		}
	}

	t.Run("no filter", func(t *testing.T) {
		items, _, err := pg.ListImageOriginMappings(ctx, api.StoreListImageOriginMappingsParams{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(items) != 4 {
			t.Fatalf("want 4, got %d", len(items))
		}
		// Default sort = image_name ASC.
		if items[0].ImageName != "fluxcd/source-controller" {
			t.Fatalf("want fluxcd first, got %s", items[0].ImageName)
		}
	})

	t.Run("filter by registry", func(t *testing.T) {
		items, _, err := pg.ListImageOriginMappings(ctx,
			api.StoreListImageOriginMappingsParams{PublicRegistry: "docker.io"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("want 2, got %d", len(items))
		}
	})

	t.Run("filter by q substring", func(t *testing.T) {
		items, _, err := pg.ListImageOriginMappings(ctx,
			api.StoreListImageOriginMappingsParams{Q: "grafana"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(items) != 2 {
			t.Fatalf("want 2 grafana, got %d", len(items))
		}
	})

	t.Run("pagination", func(t *testing.T) {
		page1, cursor, err := pg.ListImageOriginMappings(ctx,
			api.StoreListImageOriginMappingsParams{Limit: 2})
		if err != nil {
			t.Fatalf("page1: %v", err)
		}
		if len(page1) != 2 || cursor == "" {
			t.Fatalf("want 2 + non-empty cursor, got %d / %q", len(page1), cursor)
		}
		page2, cursor2, err := pg.ListImageOriginMappings(ctx,
			api.StoreListImageOriginMappingsParams{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("page2: %v", err)
		}
		if len(page2) != 2 || cursor2 != "" {
			t.Fatalf("want last page (2, no cursor), got %d / %q", len(page2), cursor2)
		}
		// No overlap between pages.
		seen := map[string]bool{}
		for _, m := range append(page1, page2...) {
			if seen[m.ImageName] {
				t.Fatalf("duplicate row across pages: %s", m.ImageName)
			}
			seen[m.ImageName] = true
		}
	})
}
