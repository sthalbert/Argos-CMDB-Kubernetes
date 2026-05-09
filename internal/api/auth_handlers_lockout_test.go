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
