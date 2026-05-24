package api

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// TestResolveApplicationID exercises every (id, name) precedence branch and
// pins the ADR-0029 §2.3 contract that id wins on conflict. Drives the
// in-memory fake (server_application_fake_test.go) so it runs without a
// Postgres dependency.
//
//nolint:gocyclo // table-driven precedence matrix; branches are inherent to the contract under test
func TestResolveApplicationID(t *testing.T) {
	resetApplicationFake()

	ms := newMemStore()
	ctx := context.Background()

	good, err := ms.CreateApplication(ctx, ApplicationCreate{Name: "Billing"})
	if err != nil {
		t.Fatalf("seed application: %v", err)
	}
	// good.Name has been normalised to "billing".

	other, err := ms.CreateApplication(ctx, ApplicationCreate{Name: "Checkout"})
	if err != nil {
		t.Fatalf("seed other application: %v", err)
	}

	missingID := uuid.New()
	missingName := "no-such-app"
	emptyName := ""

	otherName := "checkout"

	tests := []struct {
		name        string
		id          *uuid.UUID
		nameArg     *string
		wantID      *uuid.UUID
		wantErr     bool
		wantErrIs   error
		description string
	}{
		{
			name:        "both nil → no link, no error",
			id:          nil,
			nameArg:     nil,
			wantID:      nil,
			description: "documented (nil, nil) case so callers can treat 'neither provided' as 'no change'",
		},
		{
			name:        "id only, valid → returns id",
			id:          &good.ID,
			nameArg:     nil,
			wantID:      &good.ID,
			description: "happy path for the id-first surface",
		},
		{
			name:        "id only, missing → ErrNotFound",
			id:          &missingID,
			nameArg:     nil,
			wantErr:     true,
			wantErrIs:   ErrNotFound,
			description: "callers map this to RFC 7807 400",
		},
		{
			name:        "name only, valid → resolves to id",
			id:          nil,
			nameArg:     &good.Name,
			wantID:      &good.ID,
			description: "happy path for the convenience surface",
		},
		{
			name:        "name only, missing → ErrNotFound",
			id:          nil,
			nameArg:     &missingName,
			wantErr:     true,
			wantErrIs:   ErrNotFound,
			description: "404-ish error; handlers map to 400 because the input is operator-supplied",
		},
		{
			name:        "name only, empty string → (nil, nil)",
			id:          nil,
			nameArg:     &emptyName,
			wantID:      nil,
			description: "empty-string convenience: treated like 'unset' so a UI clearing a textbox doesn't 400",
		},
		{
			name:        "both set → id wins (mirrors ADR-0019 precedence)",
			id:          &good.ID,
			nameArg:     &otherName, // points at a different real application
			wantID:      &good.ID,
			description: "pins the precedence test the ADR calls out explicitly",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveApplicationID(ctx, ms, tc.id, tc.nameArg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (resolved to %v)", got)
				}
				if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("expected errors.Is(err, %v); got %v", tc.wantErrIs, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			switch {
			case tc.wantID == nil && got != nil:
				t.Fatalf("expected nil id, got %v", got)
			case tc.wantID != nil && got == nil:
				t.Fatalf("expected id %v, got nil", tc.wantID)
			case tc.wantID != nil && *got != *tc.wantID:
				t.Fatalf("expected id %v, got %v", *tc.wantID, *got)
			}
		})
	}

	_ = other // silence linter; row is seeded to make the "id wins" case meaningful
}
