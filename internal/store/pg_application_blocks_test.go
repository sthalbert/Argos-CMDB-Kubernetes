package store

// PostgreSQL integration tests for the application_blocks CRUD methods
// (ADR-0029, Task 1.6). Gated on PGX_TEST_DATABASE like the rest of
// internal/store; newTestPG TRUNCATEs application_blocks between tests.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// TestPGApplicationBlockCRUD walks the happy path: create, get-by-id,
// get-by-name, update notes, list, delete, and a follow-up get returns
// ErrNotFound.
//
//nolint:gocyclo // integration test exercises multiple CRUD branches
func TestPGApplicationBlockCRUD(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	owner := "team-platform"
	annotations := map[string]string{"snc": "true"}
	created, err := pg.CreateApplicationBlock(ctx, api.ApplicationBlockCreate{
		Name:        "billing-platform",
		DisplayName: strPtr("Billing Platform"),
		Description: strPtr("Subscriber billing services"),
		Owner:       &owner,
		Annotations: &annotations,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("created.ID is zero")
	}
	if created.Name != "billing-platform" {
		t.Errorf("name = %q, want %q", created.Name, "billing-platform")
	}
	if created.DisplayName == nil || *created.DisplayName != "Billing Platform" {
		t.Errorf("display_name = %v", created.DisplayName)
	}
	if created.Annotations == nil || created.Annotations["snc"] != "true" {
		t.Errorf("annotations = %v", created.Annotations)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Errorf("timestamps not set: %+v", created)
	}

	got, err := pg.GetApplicationBlock(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.ID != created.ID || got.Name != created.Name {
		t.Errorf("get-by-id mismatch: %+v vs %+v", got, created)
	}

	byName, err := pg.GetApplicationBlockByName(ctx, "billing-platform")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if byName.ID != created.ID {
		t.Errorf("get-by-name returned wrong row: got %s, want %s", byName.ID, created.ID)
	}

	// Get-by-name normalises the input.
	byNameRaw, err := pg.GetApplicationBlockByName(ctx, "Billing Platform")
	if err != nil {
		t.Fatalf("get by name (denormalised input): %v", err)
	}
	if byNameRaw.ID != created.ID {
		t.Errorf("normalised get-by-name returned wrong row: got %s, want %s", byNameRaw.ID, created.ID)
	}

	notes := "On call: page #ops-billing"
	patched, err := pg.UpdateApplicationBlock(ctx, created.ID, api.ApplicationBlockPatch{
		Notes: &notes,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if patched.Notes == nil || *patched.Notes != notes {
		t.Errorf("notes after update = %v, want %q", patched.Notes, notes)
	}
	if !patched.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("updated_at should advance: %v vs %v", patched.UpdatedAt, created.UpdatedAt)
	}
	// Untouched fields preserved.
	if patched.DisplayName == nil || *patched.DisplayName != "Billing Platform" {
		t.Errorf("display_name should be preserved across patch: %v", patched.DisplayName)
	}
	if patched.Annotations["snc"] != "true" {
		t.Errorf("annotations should be preserved across patch: %v", patched.Annotations)
	}

	items, next, err := pg.ListApplicationBlocks(ctx, api.ApplicationBlockListFilter{}, 50, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("list len=%d, want 1", len(items))
	}
	if next != "" {
		t.Errorf("next cursor should be empty for single-row list, got %q", next)
	}

	if err := pg.DeleteApplicationBlock(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := pg.DeleteApplicationBlock(ctx, created.ID); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("second delete should be ErrNotFound, got %v", err)
	}
	if _, err := pg.GetApplicationBlock(ctx, created.ID); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("get after delete should be ErrNotFound, got %v", err)
	}
}

// TestPGApplicationBlockDuplicateName proves the UNIQUE (name) index
// surfaces as api.ErrConflict, and that the normalisation step makes
// (`"Office Apps"`, `"office-apps"`) collide.
func TestPGApplicationBlockDuplicateName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	if _, err := pg.CreateApplicationBlock(ctx, api.ApplicationBlockCreate{
		Name: "office-apps",
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := pg.CreateApplicationBlock(ctx, api.ApplicationBlockCreate{
		Name: "office-apps",
	})
	if !errors.Is(err, api.ErrConflict) {
		t.Errorf("duplicate exact-name create should be ErrConflict, got %v", err)
	}

	// Normalisation: "Office Apps" should also collide with "office-apps".
	_, err = pg.CreateApplicationBlock(ctx, api.ApplicationBlockCreate{
		Name: "Office Apps",
	})
	if !errors.Is(err, api.ErrConflict) {
		t.Errorf("duplicate normalised-name create should be ErrConflict, got %v", err)
	}
}

// TestPGApplicationBlockListFilter exercises the (name substring, owner)
// filters and confirms case-insensitive matching on name.
//
//nolint:gocyclo // integration test exercises multiple list paths
func TestPGApplicationBlockListFilter(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	teamA := "team-a"
	teamB := "team-b"
	seeds := []api.ApplicationBlockCreate{
		{Name: "billing-platform", Owner: &teamA},
		{Name: "payments-core", Owner: &teamA},
		{Name: "marketing-tools", Owner: &teamB},
	}
	for _, s := range seeds {
		if _, err := pg.CreateApplicationBlock(ctx, s); err != nil {
			t.Fatalf("seed %s: %v", s.Name, err)
		}
	}

	// Name substring filter (case-insensitive).
	nameQ := "PaY" // matches "payments-core"
	items, _, err := pg.ListApplicationBlocks(ctx, api.ApplicationBlockListFilter{Name: &nameQ}, 50, "")
	if err != nil {
		t.Fatalf("list by name: %v", err)
	}
	if len(items) != 1 || items[0].Name != "payments-core" {
		t.Errorf("name filter returned %+v", items)
	}

	// Owner exact filter.
	items, _, err = pg.ListApplicationBlocks(ctx, api.ApplicationBlockListFilter{Owner: &teamA}, 50, "")
	if err != nil {
		t.Fatalf("list by owner: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("owner filter returned %d rows, want 2", len(items))
	}
	for _, it := range items {
		if it.Owner == nil || *it.Owner != teamA {
			t.Errorf("owner filter returned wrong owner: %+v", it)
		}
	}

	// Name + owner together.
	billing := "billing"
	items, _, err = pg.ListApplicationBlocks(ctx, api.ApplicationBlockListFilter{
		Name:  &billing,
		Owner: &teamA,
	}, 50, "")
	if err != nil {
		t.Fatalf("list by name+owner: %v", err)
	}
	if len(items) != 1 || items[0].Name != "billing-platform" {
		t.Errorf("name+owner filter returned %+v", items)
	}

	// No-match returns empty, not error.
	missing := "nonexistent-xyz"
	items, _, err = pg.ListApplicationBlocks(ctx, api.ApplicationBlockListFilter{Name: &missing}, 50, "")
	if err != nil {
		t.Fatalf("list no-match: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("no-match filter returned %+v", items)
	}
}

// TestPGApplicationBlockUpdateUnknown verifies UpdateApplicationBlock on
// a random UUID returns api.ErrNotFound rather than a silent zero-row.
func TestPGApplicationBlockUpdateUnknown(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	notes := "owner notes"
	_, err := pg.UpdateApplicationBlock(ctx, uuid.New(), api.ApplicationBlockPatch{
		Notes: &notes,
	})
	if !errors.Is(err, api.ErrNotFound) {
		t.Errorf("update unknown id should be ErrNotFound, got %v", err)
	}
}

// TestPGApplicationBlockNormalisesNameOnCreate verifies that the input
// name is run through api.NormalizeApplicationName before insertion, so
// stored names follow the kebab-case convention.
func TestPGApplicationBlockNormalisesNameOnCreate(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	created, err := pg.CreateApplicationBlock(ctx, api.ApplicationBlockCreate{
		Name: "Office Apps",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const want = "office-apps"
	if created.Name != want {
		t.Errorf("stored name = %q, want %q", created.Name, want)
	}
	// Verify the row in the DB has the normalised value, not the input.
	var dbName string
	if err := pg.pool.QueryRow(ctx,
		"SELECT name FROM application_blocks WHERE id=$1", created.ID,
	).Scan(&dbName); err != nil {
		t.Fatalf("query db: %v", err)
	}
	if dbName != want {
		t.Errorf("db name = %q, want %q", dbName, want)
	}
}

// TestPGApplicationBlockEmptyNameRejected verifies the store rejects an
// empty or whitespace-only name (normalisation collapses to empty).
func TestPGApplicationBlockEmptyNameRejected(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	for _, name := range []string{"", "   ", "\t"} {
		_, err := pg.CreateApplicationBlock(ctx, api.ApplicationBlockCreate{Name: name})
		if err == nil {
			t.Errorf("empty name %q should error, got nil", name)
			continue
		}
		// Use simple substring check rather than asserting a particular
		// sentinel — the store returns a fmt.Errorf("name required").
		if !strings.Contains(err.Error(), "name") {
			t.Errorf("empty name %q: error %q should mention 'name'", name, err)
		}
	}
}
