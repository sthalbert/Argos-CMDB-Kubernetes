package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestHandler(t *testing.T, version string) http.Handler {
	t.Helper()
	return Handler(NewServer(version))
}

func TestServerRoutes(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, "test")

	tests := []struct {
		name       string
		method     string
		target     string
		body       string
		wantStatus int
		wantCT     string
	}{
		{
			name:       "healthz ok",
			method:     http.MethodGet,
			target:     "/healthz",
			wantStatus: http.StatusOK,
			wantCT:     "application/json",
		},
		{
			name:       "readyz ok",
			method:     http.MethodGet,
			target:     "/readyz",
			wantStatus: http.StatusOK,
			wantCT:     "application/json",
		},
		{
			name:       "list clusters stubbed",
			method:     http.MethodGet,
			target:     "/v1/clusters",
			wantStatus: http.StatusNotImplemented,
			wantCT:     "application/problem+json",
		},
		{
			name:       "create cluster stubbed",
			method:     http.MethodPost,
			target:     "/v1/clusters",
			body:       `{"name":"prod"}`,
			wantStatus: http.StatusNotImplemented,
			wantCT:     "application/problem+json",
		},
		{
			name:       "get cluster stubbed",
			method:     http.MethodGet,
			target:     "/v1/clusters/00000000-0000-0000-0000-000000000000",
			wantStatus: http.StatusNotImplemented,
			wantCT:     "application/problem+json",
		},
		{
			name:       "patch cluster stubbed",
			method:     http.MethodPatch,
			target:     "/v1/clusters/00000000-0000-0000-0000-000000000000",
			body:       `{"environment":"prod"}`,
			wantStatus: http.StatusNotImplemented,
			wantCT:     "application/problem+json",
		},
		{
			name:       "delete cluster stubbed",
			method:     http.MethodDelete,
			target:     "/v1/clusters/00000000-0000-0000-0000-000000000000",
			wantStatus: http.StatusNotImplemented,
			wantCT:     "application/problem+json",
		},
		{
			name:       "unknown route 404",
			method:     http.MethodGet,
			target:     "/does-not-exist",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var body *strings.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(tt.method, tt.target, body)
			if tt.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d; body=%q", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if tt.wantCT != "" {
				if ct := rr.Header().Get("Content-Type"); ct != tt.wantCT {
					t.Errorf("Content-Type = %q, want %q", ct, tt.wantCT)
				}
			}
		})
	}
}

func TestHealthzBody(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, "v1.2.3")

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	var got Health
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != Ok {
		t.Errorf("Status = %q, want %q", got.Status, Ok)
	}
	if got.Version == nil || *got.Version != "v1.2.3" {
		t.Errorf("Version = %v, want pointer to \"v1.2.3\"", got.Version)
	}
}

func TestProblemResponseShape(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, "test")

	req := httptest.NewRequest(http.MethodGet, "/v1/clusters", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	var got Problem
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != http.StatusNotImplemented {
		t.Errorf("Status = %d, want %d", got.Status, http.StatusNotImplemented)
	}
	if got.Title == "" {
		t.Error("Title is empty")
	}
	if got.Type != "about:blank" {
		t.Errorf("Type = %q, want %q", got.Type, "about:blank")
	}
}
