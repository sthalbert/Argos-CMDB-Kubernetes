package api

// Tests for Server.VerifyToken (POST /v1/auth/verify, ADR-0016 §5).
// Uses the existing memStore test fake — no PostgreSQL needed.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// newVerifyServer builds an ingest-mux-ready server for verify endpoint tests.
// verifyLimiter=nil disables rate limiting (so tests don't trip over the limit).
func newVerifyServer(t *testing.T, store Store) http.Handler {
	t.Helper()
	srv := NewServer("test", store, auth.SecureNever, nil, NewLoginRateLimiter(), nil /* no rate limit */)
	strict := NewStrictHandlerWithOptions(
		srv,
		[]StrictMiddlewareFunc{InjectRequestMiddleware},
		StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			},
		},
	)
	// Use the ingest mux so /v1/auth/verify is actually registered.
	return NewIngestMux(IngestMuxConfig{
		Server: strict,
		// No-op auth + audit middleware — tests exercise only the handler.
		AuthMiddleware:  func(next http.Handler) http.Handler { return next },
		AuditMiddleware: func(next http.Handler) http.Handler { return next },
	})
}

func TestVerifyToken_MissingBody_Returns400(t *testing.T) {
	t.Parallel()
	h := newVerifyServer(t, newMemStore())

	// Empty body — no "token" field.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/verify", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 for missing token", rr.Code)
	}
}

func TestVerifyToken_InvalidToken_Returns401(t *testing.T) {
	t.Parallel()
	h := newVerifyServer(t, newMemStore())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/verify",
		strings.NewReader(`{"token":"argos_pat_deadbeef_notavalidtoken"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 for unknown token", rr.Code)
	}
	// Response must not reveal why (no discriminating detail).
	body := rr.Body.String()
	for _, leak := range []string{"not found", "hash mismatch", "expired", "prefix"} {
		if strings.Contains(strings.ToLower(body), leak) {
			t.Errorf("response reveals discriminating detail %q: %s", leak, body)
		}
	}
}

// mintAndStoreToken creates an admin user, mints a real token, and stores it.
// Returns the minted token plaintext.
func mintAndStoreToken(t *testing.T, store Store, tokenName string, scopes []string) string {
	t.Helper()
	owner, err := store.CreateUser(context.Background(), UserInsert{
		Username:     "owner-" + tokenName,
		PasswordHash: "ignored",
		Role:         auth.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	minted, err := auth.MintToken()
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	_, err = store.CreateAPIToken(context.Background(), APITokenInsert{
		ID:              uuid.New(),
		Name:            tokenName,
		Prefix:          minted.Prefix,
		Hash:            minted.Hash,
		Scopes:          scopes,
		CreatedByUserID: *owner.Id,
	})
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	return minted.Plaintext
}

func TestVerifyToken_ValidToken_Returns200(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	plaintext := mintAndStoreToken(t, store, "collector-token", []string{auth.ScopeWrite})

	h := newVerifyServer(t, store)
	body := `{"token":"` + plaintext + `"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200\nbody: %s", rr.Code, rr.Body.String())
	}
	var resp VerifyTokenResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	assertVerifyResponse(t, &resp, "collector-token", auth.ScopeWrite)
}

// assertVerifyResponse checks the common fields of a successful VerifyTokenResponse.
func assertVerifyResponse(t *testing.T, resp *VerifyTokenResponse, wantTokenName, wantScope string) {
	t.Helper()
	if !resp.Valid {
		t.Error("Valid = false; want true")
	}
	if resp.Kind != VerifyTokenResponseKindToken {
		t.Errorf("Kind = %q; want token", resp.Kind)
	}
	if resp.CallerId == nil {
		t.Error("CallerId is absent; want non-empty token id")
	}
	if resp.TokenName == nil || *resp.TokenName != wantTokenName {
		t.Errorf("TokenName = %v; want %s", resp.TokenName, wantTokenName)
	}
	if len(resp.Scopes) == 0 || resp.Scopes[0] != wantScope {
		t.Errorf("Scopes = %v; want [%s]", resp.Scopes, wantScope)
	}
}

func TestVerifyToken_VMCollectorToken_IncludesBoundCloudAccountID(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	accountID := uuid.New()
	plaintext := mintAndStoreTokenWithAccount(t, store, "vm-collector-token", []string{auth.ScopeVMCollector}, &accountID)

	h := newVerifyServer(t, store)
	body := `{"token":"` + plaintext + `"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200\nbody: %s", rr.Code, rr.Body.String())
	}
	var resp VerifyTokenResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.BoundCloudAccountId == nil {
		t.Fatal("BoundCloudAccountId is nil; want set for vm-collector token")
	}
	if resp.BoundCloudAccountId.String() != accountID.String() {
		t.Errorf("BoundCloudAccountId = %q; want %s", resp.BoundCloudAccountId, accountID)
	}
}

// mintAndStoreTokenWithAccount is like mintAndStoreToken but also binds the token
// to a cloud account (used for vm-collector tokens).
func mintAndStoreTokenWithAccount(t *testing.T, store Store, tokenName string, scopes []string, accountID *uuid.UUID) string {
	t.Helper()
	owner, err := store.CreateUser(context.Background(), UserInsert{
		Username:     "owner-" + tokenName,
		PasswordHash: "ignored",
		Role:         auth.RoleAdmin,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	minted, err := auth.MintToken()
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	_, err = store.CreateAPIToken(context.Background(), APITokenInsert{
		ID:                  uuid.New(),
		Name:                tokenName,
		Prefix:              minted.Prefix,
		Hash:                minted.Hash,
		Scopes:              scopes,
		CreatedByUserID:     *owner.Id,
		BoundCloudAccountID: accountID,
	})
	if err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	return minted.Plaintext
}

// TestVerifyToken_PublicEndpoint_NoAuthHeader_Returns200 locks in the
// ADR-0016 §5 contract: /v1/auth/verify is authenticated by the listener's
// mTLS handshake, NOT by an Authorization header. The DMZ ingest gateway
// sends the token to verify in the request body and never presents a
// bearer credential of its own. Wrapping with the real auth.Middleware
// must therefore let an Authorization-header-less request through to the
// VerifyToken handler — anything else (e.g. a 401 from the middleware
// because the operation inherits the global "BearerAuth required" block)
// is the bug fixed by adding `security: []` to the operation in the spec.
func TestVerifyToken_PublicEndpoint_NoAuthHeader_Returns200(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	plaintext := mintAndStoreToken(t, store, "gateway-test", []string{auth.ScopeWrite})

	srv := NewServer("test", store, auth.SecureNever, nil, NewLoginRateLimiter(), nil)
	strict := NewStrictHandlerWithOptions(
		srv,
		[]StrictMiddlewareFunc{InjectRequestMiddleware},
		StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			},
		},
	)
	// The real auth.Middleware — not a no-op. This is what longue-vue runs in
	// production on the ingest listener.
	realAuth := MiddlewareFunc(auth.Middleware(store, auth.SecureNever, nil))
	h := NewIngestMux(IngestMuxConfig{
		Server:          strict,
		AuthMiddleware:  realAuth,
		AuditMiddleware: func(next http.Handler) http.Handler { return next },
	})

	body := `{"token":"` + plaintext + `"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/verify", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Deliberately NO Authorization header — auth is the listener-level
	// mTLS handshake (which httptest doesn't simulate; it doesn't need to,
	// because the spec's `security: []` declares the endpoint public to
	// the application-layer middleware).
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 (the verify endpoint must be public to the auth middleware)\nbody: %s",
			rr.Code, rr.Body.String())
	}
}

func TestVerifyToken_RateLimited_Returns401(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	// Build a real VerifyRateLimiter and exhaust it completely for the test IP.
	limiter := NewVerifyRateLimiter()
	// Exhaust the limiter's burst for address "192.0.2.1" (httptest default RemoteAddr).
	testIP := "192.0.2.1"
	for range 500 {
		limiter.Allow(testIP)
	}

	srv := NewServer("test", store, auth.SecureNever, nil, NewLoginRateLimiter(), limiter)
	strict := NewStrictHandlerWithOptions(
		srv,
		[]StrictMiddlewareFunc{InjectRequestMiddleware},
		StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			},
		},
	)
	h := NewIngestMux(IngestMuxConfig{
		Server:          strict,
		AuthMiddleware:  func(next http.Handler) http.Handler { return next },
		AuditMiddleware: func(next http.Handler) http.Handler { return next },
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/verify",
		strings.NewReader(`{"token":"argos_pat_deadbeef_tok"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = testIP + ":12345"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 when rate limit exceeded", rr.Code)
	}
}
