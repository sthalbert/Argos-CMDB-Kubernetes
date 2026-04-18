package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mustTokenStore(t *testing.T, tokens []ScopedToken) *TokenStore {
	t.Helper()
	store, err := NewTokenStore(tokens)
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}
	return store
}

func TestNewTokenStoreValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		tokens  []ScopedToken
		wantErr bool
	}{
		{"empty slice ok", nil, false},
		{"valid single", []ScopedToken{{Name: "a", Token: "x", Scopes: []string{ScopeRead}}}, false},
		{"empty token rejected", []ScopedToken{{Name: "a", Token: "", Scopes: []string{ScopeRead}}}, true},
		{"duplicate token rejected", []ScopedToken{
			{Name: "a", Token: "same", Scopes: []string{ScopeRead}},
			{Name: "b", Token: "same", Scopes: []string{ScopeWrite}},
		}, true},
		{"unknown scope rejected", []ScopedToken{{Name: "a", Token: "x", Scopes: []string{"bogus"}}}, true},
		{"admin scope accepted", []ScopedToken{{Name: "a", Token: "x", Scopes: []string{ScopeAdmin}}}, false},
		{"all known scopes accepted", []ScopedToken{
			{Name: "a", Token: "x", Scopes: []string{ScopeRead, ScopeWrite, ScopeDelete, ScopeAdmin}},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewTokenStore(tt.tokens)
			if (err != nil) != tt.wantErr {
				t.Errorf("err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestParseTokensJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		wantLen int
		wantErr bool
	}{
		{"empty string yields nil", "", 0, false},
		{"whitespace yields nil", "   \t\n ", 0, false},
		{"empty array", "[]", 0, false},
		{"single token", `[{"name":"a","token":"tok","scopes":["read"]}]`, 1, false},
		{"multiple tokens", `[{"name":"a","token":"t1","scopes":["read"]},{"name":"b","token":"t2","scopes":["admin"]}]`, 2, false},
		{"malformed json", `[{`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseTokensJSON(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tt.wantErr)
			}
			if err == nil && len(got) != tt.wantLen {
				t.Errorf("len=%d, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestBearerAuthEnforcesScopes(t *testing.T) {
	t.Parallel()

	store := mustTokenStore(t, []ScopedToken{
		{Name: "ro", Token: "ro-tok", Scopes: []string{ScopeRead}},
		{Name: "rw", Token: "rw-tok", Scopes: []string{ScopeRead, ScopeWrite}},
		{Name: "del", Token: "del-tok", Scopes: []string{ScopeDelete}},
		{Name: "god", Token: "god-tok", Scopes: []string{ScopeAdmin}},
	})

	h := HandlerWithOptions(NewServer("test", newMemStore()), StdHTTPServerOptions{
		Middlewares: []MiddlewareFunc{BearerAuth(store)},
	})

	// Pre-create a cluster with the admin token so subsequent cases exercise
	// auth, not missing-data paths.
	precondition := doAuth(t, h, http.MethodPost, "/v1/clusters", "god-tok", `{"name":"prod"}`)
	if precondition.Code != http.StatusCreated {
		t.Fatalf("precondition failed to create cluster: status=%d body=%q", precondition.Code, precondition.Body.String())
	}

	anyUUID := "00000000-0000-0000-0000-000000000000"

	tests := []struct {
		name       string
		method     string
		target     string
		token      string
		body       string
		wantStatus int
	}{
		{"healthz unauthenticated", http.MethodGet, "/healthz", "", "", http.StatusOK},
		{"readyz unauthenticated", http.MethodGet, "/readyz", "", "", http.StatusOK},

		{"list without token 401", http.MethodGet, "/v1/clusters", "", "", http.StatusUnauthorized},
		{"list bad token 401", http.MethodGet, "/v1/clusters", "nope", "", http.StatusUnauthorized},
		{"list with read ok", http.MethodGet, "/v1/clusters", "ro-tok", "", http.StatusOK},
		{"list with delete-only 403", http.MethodGet, "/v1/clusters", "del-tok", "", http.StatusForbidden},
		{"list with admin ok", http.MethodGet, "/v1/clusters", "god-tok", "", http.StatusOK},

		{"create with read 403", http.MethodPost, "/v1/clusters", "ro-tok", `{"name":"a"}`, http.StatusForbidden},
		{"create with write ok", http.MethodPost, "/v1/clusters", "rw-tok", `{"name":"b"}`, http.StatusCreated},
		{"create with admin ok", http.MethodPost, "/v1/clusters", "god-tok", `{"name":"c"}`, http.StatusCreated},

		{"patch with read 403", http.MethodPatch, "/v1/clusters/" + anyUUID, "ro-tok", `{"provider":"gke"}`, http.StatusForbidden},
		{"patch with write returns 404 (no row)", http.MethodPatch, "/v1/clusters/" + anyUUID, "rw-tok", `{"provider":"gke"}`, http.StatusNotFound},

		{"delete with write 403", http.MethodDelete, "/v1/clusters/" + anyUUID, "rw-tok", "", http.StatusForbidden},
		{"delete with delete-scope returns 404", http.MethodDelete, "/v1/clusters/" + anyUUID, "del-tok", "", http.StatusNotFound},
		{"delete with admin returns 404", http.MethodDelete, "/v1/clusters/" + anyUUID, "god-tok", "", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := doAuth(t, h, tt.method, tt.target, tt.token, tt.body)
			if rr.Code != tt.wantStatus {
				t.Errorf("status=%d, want=%d; body=%q", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantStatus == http.StatusUnauthorized {
				if rr.Header().Get("WWW-Authenticate") == "" {
					t.Error("WWW-Authenticate header missing on 401")
				}
			}
		})
	}
}

func doAuth(t *testing.T, h http.Handler, method, target, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
