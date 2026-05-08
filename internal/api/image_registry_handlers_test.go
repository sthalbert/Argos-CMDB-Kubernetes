package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleListImageRegistries(t *testing.T) {
	s := newMemStore()
	seedImageRegistriesForTest(t, s)

	h := HandleListImageRegistries(s)
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/image-versions/registries", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items, _ := body["items"].([]any)
	if len(items) != 7 {
		t.Fatalf("expected 7 defaults, got %d", len(items))
	}
}

func TestHandleCreateImageRegistry(t *testing.T) {
	s := newMemStore()
	h := HandleCreateImageRegistry(s)
	body, _ := json.Marshal(map[string]any{
		"hostname":           "mirror.example.com",
		"rate_limit_per_sec": 2.5,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/image-versions/registries", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleCreateImageRegistry_BadInput(t *testing.T) {
	s := newMemStore()
	h := HandleCreateImageRegistry(s)
	body, _ := json.Marshal(map[string]any{"hostname": ""})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/image-versions/registries", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", w.Code)
	}
}

func TestHandleCreateImageRegistry_Conflict(t *testing.T) {
	s := newMemStore()
	seedImageRegistriesForTest(t, s)
	h := HandleCreateImageRegistry(s)
	body, _ := json.Marshal(map[string]any{
		"hostname":           "docker.io", // already seeded
		"rate_limit_per_sec": 1.0,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/image-versions/registries", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleUpdateImageRegistry(t *testing.T) {
	s := newMemStore()
	seedImageRegistriesForTest(t, s)
	h := HandleUpdateImageRegistry(s)
	body, _ := json.Marshal(map[string]any{"rate_limit_per_sec": 0.5})
	req := httptest.NewRequest(http.MethodPatch, "/v1/admin/image-versions/registries/docker.io", bytes.NewReader(body))
	req.SetPathValue("hostname", "docker.io")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleUpdateImageRegistry_NotFound(t *testing.T) {
	s := newMemStore()
	h := HandleUpdateImageRegistry(s)
	body, _ := json.Marshal(map[string]any{"rate_limit_per_sec": 1.0})
	req := httptest.NewRequest(http.MethodPatch, "/v1/admin/image-versions/registries/missing.example.com", bytes.NewReader(body))
	req.SetPathValue("hostname", "missing.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", w.Code)
	}
}

func TestHandleDeleteImageRegistry(t *testing.T) {
	s := newMemStore()
	seedImageRegistriesForTest(t, s)
	h := HandleDeleteImageRegistry(s)
	req := httptest.NewRequest(http.MethodDelete, "/v1/admin/image-versions/registries/docker.io", nil)
	req.SetPathValue("hostname", "docker.io")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status: want 204, got %d", w.Code)
	}
}

// seedImageRegistriesForTest inserts the 7 default registries into the
// memStore for tests that need them.
func seedImageRegistriesForTest(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	seeds := []struct {
		host string
		rate float32
	}{
		{"docker.io", 1.0},
		{"ghcr.io", 5.0},
		{"quay.io", 5.0},
		{"gcr.io", 5.0},
		{"*-docker.pkg.dev", 5.0},
		{"registry.k8s.io", 5.0},
		{"public.ecr.aws", 5.0},
	}
	for _, sd := range seeds {
		if _, err := s.CreateImageRegistry(ctx, ImageRegistryUpsert{
			Hostname:        sd.host,
			RateLimitPerSec: sd.rate,
		}); err != nil {
			t.Fatalf("seed %s: %v", sd.host, err)
		}
	}
}
