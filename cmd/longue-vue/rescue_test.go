package main

// AUTH-VULN-03 / docs/superpowers/specs/2026-05-09-per-account-login-lockout-design.md
//
// Boot-time rescue when every admin is locked.

import (
	"context"
	"os"
	"testing"

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
