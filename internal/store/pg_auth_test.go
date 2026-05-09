package store

import (
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/auth"
)

const lockoutThreshold = 6

// seedUser creates a user with a placeholder password hash.
func seedUser(t *testing.T, pg *PG, username, role string) api.User {
	t.Helper()
	u, err := pg.CreateUser(t.Context(), api.UserInsert{
		Username:     username,
		PasswordHash: "$argon2id$placeholder",
		Role:         role,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func TestIncrementFailedLogin_BelowThreshold(t *testing.T) {
	pg := newTestPG(t)
	u := seedUser(t, pg, "victor", auth.RoleEditor)

	for i := 1; i <= 5; i++ {
		locked, err := pg.IncrementFailedLogin(t.Context(), *u.Id, lockoutThreshold)
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if locked {
			t.Fatalf("attempt %d: should not be locked yet", i)
		}
	}

	got, err := pg.GetUser(t.Context(), *u.Id)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.LockedAt != nil {
		t.Errorf("locked_at should be NULL, got %v", got.LockedAt)
	}
	if got.FailedLoginCount == nil || *got.FailedLoginCount != 5 {
		t.Errorf("failed_login_count = %v, want 5", got.FailedLoginCount)
	}
}

func TestIncrementFailedLogin_ReachesThreshold(t *testing.T) {
	pg := newTestPG(t)
	u := seedUser(t, pg, "valeria", auth.RoleEditor)

	var locked bool
	var err error
	for i := 1; i <= lockoutThreshold; i++ {
		locked, err = pg.IncrementFailedLogin(t.Context(), *u.Id, lockoutThreshold)
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if !locked {
		t.Fatalf("expected locked=true on attempt %d, got false", lockoutThreshold)
	}

	got, err := pg.GetUser(t.Context(), *u.Id)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.LockedAt == nil {
		t.Fatal("locked_at should be set after threshold")
	}
}

func TestIncrementFailedLogin_AfterLockNoOp(t *testing.T) {
	pg := newTestPG(t)
	u := seedUser(t, pg, "wesley", auth.RoleEditor)

	for i := 1; i <= lockoutThreshold; i++ {
		if _, err := pg.IncrementFailedLogin(t.Context(), *u.Id, lockoutThreshold); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	first, err := pg.GetUser(t.Context(), *u.Id)
	if err != nil {
		t.Fatalf("get after lock: %v", err)
	}

	// 7th attempt is a no-op.
	locked, err := pg.IncrementFailedLogin(t.Context(), *u.Id, lockoutThreshold)
	if err != nil {
		t.Fatalf("post-lock attempt: %v", err)
	}
	if locked {
		t.Error("locked=true on already-locked account")
	}

	got, err := pg.GetUser(t.Context(), *u.Id)
	if err != nil {
		t.Fatalf("get after no-op: %v", err)
	}
	if got.FailedLoginCount == nil || *got.FailedLoginCount != lockoutThreshold {
		t.Errorf("failed_login_count = %v, want %d (must not increment past threshold)",
			got.FailedLoginCount, lockoutThreshold)
	}
	if got.LockedAt == nil || !got.LockedAt.Equal(*first.LockedAt) {
		t.Errorf("locked_at refreshed: was %v, now %v", first.LockedAt, got.LockedAt)
	}
}

func TestIncrementFailedLogin_LastAdminCanLock(t *testing.T) {
	// Regression: confirm the last-admin guard is intentionally absent.
	pg := newTestPG(t)
	u := seedUser(t, pg, "lone-admin", auth.RoleAdmin)

	var locked bool
	for i := 1; i <= lockoutThreshold; i++ {
		var err error
		locked, err = pg.IncrementFailedLogin(t.Context(), *u.Id, lockoutThreshold)
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if !locked {
		t.Fatal("the only admin must be lockable; recovery is via the boot rescue")
	}
}

func TestResetFailedLogin(t *testing.T) {
	pg := newTestPG(t)
	u := seedUser(t, pg, "rose", auth.RoleEditor)

	for i := 1; i <= lockoutThreshold; i++ {
		if _, err := pg.IncrementFailedLogin(t.Context(), *u.Id, lockoutThreshold); err != nil {
			t.Fatalf("seed lock: %v", err)
		}
	}

	if err := pg.ResetFailedLogin(t.Context(), *u.Id); err != nil {
		t.Fatalf("reset: %v", err)
	}

	got, err := pg.GetUser(t.Context(), *u.Id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.FailedLoginCount == nil || *got.FailedLoginCount != 0 {
		t.Errorf("failed_login_count = %v, want 0", got.FailedLoginCount)
	}
	if got.LockedAt != nil {
		t.Errorf("locked_at should be NULL, got %v", got.LockedAt)
	}
}

