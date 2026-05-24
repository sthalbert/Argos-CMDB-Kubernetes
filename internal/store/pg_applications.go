package store

// Stub implementations for the Application methods on the api.Store
// interface (ADR-0029). Task 1.5 introduced the interface; Task 1.6
// replaced the ApplicationBlock stubs with real PostgreSQL bodies (see
// pg_application_blocks.go). Task 1.7 will replace the Application
// stubs below.

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// errNotImplemented mirrors the memStore convention for write-path stubs
// that have not yet acquired a real PostgreSQL body.
var errNotImplemented = errors.New("store: not implemented")

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
