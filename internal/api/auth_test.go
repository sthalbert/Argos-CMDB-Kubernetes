package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBearerAuth(t *testing.T) {
	t.Parallel()

	const token = "s3cret-token"
	passthrough := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := BearerAuth(token)(passthrough)

	tests := []struct {
		name        string
		path        string
		authHeader  string
		wantStatus  int
		wantWWWAuth bool
	}{
		{"healthz open without auth", "/healthz", "", http.StatusOK, false},
		{"readyz open without auth", "/readyz", "", http.StatusOK, false},
		{"missing header rejected", "/v1/clusters", "", http.StatusUnauthorized, true},
		{"wrong scheme rejected", "/v1/clusters", "Basic abc", http.StatusUnauthorized, true},
		{"empty bearer rejected", "/v1/clusters", "Bearer ", http.StatusUnauthorized, true},
		{"bad token rejected", "/v1/clusters", "Bearer wrong-token", http.StatusUnauthorized, true},
		{"correct token allowed", "/v1/clusters", "Bearer " + token, http.StatusOK, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := httptest.NewRecorder()
			wrapped.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status=%d, want=%d body=%q", rr.Code, tt.wantStatus, rr.Body.String())
			}
			gotWWW := rr.Header().Get("WWW-Authenticate") != ""
			if gotWWW != tt.wantWWWAuth {
				t.Errorf("WWW-Authenticate presence=%v, want=%v", gotWWW, tt.wantWWWAuth)
			}
		})
	}
}

// TestBearerAuthWithServer verifies the middleware composes correctly with
// the generated handler chain and keeps health probes unauthenticated.
func TestBearerAuthWithServer(t *testing.T) {
	t.Parallel()
	const token = "srv-token"
	base := Handler(NewServer("test", newMemStore()))
	h := BearerAuth(token)(base)

	cases := []struct {
		name       string
		method     string
		path       string
		header     string
		wantStatus int
	}{
		{"authorized list", http.MethodGet, "/v1/clusters", "Bearer " + token, http.StatusOK},
		{"unauthorized list", http.MethodGet, "/v1/clusters", "", http.StatusUnauthorized},
		{"healthz open", http.MethodGet, "/healthz", "", http.StatusOK},
		{"readyz open", http.MethodGet, "/readyz", "", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.wantStatus {
				t.Errorf("status=%d, want=%d body=%q", rr.Code, tc.wantStatus, rr.Body.String())
			}
		})
	}
}
