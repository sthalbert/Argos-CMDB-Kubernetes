package api

// In-memory Application store methods for memStore (ADR-0029). Replaces
// the bare stubs in server_test.go so the handler tests in
// application_handlers_test.go can drive the full CRUD + members surface
// without a Postgres dependency.
//
// Mirrors server_application_block_fake_test.go: process-wide singleton
// with a reset helper called from each test's setup. The seven
// behaviours covered here:
//   - CreateApplication (with UNIQUE-name → ErrConflict; block
//     resolution by id or name; member counts default to zero)
//   - GetApplication / GetApplicationByName (case-insensitive normalised)
//   - ListApplications (filters: name substring, application_block_id,
//     application_block_name, criticality, has_dict, dict_min;
//     cursor-paginated on (created_at DESC, id DESC))
//   - UpdateApplication (merge-patch on the mutable fields; block
//     re-resolution by id or name)
//   - DeleteApplication
//   - ListApplicationMembers (always empty in Phase 1: there's no
//     handler path to link members in this phase, and the per-source
//     walk lives in the PG impl)
//
// MemberCounts on the in-memory rows is always zero — the real walk is
// store-side. Tests that care about counts will run against the PG impl
// in pg_applications_test.go.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type memApplicationState struct {
	mu     sync.Mutex
	byID   map[uuid.UUID]Application
	byName map[string]uuid.UUID // normalised name → id
}

var appFake = &memApplicationState{
	byID:   make(map[uuid.UUID]Application),
	byName: make(map[string]uuid.UUID),
}

func resetApplicationFake() {
	appFake.mu.Lock()
	defer appFake.mu.Unlock()
	appFake.byID = make(map[uuid.UUID]Application)
	appFake.byName = make(map[string]uuid.UUID)
}

// resolveBlockIDFake mirrors PG.resolveBlockID: id wins on conflict;
// (nil, nil) is the legitimate "no block" case. Returns an error when
// the supplied id or name does not match a known block.
func resolveBlockIDFake(byID *uuid.UUID, byName *string) (*uuid.UUID, error) {
	if byID != nil {
		appBlockFake.mu.Lock()
		_, ok := appBlockFake.byID[*byID]
		appBlockFake.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("application_block id %s not found", *byID)
		}
		id := *byID
		return &id, nil
	}
	if byName != nil && *byName != "" {
		norm := NormalizeApplicationName(*byName)
		appBlockFake.mu.Lock()
		id, ok := appBlockFake.byName[norm]
		appBlockFake.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("application_block name %q not found", *byName)
		}
		out := id
		return &out, nil
	}
	return nil, nil //nolint:nilnil // (nil, nil) is the documented "no block selected" signal
}

// blockNameFor returns the denormalised block name for the given block
// id (or nil if the block is missing or the id is nil). Used to fill
// the ApplicationBlockName field on returned rows (ADR-0027 pattern).
func blockNameFor(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	appBlockFake.mu.Lock()
	defer appBlockFake.mu.Unlock()
	blk, ok := appBlockFake.byID[*id]
	if !ok {
		return nil
	}
	name := blk.Name
	return &name
}

//nolint:gocritic // hugeParam: interface-mandated signature
func (m *memStore) CreateApplication(_ context.Context, in ApplicationCreate) (Application, error) {
	name := NormalizeApplicationName(in.Name)
	if name == "" {
		return Application{}, fmt.Errorf("application: name required")
	}

	blockID, err := resolveBlockIDFake(in.ApplicationBlockID, in.ApplicationBlockName)
	if err != nil {
		return Application{}, err
	}

	appFake.mu.Lock()
	defer appFake.mu.Unlock()

	if _, exists := appFake.byName[name]; exists {
		return Application{}, ErrConflict
	}

	now := time.Now().UTC()
	anns := map[string]string{}
	if in.Annotations != nil {
		anns = cloneAnnotations(*in.Annotations)
	}

	app := Application{
		ID:                   uuid.New(),
		Name:                 name,
		DisplayName:          in.DisplayName,
		Description:          in.Description,
		ApplicationBlockID:   blockID,
		ApplicationBlockName: blockNameFor(blockID),
		Owner:                in.Owner,
		Criticality:          in.Criticality,
		Notes:                in.Notes,
		RunbookURL:           in.RunbookURL,
		Annotations:          anns,
		SecDisponibilite:     in.SecDisponibilite,
		SecIntegrite:         in.SecIntegrite,
		SecConfidentialite:   in.SecConfidentialite,
		SecTracabilite:       in.SecTracabilite,
		SecNotes:             in.SecNotes,
		CreatedAt:            now,
		UpdatedAt:            now,
		MemberCounts:         ApplicationMemberCounts{},
	}
	appFake.byID[app.ID] = app
	appFake.byName[name] = app.ID
	return app, nil
}

func (m *memStore) GetApplication(_ context.Context, id uuid.UUID) (Application, error) {
	appFake.mu.Lock()
	defer appFake.mu.Unlock()
	app, ok := appFake.byID[id]
	if !ok {
		return Application{}, ErrNotFound
	}
	app.Annotations = cloneAnnotations(app.Annotations)
	// Refresh denormalised block name in case the block was renamed
	// since insert (matches the PG impl, which re-projects on every read).
	app.ApplicationBlockName = blockNameFor(app.ApplicationBlockID)
	return app, nil
}

func (m *memStore) GetApplicationsByIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]Application, error) {
	out := make(map[uuid.UUID]Application, len(ids))
	appFake.mu.Lock()
	defer appFake.mu.Unlock()
	for _, id := range ids {
		app, ok := appFake.byID[id]
		if !ok {
			continue
		}
		app.Annotations = cloneAnnotations(app.Annotations)
		app.ApplicationBlockName = blockNameFor(app.ApplicationBlockID)
		out[id] = app
	}
	return out, nil
}

func (m *memStore) DICTCoverageCounts(_ context.Context) (application, workload, none int, _ error) {
	m.mu.Lock()
	appIDs := make([]*uuid.UUID, 0, len(m.workloadsByID))
	for id := range m.workloadsByID {
		wl := m.workloadsByID[id]
		appIDs = append(appIDs, wl.ApplicationId)
	}
	m.mu.Unlock()

	appFake.mu.Lock()
	defer appFake.mu.Unlock()
	for _, appID := range appIDs {
		appHasDICT := false
		if appID != nil {
			if app, ok := appFake.byID[*appID]; ok {
				appHasDICT = anyAxisSet(app.SecDisponibilite, app.SecIntegrite, app.SecConfidentialite, app.SecTracabilite)
			}
		}
		if appHasDICT {
			application++
		} else {
			// Workloads carry no DICT columns, so the only fallback is "none".
			none++
		}
	}
	return application, workload, none, nil
}

func (m *memStore) GetApplicationByName(_ context.Context, name string) (Application, error) {
	norm := NormalizeApplicationName(name)
	if norm == "" {
		return Application{}, ErrNotFound
	}
	appFake.mu.Lock()
	defer appFake.mu.Unlock()
	id, ok := appFake.byName[norm]
	if !ok {
		return Application{}, ErrNotFound
	}
	app := appFake.byID[id]
	app.Annotations = cloneAnnotations(app.Annotations)
	app.ApplicationBlockName = blockNameFor(app.ApplicationBlockID)
	return app, nil
}

// applicationMatchesFilter returns true when the row passes every
// optional filter. Extracted from ListApplications so the dispatcher
// loop stays under the gocognit threshold. Takes *Application to avoid
// hugeParam copies — the caller already holds a map-valued copy in
// scope so pointer arithmetic is safe.
//
//nolint:gocyclo // one branch per optional filter; flattening would obscure intent
func applicationMatchesFilter(app *Application, filter ApplicationListFilter) bool {
	if filter.Name != nil && !applicationNameMatches(app, *filter.Name) {
		return false
	}
	if filter.ApplicationBlockID != nil {
		if app.ApplicationBlockID == nil || *app.ApplicationBlockID != *filter.ApplicationBlockID {
			return false
		}
	}
	if filter.ApplicationBlockName != nil && *filter.ApplicationBlockName != "" {
		if !applicationBlockNameMatches(app, *filter.ApplicationBlockName) {
			return false
		}
	}
	if filter.Criticality != nil && *filter.Criticality != "" {
		if app.Criticality == nil || *app.Criticality != *filter.Criticality {
			return false
		}
	}
	if filter.HasDICT != nil {
		if applicationHasDICT(app) != *filter.HasDICT {
			return false
		}
	}
	if filter.DICTMin != nil && applicationMaxDICTAxis(app) < *filter.DICTMin {
		return false
	}
	return true
}

func applicationNameMatches(app *Application, needle string) bool {
	needle = strings.ToLower(needle)
	if strings.Contains(strings.ToLower(app.Name), needle) {
		return true
	}
	if app.DisplayName != nil && strings.Contains(strings.ToLower(*app.DisplayName), needle) {
		return true
	}
	return false
}

func applicationBlockNameMatches(app *Application, blockName string) bool {
	wantName := NormalizeApplicationName(blockName)
	if app.ApplicationBlockID == nil {
		return false
	}
	gotName := blockNameFor(app.ApplicationBlockID)
	return gotName != nil && *gotName == wantName
}

func applicationHasDICT(app *Application) bool {
	return app.SecDisponibilite != nil || app.SecIntegrite != nil ||
		app.SecConfidentialite != nil || app.SecTracabilite != nil
}

func applicationMaxDICTAxis(app *Application) int {
	maxAxis := 0
	for _, v := range []*int{app.SecDisponibilite, app.SecIntegrite, app.SecConfidentialite, app.SecTracabilite} {
		if v != nil && *v > maxAxis {
			maxAxis = *v
		}
	}
	return maxAxis
}

//nolint:gocyclo // cursor decode + filter + sort + pagination; helper-extracted further would obscure intent
func (m *memStore) ListApplications(
	_ context.Context,
	filter ApplicationListFilter,
	limit int,
	cursor string,
) ([]Application, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	appFake.mu.Lock()
	defer appFake.mu.Unlock()

	all := make([]Application, 0, len(appFake.byID))
	for _, app := range appFake.byID { //nolint:gocritic // rangeValCopy: test fake; copy is intentional
		appCopy := app
		if applicationMatchesFilter(&appCopy, filter) {
			all = append(all, appCopy)
		}
	}

	// Order by (created_at DESC, id DESC) — matches PG impl so cursor
	// semantics are consistent.
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID.String() > all[j].ID.String()
	})

	if cursor != "" {
		ts, cid, err := decodeFakeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		filtered := all[:0]
		for _, app := range all { //nolint:gocritic // rangeValCopy: test fake; copy is intentional
			if app.CreatedAt.Before(ts) || (app.CreatedAt.Equal(ts) && app.ID.String() < cid.String()) {
				filtered = append(filtered, app)
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
	for i := range all {
		all[i].Annotations = cloneAnnotations(all[i].Annotations)
		all[i].ApplicationBlockName = blockNameFor(all[i].ApplicationBlockID)
	}
	return all, next, nil
}

//nolint:gocyclo,gocritic // wide patch surface; hugeParam: interface-mandated signature
func (m *memStore) UpdateApplication(_ context.Context, id uuid.UUID, in ApplicationPatch) (Application, error) {
	// Resolve block first so an invalid id/name surfaces before any state
	// mutation (mirrors PG impl).
	var blockID *uuid.UUID
	resolveBlock := in.ApplicationBlockID != nil || in.ApplicationBlockName != nil
	if resolveBlock {
		resolved, err := resolveBlockIDFake(in.ApplicationBlockID, in.ApplicationBlockName)
		if err != nil {
			return Application{}, err
		}
		blockID = resolved
	}

	appFake.mu.Lock()
	defer appFake.mu.Unlock()
	app, ok := appFake.byID[id]
	if !ok {
		return Application{}, ErrNotFound
	}

	if in.DisplayName != nil {
		app.DisplayName = in.DisplayName
	}
	if in.Description != nil {
		app.Description = in.Description
	}
	if resolveBlock {
		app.ApplicationBlockID = blockID
	}
	if in.Owner != nil {
		app.Owner = in.Owner
	}
	if in.Criticality != nil {
		app.Criticality = in.Criticality
	}
	if in.Notes != nil {
		app.Notes = in.Notes
	}
	if in.RunbookURL != nil {
		app.RunbookURL = in.RunbookURL
	}
	if in.Annotations != nil {
		app.Annotations = cloneAnnotations(*in.Annotations)
	}
	if in.SecDisponibilite != nil {
		app.SecDisponibilite = in.SecDisponibilite
	}
	if in.SecIntegrite != nil {
		app.SecIntegrite = in.SecIntegrite
	}
	if in.SecConfidentialite != nil {
		app.SecConfidentialite = in.SecConfidentialite
	}
	if in.SecTracabilite != nil {
		app.SecTracabilite = in.SecTracabilite
	}
	if in.SecNotes != nil {
		app.SecNotes = in.SecNotes
	}
	app.UpdatedAt = time.Now().UTC()
	app.ApplicationBlockName = blockNameFor(app.ApplicationBlockID)
	appFake.byID[id] = app
	return app, nil
}

func (m *memStore) DeleteApplication(_ context.Context, id uuid.UUID) error {
	appFake.mu.Lock()
	defer appFake.mu.Unlock()
	app, ok := appFake.byID[id]
	if !ok {
		return ErrNotFound
	}
	delete(appFake.byID, id)
	delete(appFake.byName, app.Name)
	return nil
}

// ListApplicationMembers returns an empty page in Phase 1 — there's no
// handler path to link workloads / VMs to applications until Phase 2
// (Tasks 2.1-2.5), so the fake has nothing real to walk. The PG impl
// owns the actual three-source walk and is exercised by
// pg_applications_test.go.
func (m *memStore) ListApplicationMembers(_ context.Context, _ uuid.UUID, _ int, _ string) ([]ApplicationMember, string, error) {
	return []ApplicationMember{}, "", nil
}
