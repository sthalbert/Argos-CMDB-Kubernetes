package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ResolveApplicationID picks the application id from (id, name).
//
// Precedence (ADR-0029 §2.3): id wins when both are set; mirrors the
// cloud_account_id / cloud_account_name precedence from ADR-0019.
//
// Returns:
//   - (id, nil) when id is non-nil and the row exists.
//   - (&app.ID, nil) when only name is non-nil/non-empty and resolves.
//   - (nil, nil) when neither is provided (caller treats as "no change").
//   - (nil, error) on lookup failures, including a 400-friendly "not found"
//     error wrapped around api.ErrNotFound so callers can map to RFC 7807.
//
// The helper is shared by the workload and virtual-machine PATCH handlers
// (Task 2.2 / 2.3) and by any future surface that accepts the same
// {id, name} pair.
func ResolveApplicationID(
	ctx context.Context,
	store Store,
	id *uuid.UUID,
	name *string,
) (*uuid.UUID, error) {
	if id != nil {
		if _, err := store.GetApplication(ctx, *id); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("application_id %s not found: %w", *id, ErrNotFound)
			}
			return nil, fmt.Errorf("get application by id: %w", err)
		}
		return id, nil
	}
	if name != nil && *name != "" {
		app, err := store.GetApplicationByName(ctx, *name)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("application name %q not found: %w", *name, ErrNotFound)
			}
			return nil, fmt.Errorf("get application by name: %w", err)
		}
		return &app.ID, nil
	}
	return nil, nil //nolint:nilnil // (nil, nil) is the documented "neither field provided" signal so callers can treat it as "no change"
}
