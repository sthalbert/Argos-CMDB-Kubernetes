package api

// Concurrent last-admin guard regression tests for AUTHZ-VULN-01 / -02
// (audit finding H1). The pre-fix implementation read CountActiveAdmins
// then issued the UPDATE/DELETE in two separate database round-trips,
// allowing two simultaneous demote/disable/delete operations to both
// pass the guard and leave the deployment with zero active admins. The
// transactional `*Guarded` Store methods exercised here close that race.

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/auth"
)

// TestUpdateUserGuarded_LastAdminReturnsSentinel locks down the Store
// contract: demoting the only active admin via the guarded path returns
// ErrLastAdmin. Without the sentinel the handler cannot tell apart a
// "not found" from a "would orphan the deployment" outcome.
func TestUpdateUserGuarded_LastAdminReturnsSentinel(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	id := seedAdmin(t, m, "alice", false)

	role := auth.RoleViewer
	_, err := m.UpdateUserGuarded(t.Context(), id, UserPatch{Role: &role})
	if !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin, got %v", err)
	}
}

func TestUpdateUserGuarded_DisableLastAdminReturnsSentinel(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	id := seedAdmin(t, m, "alice", false)

	disabled := true
	_, err := m.UpdateUserGuarded(t.Context(), id, UserPatch{Disabled: &disabled})
	if !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin, got %v", err)
	}
}

func TestUpdateUserGuarded_AllowedWhenAnotherAdminActive(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	target := seedAdmin(t, m, "alice", false)
	_ = seedAdmin(t, m, "bob", false)

	role := auth.RoleViewer
	u, err := m.UpdateUserGuarded(t.Context(), target, UserPatch{Role: &role})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if u.Role != Role(auth.RoleViewer) {
		t.Fatalf("expected role viewer, got %s", u.Role)
	}
}

func TestDeleteUserGuarded_LastAdminReturnsSentinel(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	id := seedAdmin(t, m, "alice", false)

	err := m.DeleteUserGuarded(t.Context(), id)
	if !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("expected ErrLastAdmin, got %v", err)
	}
}

func TestDeleteUserGuarded_AllowedForDisabledAdmin(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	_ = seedAdmin(t, m, "alice", false)
	target := seedAdmin(t, m, "bob", true) // disabled — not counted

	if err := m.DeleteUserGuarded(t.Context(), target); err != nil {
		t.Fatalf("unexpected error deleting disabled admin: %v", err)
	}
}

// TestUpdateUserGuarded_NeverLeavesZeroAdmins is the pentest reproducer
// for H1. With two active admins and N concurrent demotion goroutines,
// the post-fix behaviour must converge to *exactly one* active admin —
// the second demotion must trip ErrLastAdmin once the first commits.
// The pre-fix code occasionally produced zero active admins, leaving
// the deployment unmanageable.
func TestUpdateUserGuarded_NeverLeavesZeroAdmins(t *testing.T) {
	t.Parallel()
	const goroutines = 32

	for trial := range 50 {
		m := newMemStore()
		a := seedAdmin(t, m, "alice", false)
		b := seedAdmin(t, m, "bob", false)

		role := auth.RoleViewer
		patch := UserPatch{Role: &role}

		var (
			wg     sync.WaitGroup
			start  = make(chan struct{})
			ctx    = context.Background()
			errsMu sync.Mutex
			errs   []error
		)
		wg.Add(goroutines)
		for i := range goroutines {
			target := a
			if i%2 == 1 {
				target = b
			}
			go func(tgt uuid.UUID) {
				defer wg.Done()
				<-start
				_, err := m.UpdateUserGuarded(ctx, tgt, patch)
				errsMu.Lock()
				errs = append(errs, err)
				errsMu.Unlock()
			}(target)
		}
		close(start)
		wg.Wait()

		n, _ := m.CountActiveAdmins(ctx)
		if n < 1 {
			t.Fatalf("trial %d: ZERO active admins remain — TOCTOU race not closed (errs=%v)", trial, errs)
		}
	}
}
