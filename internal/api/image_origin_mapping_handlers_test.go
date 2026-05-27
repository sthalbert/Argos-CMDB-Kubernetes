package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// newTestServer returns a *Server backed by a fresh memStore for use in
// image-origin-mapping handler tests.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	return NewServer("test", newMemStore(), auth.SecureNever, nil, NewLoginRateLimiter(), NewVerifyRateLimiter())
}

// muxForTest wires the strict handler around srv and returns an http.Handler
// ready for httptest requests.
func muxForTest(t *testing.T, srv *Server) http.Handler {
	t.Helper()
	return newTestHandler(t, srv.store)
}

// helper: post a mapping via the strict-server handler.
func seedMapping(t *testing.T, srv *Server, imageName, registry string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"image_name": imageName, "public_registry": registry,
	})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/admin/image-origin-mappings", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	muxForTest(t, srv).ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed: want 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestImageOriginMappings_ListAndGet(t *testing.T) {
	resetMemOriginMappings()
	srv := newTestServer(t)
	mux := muxForTest(t, srv)

	// Seed two mappings.
	for _, p := range []struct{ n, r string }{
		{"grafana/alloy", "docker.io"},
		{"prometheus/prometheus", "quay.io"},
	} {
		seedMapping(t, srv, p.n, p.r)
	}

	// List.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/v1/admin/image-origin-mappings", http.NoBody)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var lst struct {
		Items []ImageOriginMapping `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&lst); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(lst.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(lst.Items))
	}

	// Get one.
	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/v1/admin/image-origin-mappings/grafana%2Falloy", http.NoBody)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d: %s", w.Code, w.Body.String())
	}

	// Get missing → 404.
	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/v1/admin/image-origin-mappings/does%2Fnot%2Fexist", http.NoBody)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("get missing: want 404, got %d", w.Code)
	}
}

func TestImageOriginMappings_Create(t *testing.T) {
	resetMemOriginMappings()
	srv := newTestServer(t)
	mux := muxForTest(t, srv)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
			"/v1/admin/image-origin-mappings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	t.Run("happy path", func(t *testing.T) {
		w := post(`{"image_name":"grafana/alloy","public_registry":"docker.io"}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		w := post(`{"image_name":"grafana/alloy","public_registry":"docker.io"}`)
		if w.Code != http.StatusConflict {
			t.Fatalf("want 409, got %d", w.Code)
		}
	})

	for _, tc := range []struct {
		name string
		body string
	}{
		{"missing image_name", `{"public_registry":"docker.io"}`},
		{"image_name with scheme", `{"image_name":"https://grafana/alloy","public_registry":"docker.io"}`},
		{"image_name with tag colon", `{"image_name":"grafana/alloy:v1","public_registry":"docker.io"}`},
		{"image_name with digest", `{"image_name":"grafana/alloy@sha256:abc","public_registry":"docker.io"}`},
		{"image_name with space", `{"image_name":"grafana /alloy","public_registry":"docker.io"}`},
		{"public_registry with slash", `{"image_name":"grafana/alloy","public_registry":"docker.io/lib"}`},
		{"public_registry empty", `{"image_name":"grafana/alloy","public_registry":""}`},
		{"public_registry bad shape", `{"image_name":"grafana/alloy","public_registry":"DOCKER.IO!"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetMemOriginMappings()
			w := post(tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("want 400 for %s, got %d: %s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestImageOriginMappings_PatchAndDelete(t *testing.T) {
	resetMemOriginMappings()
	srv := newTestServer(t)
	mux := muxForTest(t, srv)
	seedMapping(t, srv, "grafana/alloy", "docker.io")

	patch := func(name, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch,
			"/v1/admin/image-origin-mappings/"+name, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}

	t.Run("patch registry", func(t *testing.T) {
		w := patch("grafana%2Falloy", `{"public_registry":"ghcr.io"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
		}
		var got ImageOriginMapping
		_ = json.NewDecoder(w.Body).Decode(&got)
		if got.PublicRegistry != "ghcr.io" {
			t.Fatalf("want ghcr.io, got %q", got.PublicRegistry)
		}
	})

	t.Run("patch invalid registry → 400", func(t *testing.T) {
		w := patch("grafana%2Falloy", `{"public_registry":"docker.io/lib"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", w.Code)
		}
	})

	t.Run("patch missing → 404", func(t *testing.T) {
		w := patch("does%2Fnot%2Fexist", `{"public_registry":"ghcr.io"}`)
		if w.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d", w.Code)
		}
	})

	t.Run("delete", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete,
			"/v1/admin/image-origin-mappings/grafana%2Falloy", http.NoBody)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("want 204, got %d", w.Code)
		}
		req = httptest.NewRequestWithContext(t.Context(), http.MethodDelete,
			"/v1/admin/image-origin-mappings/grafana%2Falloy", http.NoBody)
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("want 404 on second delete, got %d", w.Code)
		}
	})
}
