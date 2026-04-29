package api

// Last-admin invariant guard for the admin-user lifecycle endpoints.
// Pentest finding AUTHZ-VULN-01 / -02: an authenticated admin could
// delete every other admin or demote the only remaining admin, leaving
// the deployment with no humans able to manage it. The guards added
// here refuse the operation with 409 when the change would leave zero
// active admins.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/auth"
)

// seedAdmin inserts a fresh admin user via the memStore's CreateUser and
// returns the new ID. Test-helper to keep the table-driven cases readable.
func seedAdmin(t *testing.T, m *memStore, username string, disabled bool) uuid.UUID {
	t.Helper()
	u, err := m.CreateUser(t.Context(), UserInsert{
		Username:     username,
		Role:         auth.RoleAdmin,
		PasswordHash: "$argon2id$placeholder",
	})
	if err != nil {
		t.Fatalf("seedAdmin %q: %v", username, err)
	}
	if disabled {
		now := time.Now().UTC()
		stored := m.authState.users[*u.Id]
		stored.DisabledAt = &now
		m.authState.users[*u.Id] = stored
	}
	return *u.Id
}

func TestDeleteUser_LastAdminGuardRefuses(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	h := newTestHandler(t, m)

	// Sole active admin in the system.
	id := seedAdmin(t, m, "alice", false)

	rr := do(h, http.MethodDelete, fmt.Sprintf("/v1/admin/users/%s", id), "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 last-admin guard, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeleteUser_AllowedWhenAnotherAdminActive(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	h := newTestHandler(t, m)

	// Two active admins — deleting one is fine.
	target := seedAdmin(t, m, "alice", false)
	_ = seedAdmin(t, m, "bob", false)

	rr := do(h, http.MethodDelete, fmt.Sprintf("/v1/admin/users/%s", target), "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeleteUser_AllowedWhenTargetAlreadyDisabled(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	h := newTestHandler(t, m)

	// One active admin + one disabled admin → deleting the disabled one
	// keeps the active count above zero.
	_ = seedAdmin(t, m, "alice", false)
	target := seedAdmin(t, m, "bob", true) // disabled — not counted

	rr := do(h, http.MethodDelete, fmt.Sprintf("/v1/admin/users/%s", target), "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeleteUser_NonAdminTargetUnaffected(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	h := newTestHandler(t, m)

	_ = seedAdmin(t, m, "alice", false)
	// Editor user — not admin, not gated by the guard.
	editor, err := m.CreateUser(t.Context(), UserInsert{
		Username:     "carol",
		Role:         auth.RoleEditor,
		PasswordHash: "$argon2id$placeholder",
	})
	if err != nil {
		t.Fatalf("create editor: %v", err)
	}

	rr := do(h, http.MethodDelete, fmt.Sprintf("/v1/admin/users/%s", *editor.Id), "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for non-admin delete, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestUpdateUser_LastAdminDemotionRefused(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	h := newTestHandler(t, m)

	id := seedAdmin(t, m, "alice", false)

	patch, _ := json.Marshal(map[string]string{"role": "viewer"})
	rr := do(h, http.MethodPatch, fmt.Sprintf("/v1/admin/users/%s", id), string(patch))
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 last-admin demotion guard, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestUpdateUser_LastAdminDisableRefused(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	h := newTestHandler(t, m)

	id := seedAdmin(t, m, "alice", false)

	patch, _ := json.Marshal(map[string]bool{"disabled": true})
	rr := do(h, http.MethodPatch, fmt.Sprintf("/v1/admin/users/%s", id), string(patch))
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 last-admin disable guard, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestUpdateUser_DemotionAllowedWhenAnotherAdminActive(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	h := newTestHandler(t, m)

	target := seedAdmin(t, m, "alice", false)
	_ = seedAdmin(t, m, "bob", false)

	patch, _ := json.Marshal(map[string]string{"role": "viewer"})
	rr := do(h, http.MethodPatch, fmt.Sprintf("/v1/admin/users/%s", target), string(patch))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 demotion, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestUpdateUser_DisableAllowedWhenAnotherAdminActive(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	h := newTestHandler(t, m)

	target := seedAdmin(t, m, "alice", false)
	_ = seedAdmin(t, m, "bob", false)

	patch, _ := json.Marshal(map[string]bool{"disabled": true})
	rr := do(h, http.MethodPatch, fmt.Sprintf("/v1/admin/users/%s", target), string(patch))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 disable, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestUpdateUser_DisabledTargetDemotionAllowed(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	h := newTestHandler(t, m)

	// Active admin + disabled admin. Demoting the disabled one is fine —
	// they weren't counted as active anyway.
	_ = seedAdmin(t, m, "alice", false)
	target := seedAdmin(t, m, "bob", true)

	patch, _ := json.Marshal(map[string]string{"role": "viewer"})
	rr := do(h, http.MethodPatch, fmt.Sprintf("/v1/admin/users/%s", target), string(patch))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 disabled-admin demotion, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestUpdateUser_NonAdminPatchUnaffected(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	h := newTestHandler(t, m)

	_ = seedAdmin(t, m, "alice", false)
	editor, err := m.CreateUser(t.Context(), UserInsert{
		Username:     "carol",
		Role:         auth.RoleEditor,
		PasswordHash: "$argon2id$placeholder",
	})
	if err != nil {
		t.Fatalf("create editor: %v", err)
	}

	patch, _ := json.Marshal(map[string]string{"role": "viewer"})
	rr := do(h, http.MethodPatch, fmt.Sprintf("/v1/admin/users/%s", *editor.Id), string(patch))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 non-admin role change, got %d body=%s", rr.Code, rr.Body.String())
	}
}
