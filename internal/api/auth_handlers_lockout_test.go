package api

// AUTH-VULN-03 / docs/superpowers/specs/2026-05-09-per-account-login-lockout-design.md
//
// End-to-end Login lockout. Uses the in-memory memStore fake -- no DB.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// seedLoginUser creates a user with a known password and returns nothing.
// Use the resulting credentials by calling loginPOST.
func seedLoginUser(t *testing.T, m *memStore, username, password, role string) {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := m.CreateUser(t.Context(), UserInsert{
		Username:     username,
		Role:         role,
		PasswordHash: hash,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
}

// loginPOST issues POST /v1/auth/login with the given credentials and
// source IP, returning the response.
func loginPOST(t *testing.T, h http.Handler, username, password, ip string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":12345"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr.Result()
}

func TestLogin_LocksAfterSixFailures(t *testing.T) {
	m := newMemStore()
	h := newTestHandler(t, m)
	seedLoginUser(t, m, "alice", "correct-horse-battery-staple", auth.RoleEditor)

	// 6 wrong passwords -- each from a different IP to bypass per-IP limiter.
	for i := 1; i <= 6; i++ {
		ip := fmt.Sprintf("10.0.0.%d", i)
		resp := loginPOST(t, h, "alice", "wrong", ip)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i, resp.StatusCode)
		}
	}

	// 7th attempt with the CORRECT password must still be 401.
	resp := loginPOST(t, h, "alice", "correct-horse-battery-staple", "10.0.0.99")
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-lock correct-password status = %d, want 401", resp.StatusCode)
	}

	// memStore-side: the user must show LockedAt != nil.
	withSecret, err := m.GetUserByUsername(t.Context(), "alice")
	if err != nil {
		t.Fatalf("get by username: %v", err)
	}
	if withSecret.LockedAt == nil {
		t.Error("user.locked_at should be set after 6 failures")
	}
}

func TestLogin_LockedAccountReturnsGeneric401(t *testing.T) {
	m := newMemStore()
	h := newTestHandler(t, m)
	seedLoginUser(t, m, "bob", "supersecretpassword!", auth.RoleEditor)

	// Capture a wrong-password 401.
	wrong := loginPOST(t, h, "bob", "wrong", "10.0.1.1")
	wrongBody, _ := io.ReadAll(wrong.Body)
	_ = wrong.Body.Close()
	wrongHdr := wrong.Header.Clone()

	// Drive into locked state.
	for i := 2; i <= 6; i++ {
		ip := fmt.Sprintf("10.0.1.%d", i)
		r := loginPOST(t, h, "bob", "wrong", ip)
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
	}

	locked := loginPOST(t, h, "bob", "supersecretpassword!", "10.0.1.99")
	lockedBody, _ := io.ReadAll(locked.Body)
	_ = locked.Body.Close()

	if locked.StatusCode != wrong.StatusCode {
		t.Errorf("locked status = %d, wrong-password = %d", locked.StatusCode, wrong.StatusCode)
	}
	if !bytes.Equal(lockedBody, wrongBody) {
		t.Errorf("response body diverges -- enumeration risk\n  locked: %s\n   wrong: %s", lockedBody, wrongBody)
	}
	for _, hname := range []string{"Content-Type", "Www-Authenticate"} {
		if locked.Header.Get(hname) != wrongHdr.Get(hname) {
			t.Errorf("header %q diverges: locked=%q wrong=%q", hname, locked.Header.Get(hname), wrongHdr.Get(hname))
		}
	}
}

func TestLogin_SuccessResetsCounter(t *testing.T) {
	m := newMemStore()
	h := newTestHandler(t, m)
	seedLoginUser(t, m, "carol", "anotherlongpassword!", auth.RoleEditor)

	for i := 1; i <= 5; i++ {
		ip := fmt.Sprintf("10.0.2.%d", i)
		r := loginPOST(t, h, "carol", "wrong", ip)
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
	}

	good := loginPOST(t, h, "carol", "anotherlongpassword!", "10.0.2.99")
	_, _ = io.Copy(io.Discard, good.Body)
	_ = good.Body.Close()
	if good.StatusCode != http.StatusNoContent {
		t.Fatalf("good login status = %d, want 204", good.StatusCode)
	}

	withSecret, err := m.GetUserByUsername(t.Context(), "carol")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if withSecret.FailedLoginCount != nil && *withSecret.FailedLoginCount != 0 {
		t.Errorf("failed_login_count = %v, want 0 after success", withSecret.FailedLoginCount)
	}
}

func TestLogin_UnknownUserNeverLocks(t *testing.T) {
	m := newMemStore()
	h := newTestHandler(t, m)
	_ = m // keep var live; we don't seed anyone

	for i := 0; i < 100; i++ {
		ip := fmt.Sprintf("10.0.3.%d", i%200)
		r := loginPOST(t, h, "nosuchuser", "wrong", ip)
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		if r.StatusCode != http.StatusUnauthorized && r.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("attempt %d: unexpected status %d (want 401 or 429)", i, r.StatusCode)
		}
	}
	// No DB-side assertion -- the user does not exist. The point is
	// that the server stayed up (no panic, no 500) and didn't try to
	// increment a counter on a non-existent row.
}

func TestLogin_PerIPLimiterStillFiresFirst(t *testing.T) {
	m := newMemStore()
	h := newTestHandler(t, m)
	seedLoginUser(t, m, "dave", "yetanotherlongpw!", auth.RoleEditor)

	const ip = "10.0.4.1"
	got429 := false
	for i := 0; i < 10; i++ {
		r := loginPOST(t, h, "dave", "wrong", ip)
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		if r.StatusCode == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("per-IP limiter did not return 429 on burst")
	}

	// The per-account counter should still be < threshold because
	// the limiter rejected most requests before they reached
	// IncrementFailedLogin.
	withSecret, err := m.GetUserByUsername(t.Context(), "dave")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if withSecret.LockedAt != nil {
		t.Error("account locked despite per-IP limiter firing first")
	}
}

func TestPatchUser_Unlock(t *testing.T) {
	m := newMemStore()
	h := newTestHandler(t, m)
	seedLoginUser(t, m, "eve", "passwordforlockoutE!", auth.RoleEditor)
	// We also need at least one admin so the last-admin guard does not
	// block any incidental Disabled flips. (Not strictly needed for
	// unlock-only patches, but matches the existing test pattern.)
	_ = seedAdmin(t, m, "lockout-admin", false)

	// Lock eve.
	for i := 1; i <= 6; i++ {
		ip := fmt.Sprintf("10.0.5.%d", i)
		r := loginPOST(t, h, "eve", "wrong", ip)
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
	}

	eveSecret, err := m.GetUserByUsername(t.Context(), "eve")
	if err != nil {
		t.Fatalf("get eve: %v", err)
	}
	if eveSecret.LockedAt == nil {
		t.Fatal("test bug: eve should be locked after 6 wrong attempts")
	}

	// PATCH /v1/admin/users/{id} with unlock=true. The newTestHandler
	// doesn't mount auth middleware, so we don't need a session cookie.
	patchBody, _ := json.Marshal(map[string]any{"unlock": true})
	rr := do(h, http.MethodPatch, fmt.Sprintf("/v1/admin/users/%s", *eveSecret.Id), string(patchBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("unlock PATCH status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// Eve can now log in with the right password.
	good := loginPOST(t, h, "eve", "passwordforlockoutE!", "10.0.5.99")
	_, _ = io.Copy(io.Discard, good.Body)
	_ = good.Body.Close()
	if good.StatusCode != http.StatusNoContent {
		t.Errorf("post-unlock login status = %d, want 204", good.StatusCode)
	}
}
