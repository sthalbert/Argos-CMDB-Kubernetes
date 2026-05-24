package store

// Stub implementations for the Application and ApplicationBlock methods on
// the api.Store interface (ADR-0029). Task 1.5 extends the interface; the
// real PostgreSQL bodies land in Tasks 1.6 (application_blocks) and 1.7
// (applications + members). Stubs return zero values + sentinel errors so
// the build stays green and the rest of Phase 1 can land incrementally.

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// errNotImplemented mirrors the memStore convention for write-path stubs
// that have not yet acquired a real PostgreSQL body.
var errNotImplemented = errors.New("store: not implemented")

// --- ApplicationBlock stubs (Task 1.6 fills these in) -------------------

// CreateApplicationBlock is a stub; real body lands in Task 1.6.
func (p *PG) CreateApplicationBlock(_ context.Context, _ api.ApplicationBlockCreate) (api.ApplicationBlock, error) {
	return api.ApplicationBlock{}, errNotImplemented
}

// GetApplicationBlock is a stub; real body lands in Task 1.6.
func (p *PG) GetApplicationBlock(_ context.Context, _ uuid.UUID) (api.ApplicationBlock, error) {
	return api.ApplicationBlock{}, api.ErrNotFound
}

// GetApplicationBlockByName is a stub; real body lands in Task 1.6.
func (p *PG) GetApplicationBlockByName(_ context.Context, _ string) (api.ApplicationBlock, error) {
	return api.ApplicationBlock{}, api.ErrNotFound
}

// ListApplicationBlocks is a stub; real body lands in Task 1.6.
func (p *PG) ListApplicationBlocks(_ context.Context, _ api.ApplicationBlockListFilter, _ int, _ string) ([]api.ApplicationBlock, string, error) {
	return nil, "", nil
}

// UpdateApplicationBlock is a stub; real body lands in Task 1.6.
func (p *PG) UpdateApplicationBlock(_ context.Context, _ uuid.UUID, _ api.ApplicationBlockPatch) (api.ApplicationBlock, error) {
	return api.ApplicationBlock{}, api.ErrNotFound
}

// DeleteApplicationBlock is a stub; real body lands in Task 1.6.
func (p *PG) DeleteApplicationBlock(_ context.Context, _ uuid.UUID) error {
	return api.ErrNotFound
}

// --- Application stubs (Task 1.7 fills these in) ------------------------

// CreateApplication is a stub; real body lands in Task 1.7.
//
//nolint:gocritic // interface-mandated signature
func (p *PG) CreateApplication(_ context.Context, _ api.ApplicationCreate) (api.Application, error) {
	return api.Application{}, errNotImplemented
}

// GetApplication is a stub; real body lands in Task 1.7.
func (p *PG) GetApplication(_ context.Context, _ uuid.UUID) (api.Application, error) {
	return api.Application{}, api.ErrNotFound
}

// GetApplicationByName is a stub; real body lands in Task 1.7.
func (p *PG) GetApplicationByName(_ context.Context, _ string) (api.Application, error) {
	return api.Application{}, api.ErrNotFound
}

// ListApplications is a stub; real body lands in Task 1.7.
func (p *PG) ListApplications(_ context.Context, _ api.ApplicationListFilter, _ int, _ string) ([]api.Application, string, error) {
	return nil, "", nil
}

// UpdateApplication is a stub; real body lands in Task 1.7.
//
//nolint:gocritic // interface-mandated signature
func (p *PG) UpdateApplication(_ context.Context, _ uuid.UUID, _ api.ApplicationPatch) (api.Application, error) {
	return api.Application{}, api.ErrNotFound
}

// DeleteApplication is a stub; real body lands in Task 1.7.
func (p *PG) DeleteApplication(_ context.Context, _ uuid.UUID) error {
	return api.ErrNotFound
}

// ListApplicationMembers is a stub; real body lands in Task 1.7.
func (p *PG) ListApplicationMembers(_ context.Context, _ uuid.UUID, _ int, _ string) ([]api.ApplicationMember, string, error) {
	return nil, "", nil
}
