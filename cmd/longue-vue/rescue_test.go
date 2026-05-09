package main

// AUTH-VULN-03 / docs/superpowers/specs/2026-05-09-per-account-login-lockout-design.md
//
// Boot-time rescue when every admin is locked.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/store"
)

const rescueEnvVar = "LONGUE_VUE_ADMIN_RESCUE_PASSWORD"

func newTestPGForRescue(t *testing.T) *store.PG {
	t.Helper()
	dsn := os.Getenv("PGX_TEST_DATABASE")
	if dsn == "" {
		t.Skip("PGX_TEST_DATABASE not set; skipping rescue integration test")
	}
	ctx := context.Background()
	pg, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := pg.Migrate(ctx); err != nil {
		pg.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		// We can't access pg.pool from this package. Open a parallel
		// pool just for cleanup -- pgx pools are concurrency-safe and
		// cheap to construct.
		cleanupPool, err := pgxpool.New(context.Background(), dsn)
		if err == nil {
			_, _ = cleanupPool.Exec(context.Background(),
				"TRUNCATE clusters, api_tokens, sessions, user_identities, oidc_auth_states, audit_events, users CASCADE")
			cleanupPool.Close()
		}
		pg.Close()
	})
	return pg
}

func TestAdminRescue_NoLockedAdminNoOp(t *testing.T) {
	pg := newTestPGForRescue(t)
	ctx := context.Background()

	// Seed one healthy admin.
	hash, err := auth.HashPassword("originalpassword12")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	healthy, err := pg.CreateUser(ctx, api.UserInsert{
		Username:     "admin",
		PasswordHash: hash,
		Role:         auth.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Setenv(rescueEnvVar, "newrescuepassword42")

	if err := rescueLockedAdminIfNeeded(ctx, pg); err != nil {
		t.Fatalf("rescue: %v", err)
	}

	got, err := pg.GetUser(ctx, *healthy.Id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LockedAt != nil {
		t.Error("rescue should not have touched a healthy admin")
	}
	// Password unchanged: the original hash should still verify.
	withSecret, err := pg.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("get by username: %v", err)
	}
	if err := auth.VerifyPassword("originalpassword12", withSecret.PasswordHash); err != nil {
		t.Errorf("rescue overwrote a healthy admin's password: %v", err)
	}
}

//nolint:gocyclo // integration test verifies many aspects of the rescue path in one function; splitting would obscure intent
func TestAdminRescue_AllAdminsLockedTriggersUnlock(t *testing.T) {
	pg := newTestPGForRescue(t)
	ctx := context.Background()

	hash, err := auth.HashPassword("originalpassword12")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	created, err := pg.CreateUser(ctx, api.UserInsert{
		Username:     "lockout-victim",
		PasswordHash: hash,
		Role:         auth.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Drive into locked state.
	for range 6 {
		if _, err := pg.IncrementFailedLogin(ctx, *created.Id, 6); err != nil {
			t.Fatalf("seed lock: %v", err)
		}
	}

	t.Setenv(rescueEnvVar, "rescuepassword321!")

	if err := rescueLockedAdminIfNeeded(ctx, pg); err != nil {
		t.Fatalf("rescue: %v", err)
	}

	got, err := pg.GetUserByUsername(ctx, "lockout-victim")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LockedAt != nil {
		t.Error("locked_at should be NULL after rescue")
	}
	if got.MustChangePassword == nil || !*got.MustChangePassword {
		t.Error("must_change_password should be true after rescue")
	}
	if err := auth.VerifyPassword("rescuepassword321!", got.PasswordHash); err != nil {
		t.Errorf("rescue password did not take effect: %v", err)
	}
	// Audit event written.
	events, _, err := pg.ListAuditEvents(ctx, api.AuditEventFilter{}, 100, "")
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Source != nil && *e.Source == api.AuditEventSourceSystem && e.Action == "auth.admin_rescue" {
			found = true
			break
		}
	}
	if !found {
		t.Error("audit event source=system action=auth.admin_rescue not found")
	}
}

func TestAdminRescue_VarUnsetSkipsRescue(t *testing.T) {
	pg := newTestPGForRescue(t)
	ctx := context.Background()

	hash, err := auth.HashPassword("originalpassword12")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	created, err := pg.CreateUser(ctx, api.UserInsert{
		Username:     "stays-locked",
		PasswordHash: hash,
		Role:         auth.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for range 6 {
		if _, err := pg.IncrementFailedLogin(ctx, *created.Id, 6); err != nil {
			t.Fatalf("seed lock: %v", err)
		}
	}

	// Explicitly unset.
	t.Setenv(rescueEnvVar, "")

	if err := rescueLockedAdminIfNeeded(ctx, pg); err != nil {
		t.Fatalf("rescue: %v", err)
	}

	got, err := pg.GetUser(ctx, *created.Id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LockedAt == nil {
		t.Error("admin should still be locked when rescue env var is empty")
	}
}

func TestAdminRescue_PicksMostRecentlyActive(t *testing.T) {
	pg := newTestPGForRescue(t)
	ctx := context.Background()
	hash, _ := auth.HashPassword("originalpassword12")

	older, _ := pg.CreateUser(ctx, api.UserInsert{
		Username: "older-admin", PasswordHash: hash, Role: auth.RoleAdmin,
	})
	newer, _ := pg.CreateUser(ctx, api.UserInsert{
		Username: "newer-admin", PasswordHash: hash, Role: auth.RoleAdmin,
	})

	// Mark "newer-admin" as the more recently active. Note the order:
	// touch older first (older timestamp), then newer (newer timestamp).
	if err := pg.TouchUserLogin(ctx, *older.Id, time.Now().UTC().Add(-72*time.Hour)); err != nil {
		t.Fatalf("touch older: %v", err)
	}
	if err := pg.TouchUserLogin(ctx, *newer.Id, time.Now().UTC()); err != nil {
		t.Fatalf("touch newer: %v", err)
	}

	// Lock both.
	for _, id := range []uuid.UUID{*older.Id, *newer.Id} {
		for range 6 {
			if _, err := pg.IncrementFailedLogin(ctx, id, 6); err != nil {
				t.Fatalf("seed lock: %v", err)
			}
		}
	}

	t.Setenv(rescueEnvVar, "rescue9999!")

	if err := rescueLockedAdminIfNeeded(ctx, pg); err != nil {
		t.Fatalf("rescue: %v", err)
	}

	gotNewer, _ := pg.GetUser(ctx, *newer.Id)
	gotOlder, _ := pg.GetUser(ctx, *older.Id)
	if gotNewer.LockedAt != nil {
		t.Error("newer admin should have been rescued")
	}
	if gotOlder.LockedAt == nil {
		t.Error("older admin should still be locked (rescue picks one)")
	}
}
