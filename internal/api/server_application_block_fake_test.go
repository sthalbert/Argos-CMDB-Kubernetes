package api

// In-memory ApplicationBlock store methods for memStore (ADR-0029).
// Replaces the bare stubs in server_test.go so the handler tests in
// application_block_handlers_test.go can drive the full CRUD surface
// without a Postgres dependency.
//
// Mirrors the cloudFake pattern in server_cloud_fake_test.go: a
// process-wide singleton with a reset helper called from each test's
// setup. The struct must offer:
//   - CreateApplicationBlock (with UNIQUE-name → ErrConflict)
//   - GetApplicationBlock by id
//   - GetApplicationBlockByName (case-insensitive, normalised)
//   - ListApplicationBlocks (filters: name substring case-insensitive,
//     owner exact; cursor-paginated on insertion order)
//   - UpdateApplicationBlock (merge-patch on the mutable fields)
//   - DeleteApplicationBlock
//
// Behaviour matches the canonical *store.PG impl in
// pg_application_blocks.go closely enough to keep the handler tests
// realistic without re-implementing the SQL.

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type memApplicationBlockState struct {
	mu     sync.Mutex
	byID   map[uuid.UUID]ApplicationBlock
	byName map[string]uuid.UUID // normalised name → id
}

var appBlockFake = &memApplicationBlockState{
	byID:   make(map[uuid.UUID]ApplicationBlock),
	byName: make(map[string]uuid.UUID),
}

func resetApplicationBlockFake() {
	appBlockFake.mu.Lock()
	defer appBlockFake.mu.Unlock()
	appBlockFake.byID = make(map[uuid.UUID]ApplicationBlock)
	appBlockFake.byName = make(map[string]uuid.UUID)
}

func cloneAnnotations(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (m *memStore) CreateApplicationBlock(_ context.Context, in ApplicationBlockCreate) (ApplicationBlock, error) {
	name := NormalizeApplicationName(in.Name)
	if name == "" {
		return ApplicationBlock{}, fmt.Errorf("application_block: name required")
	}
	appBlockFake.mu.Lock()
	defer appBlockFake.mu.Unlock()

	if _, exists := appBlockFake.byName[name]; exists {
		return ApplicationBlock{}, ErrConflict
	}
	now := time.Now().UTC()
	anns := map[string]string{}
	if in.Annotations != nil {
		anns = cloneAnnotations(*in.Annotations)
	}
	blk := ApplicationBlock{
		ID:          uuid.New(),
		Name:        name,
		DisplayName: in.DisplayName,
		Description: in.Description,
		Owner:       in.Owner,
		Notes:       in.Notes,
		Annotations: anns,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	appBlockFake.byID[blk.ID] = blk
	appBlockFake.byName[name] = blk.ID
	return blk, nil
}

func (m *memStore) GetApplicationBlock(_ context.Context, id uuid.UUID) (ApplicationBlock, error) {
	appBlockFake.mu.Lock()
	defer appBlockFake.mu.Unlock()
	blk, ok := appBlockFake.byID[id]
	if !ok {
		return ApplicationBlock{}, ErrNotFound
	}
	// Defensive copy of the annotations map.
	blk.Annotations = cloneAnnotations(blk.Annotations)
	return blk, nil
}

func (m *memStore) GetApplicationBlockByName(_ context.Context, name string) (ApplicationBlock, error) {
	norm := NormalizeApplicationName(name)
	if norm == "" {
		return ApplicationBlock{}, ErrNotFound
	}
	appBlockFake.mu.Lock()
	defer appBlockFake.mu.Unlock()
	id, ok := appBlockFake.byName[norm]
	if !ok {
		return ApplicationBlock{}, ErrNotFound
	}
	blk := appBlockFake.byID[id]
	blk.Annotations = cloneAnnotations(blk.Annotations)
	return blk, nil
}

//nolint:gocyclo // test fake mirrors PG impl: optional filters + cursor branch
func (m *memStore) ListApplicationBlocks(
	_ context.Context,
	filter ApplicationBlockListFilter,
	limit int,
	cursor string,
) ([]ApplicationBlock, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	appBlockFake.mu.Lock()
	defer appBlockFake.mu.Unlock()

	// Collect, then filter.
	all := make([]ApplicationBlock, 0, len(appBlockFake.byID))
	for _, blk := range appBlockFake.byID { //nolint:gocritic // rangeValCopy: test fake; copy is intentional

		if filter.Name != nil {
			needle := strings.ToLower(*filter.Name)
			if !strings.Contains(strings.ToLower(blk.Name), needle) {
				continue
			}
		}
		if filter.Owner != nil {
			if blk.Owner == nil || *blk.Owner != *filter.Owner {
				continue
			}
		}
		all = append(all, blk)
	}

	// Order by (created_at DESC, id DESC) — matches the PG impl so
	// cursor semantics are consistent.
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID.String() > all[j].ID.String()
	})

	// Apply opaque cursor: "RFC3339Nano|uuid", base64-encoded. We
	// re-implement here (rather than importing the store helper) to keep
	// internal/api independent of internal/store; only the format needs
	// to match what the handler emits, and the handler delegates to the
	// store at runtime — the fake just needs to be internally consistent.
	if cursor != "" {
		ts, cid, err := decodeFakeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		filtered := all[:0]
		for _, blk := range all { //nolint:gocritic // rangeValCopy: test fake; copy is intentional

			if blk.CreatedAt.Before(ts) || (blk.CreatedAt.Equal(ts) && blk.ID.String() < cid.String()) {
				filtered = append(filtered, blk)
			}
		}
		all = filtered
	}

	var next string
	if len(all) > limit {
		last := all[limit-1]
		next = encodeFakeCursor(last.CreatedAt, last.ID)
		all = all[:limit]
	}
	// Defensive copy of annotations on each row.
	for i := range all {
		all[i].Annotations = cloneAnnotations(all[i].Annotations)
	}
	return all, next, nil
}

func (m *memStore) UpdateApplicationBlock(_ context.Context, id uuid.UUID, in ApplicationBlockPatch) (ApplicationBlock, error) {
	appBlockFake.mu.Lock()
	defer appBlockFake.mu.Unlock()
	blk, ok := appBlockFake.byID[id]
	if !ok {
		return ApplicationBlock{}, ErrNotFound
	}
	if in.DisplayName != nil {
		blk.DisplayName = in.DisplayName
	}
	if in.Description != nil {
		blk.Description = in.Description
	}
	if in.Owner != nil {
		blk.Owner = in.Owner
	}
	if in.Notes != nil {
		blk.Notes = in.Notes
	}
	if in.Annotations != nil {
		blk.Annotations = cloneAnnotations(*in.Annotations)
	}
	blk.UpdatedAt = time.Now().UTC()
	appBlockFake.byID[id] = blk
	return blk, nil
}

func (m *memStore) DeleteApplicationBlock(_ context.Context, id uuid.UUID) error {
	appBlockFake.mu.Lock()
	defer appBlockFake.mu.Unlock()
	blk, ok := appBlockFake.byID[id]
	if !ok {
		return ErrNotFound
	}
	delete(appBlockFake.byID, id)
	delete(appBlockFake.byName, blk.Name)
	return nil
}

// encodeFakeCursor / decodeFakeCursor mirror the wire format used by
// internal/store/pg.go (base64 of "<RFC3339Nano>|<uuid>"). Keeping them
// here avoids an internal/api → internal/store dependency.
func encodeFakeCursor(ts time.Time, id uuid.UUID) string {
	raw := fmt.Sprintf("%s|%s", ts.UTC().Format(time.RFC3339Nano), id)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeFakeCursor(cursor string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("decode cursor: %w", err)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, fmt.Errorf("malformed cursor")
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("decode cursor timestamp: %w", err)
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("decode cursor id: %w", err)
	}
	return ts, id, nil
}
