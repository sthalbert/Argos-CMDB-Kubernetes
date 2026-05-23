package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testMirrorHostname is the placeholder hostname used as a mirror target
// across image-registry and origin-resolution tests. Extracted to satisfy
// goconst when reused across files in the api package.
const testMirrorHostname = "mirror.example.com"

// mustMarshal marshals v as JSON or fails the test. Tests build small,
// known-safe maps so propagating the error would only ever indicate a test
// programmer error.
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestHandleListImageRegistries(t *testing.T) {
	s := newMemStore()
	seedImageRegistriesForTest(t, s)

	h := HandleListImageRegistries(s)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/admin/image-versions/registries", http.NoBody)
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
	body := mustMarshal(t, map[string]any{
		"hostname":           testMirrorHostname,
		"rate_limit_per_sec": 2.5,
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/admin/image-versions/registries", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleCreateImageRegistry_BadInput(t *testing.T) {
	s := newMemStore()
	h := HandleCreateImageRegistry(s)
	body := mustMarshal(t, map[string]any{"hostname": ""})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/admin/image-versions/registries", bytes.NewReader(body))
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
	body := mustMarshal(t, map[string]any{
		"hostname":           "docker.io", // already seeded
		"rate_limit_per_sec": 1.0,
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/admin/image-versions/registries", bytes.NewReader(body))
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
	body := mustMarshal(t, map[string]any{"rate_limit_per_sec": 0.5})
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPatch,
		"/v1/admin/image-versions/registries/docker.io",
		bytes.NewReader(body),
	)
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
	body := mustMarshal(t, map[string]any{"rate_limit_per_sec": 1.0})
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPatch,
		"/v1/admin/image-versions/registries/missing.example.com",
		bytes.NewReader(body),
	)
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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/v1/admin/image-versions/registries/docker.io", http.NoBody)
	req.SetPathValue("hostname", "docker.io")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status: want 204, got %d", w.Code)
	}
}

func TestHandleGetImageRegistryCredentials_NotMirror(t *testing.T) {
	s := newMemStore()
	username := "robot$lv"
	token := "ignored"
	if _, err := s.CreateImageRegistry(context.Background(), ImageRegistryUpsert{
		Hostname:        "registry.example.com",
		PathPrefix:      "",
		RateLimitPerSec: 1.0,
		IsMirror:        false,
		AuthUsername:    &username,
		AuthToken:       &token,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := HandleGetImageRegistryCredentials(s)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/v1/admin/image-versions/registries/registry.example.com/_root/credentials", http.NoBody)
	req.SetPathValue("hostname", "registry.example.com")
	req.SetPathValue("path_prefix", "_root")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleGetImageRegistryCredentials_Mirror(t *testing.T) {
	s := newMemStore()
	username := "robot$lv"
	token := "s3cret"
	if _, err := s.CreateImageRegistry(context.Background(), ImageRegistryUpsert{
		Hostname:        "ma-registry.io",
		PathPrefix:      "container/",
		RateLimitPerSec: 2.0,
		IsMirror:        true,
		AuthUsername:    &username,
		AuthToken:       &token,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := HandleGetImageRegistryCredentials(s)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/v1/admin/image-versions/registries/ma-registry.io/container%2F/credentials", http.NoBody)
	req.SetPathValue("hostname", "ma-registry.io")
	req.SetPathValue("path_prefix", "container/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["auth_username"] != username {
		t.Errorf("auth_username=%q want %q", body["auth_username"], username)
	}
	if body["auth_token"] == "" {
		t.Errorf("auth_token unexpectedly empty")
	}
}

func TestHandleGetImageRegistryCredentials_NotFound(t *testing.T) {
	s := newMemStore()
	h := HandleGetImageRegistryCredentials(s)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/v1/admin/image-versions/registries/missing/_root/credentials", http.NoBody)
	req.SetPathValue("hostname", "missing")
	req.SetPathValue("path_prefix", "_root")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", w.Code)
	}
}

func TestHandleCreateImageRegistry_ReplicaValidationOk(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	// Seed the annotation mirror that the replica will point at.
	if _, err := m.CreateImageRegistry(context.Background(), ImageRegistryUpsert{
		Hostname: testMirrorHostname, PathPrefix: "container/",
		RateLimitPerSec: 5, IsMirror: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	target := testMirrorHostname
	body := mustMarshal(t, ImageRegistryUpsert{
		Hostname: "local.example.com", PathPrefix: "container/",
		RateLimitPerSec: 5, IsMirror: true,
		ReplicatesFromHostname: &target,
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/v1/admin/image-versions/registries", bytes.NewReader(body))
	w := httptest.NewRecorder()
	HandleCreateImageRegistry(m).ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestHandleCreateImageRegistry_ReplicaTargetMissing(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	target := "nope.example.com"
	body := mustMarshal(t, ImageRegistryUpsert{
		Hostname: "local.example.com", PathPrefix: "container/",
		RateLimitPerSec: 5, IsMirror: true,
		ReplicatesFromHostname: &target,
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/v1/admin/image-versions/registries", bytes.NewReader(body))
	w := httptest.NewRecorder()
	HandleCreateImageRegistry(m).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing target, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestHandleCreateImageRegistry_ReplicaTargetIsAlsoReplica(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	other := "outer.example.com"
	// Seed midway as a replica of "outer" so it itself is a replica.
	if _, err := m.CreateImageRegistry(context.Background(), ImageRegistryUpsert{
		Hostname: "outer.example.com", PathPrefix: "",
		RateLimitPerSec: 5, IsMirror: true,
	}); err != nil {
		t.Fatalf("seed outer: %v", err)
	}
	if _, err := m.CreateImageRegistry(context.Background(), ImageRegistryUpsert{
		Hostname: "midway.example.com", PathPrefix: "",
		RateLimitPerSec: 5, IsMirror: true,
		ReplicatesFromHostname: &other,
	}); err != nil {
		t.Fatalf("seed midway: %v", err)
	}
	target := "midway.example.com"
	body := mustMarshal(t, ImageRegistryUpsert{
		Hostname: "local.example.com", PathPrefix: "",
		RateLimitPerSec: 5, IsMirror: true,
		ReplicatesFromHostname: &target,
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/v1/admin/image-versions/registries", bytes.NewReader(body))
	w := httptest.NewRecorder()
	HandleCreateImageRegistry(m).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for replica-of-replica, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestHandleCreateImageRegistry_ReplicaRequiresMirror(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	if _, err := m.CreateImageRegistry(context.Background(), ImageRegistryUpsert{
		Hostname: testMirrorHostname, PathPrefix: "",
		RateLimitPerSec: 5, IsMirror: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	target := testMirrorHostname
	body := mustMarshal(t, ImageRegistryUpsert{
		Hostname: "public.example.com", PathPrefix: "",
		RateLimitPerSec: 5, IsMirror: false,
		ReplicatesFromHostname: &target,
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/v1/admin/image-versions/registries", bytes.NewReader(body))
	w := httptest.NewRecorder()
	HandleCreateImageRegistry(m).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (is_mirror=false), got %d (%s)", w.Code, w.Body.String())
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
