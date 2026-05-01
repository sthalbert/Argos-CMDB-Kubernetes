package ingestgw

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeArgosd simulates the argosd ingest listener. It accepts verify + write calls.
type fakeArgosd struct {
	// validToken is the only token that verify accepts.
	validToken string
	// receivedRequests captures the paths seen on successful forward.
	receivedRequests []string
	// verifyCallCount tracks how many times verify was called.
	verifyCallCount int
}

func (f *fakeArgosd) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Handle verify endpoint.
	if r.Method == http.MethodPost && r.URL.Path == verifyPath {
		f.verifyCallCount++
		var body struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Token == f.validToken {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(verifyResponseBody{
				Valid:  true,
				Kind:   "token",
				Scopes: []string{"write"},
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Handle any other write path — forward succeeds.
	f.receivedRequests = append(f.receivedRequests, r.URL.Path)
	w.WriteHeader(http.StatusCreated)
}

func newTestGateway(t *testing.T, argosdURL string) *Server {
	t.Helper()
	s, err := NewServer(Config{
		UpstreamBaseURL: argosdURL,
		UpstreamClient:  &http.Client{},
		MaxBodyBytes:    1 << 20,
		CacheConfig:     CacheConfig{MaxEntries: 100, PositiveTTL: 60, NegativeTTL: 10},
		RequiredScope:   "write",
		Logger:          nil,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

func TestServer_Healthz(t *testing.T) {
	t.Parallel()
	fake := httptest.NewServer(&fakeArgosd{validToken: "tok"})
	t.Cleanup(fake.Close)
	gw := newTestGateway(t, fake.URL)
	h := gw.Handler()

	rr := httptest.NewRecorder()
	rr.Body = nil
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", http.NoBody)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("healthz status = %d; want 200", rr.Code)
	}
}

func TestServer_Readyz(t *testing.T) {
	t.Parallel()
	fake := httptest.NewServer(&fakeArgosd{validToken: "tok"})
	t.Cleanup(fake.Close)
	gw := newTestGateway(t, fake.URL)
	h := gw.Handler()

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", http.NoBody)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("readyz status = %d; want 200", rr.Code)
	}
}

func TestServer_AllowlistedRoute_ValidToken(t *testing.T) {
	t.Parallel()
	const validToken = "argos_pat_aabbccdd_goodtoken"
	backend := &fakeArgosd{validToken: validToken}
	fake := httptest.NewServer(backend)
	t.Cleanup(fake.Close)

	gw := newTestGateway(t, fake.URL)
	h := gw.Handler()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/clusters", strings.NewReader(`{"name":"test"}`))
	req.Header.Set("Authorization", "Bearer "+validToken)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Errorf("status = %d; want 201", rr.Code)
	}
}

func TestServer_NonAllowlistedRoute_Returns404(t *testing.T) {
	t.Parallel()
	fake := httptest.NewServer(&fakeArgosd{validToken: "tok"})
	t.Cleanup(fake.Close)
	gw := newTestGateway(t, fake.URL)
	h := gw.Handler()

	// GET /v1/admin/audit is not in the allowlist at all — no pattern matches.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/admin/audit", http.NoBody)
	req.Header.Set("Authorization", "Bearer tok")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 for non-allowlisted route", rr.Code)
	}
}

func TestServer_WrongMethodForRoute_Returns405(t *testing.T) {
	t.Parallel()
	fake := httptest.NewServer(&fakeArgosd{validToken: "tok"})
	t.Cleanup(fake.Close)
	gw := newTestGateway(t, fake.URL)
	h := gw.Handler()

	// GET /v1/clusters: POST is registered, GET is not → 405 from ServeMux.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/clusters", http.NoBody)
	req.Header.Set("Authorization", "Bearer tok")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	// ServeMux returns 405 when a path is registered but the method is not.
	if rr.Code != http.StatusMethodNotAllowed && rr.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404 or 405 for wrong method on registered path", rr.Code)
	}
}

func TestServer_MissingAuthorization_Returns401(t *testing.T) {
	t.Parallel()
	fake := httptest.NewServer(&fakeArgosd{validToken: "tok"})
	t.Cleanup(fake.Close)
	gw := newTestGateway(t, fake.URL)
	h := gw.Handler()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/pods", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 for missing Authorization", rr.Code)
	}
}

func TestServer_InvalidToken_Returns401(t *testing.T) {
	t.Parallel()
	const validToken = "argos_pat_aabbccdd_good"
	backend := &fakeArgosd{validToken: validToken}
	fake := httptest.NewServer(backend)
	t.Cleanup(fake.Close)

	gw := newTestGateway(t, fake.URL)
	h := gw.Handler()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/pods", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer argos_pat_deadbeef_wrongtoken")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 for invalid token", rr.Code)
	}
}

func TestServer_TokenRevokedMidRequest_CacheInvalidated(t *testing.T) {
	t.Parallel()
	const token = "argos_pat_aabbccdd_revoked"

	// First request: longue-vue says valid.
	callCount := 0
	var backend http.HandlerFunc = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == verifyPath {
			callCount++
			var body struct {
				Token string `json:"token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if callCount == 1 && body.Token == token {
				// First verify: accept.
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(verifyResponseBody{
					Valid:  true,
					Kind:   "token",
					Scopes: []string{"write"},
				}); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				return
			}
			// Second verify: reject (token was revoked).
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// The forwarded write returns 401 to signal revocation.
		w.WriteHeader(http.StatusUnauthorized)
	}
	fake := httptest.NewServer(backend)
	t.Cleanup(fake.Close)

	gw := newTestGateway(t, fake.URL)
	h := gw.Handler()

	// First request — token valid, upstream returns 401 (simulates revoke between cache+forward).
	req1 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/clusters", strings.NewReader(`{"name":"x"}`))
	req1.Header.Set("Authorization", "Bearer "+token)
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, req1)
	// Gateway forwards; upstream returns 401 → gateway should invalidate cache.
	// (Status 401 propagated from upstream.)
	if rr1.Code != http.StatusUnauthorized {
		t.Errorf("first request status = %d; want 401", rr1.Code)
	}

	// Second request — cache should be invalidated, so verifyCallCount goes to 2.
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/clusters", strings.NewReader(`{"name":"x"}`))
	req2.Header.Set("Authorization", "Bearer "+token)
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)

	// Both requests triggered a verify call (cache was invalidated after the first 401).
	if callCount < 2 {
		t.Errorf("verify called %d times; want >= 2 (cache should be invalidated on 401)", callCount)
	}
}

func TestServer_AdminRoutesBlocked(t *testing.T) {
	t.Parallel()
	fake := httptest.NewServer(&fakeArgosd{validToken: "tok"})
	t.Cleanup(fake.Close)
	gw := newTestGateway(t, fake.URL)
	h := gw.Handler()

	adminRoutes := []struct{ method, path string }{
		{http.MethodGet, "/v1/admin/users"},
		{http.MethodPost, "/v1/admin/users"},
		{http.MethodGet, "/v1/admin/tokens"},
		{http.MethodPatch, "/v1/admin/settings"},
	}
	for _, r := range adminRoutes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), r.method, r.path, http.NoBody)
			req.Header.Set("Authorization", "Bearer tok")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusNotFound {
				t.Errorf("%s %s: status = %d; want 404", r.method, r.path, rr.Code)
			}
		})
	}
}
