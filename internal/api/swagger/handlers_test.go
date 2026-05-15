package swagger_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api/swagger"
)

func TestOpenAPISpecHandler_servesSpec(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	swagger.OpenAPISpecHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("Content-Type = %q, want application/yaml", ct)
	}
	if !strings.HasPrefix(rec.Body.String(), "openapi: 3.1.0") {
		t.Errorf("body should start with 'openapi: 3.1.0', got: %q", rec.Body.String()[:30])
	}
}

func TestOpenAPISpecHandler_matchesSourceFile(t *testing.T) {
	// Source of truth lives at api/openapi/openapi.yaml. The embedded copy
	// must be byte-identical (enforced by `make swagger-sync-check` in CI,
	// double-checked here at test time).
	srcPath := filepath.Join("..", "..", "..", "api", "openapi", "openapi.yaml")
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read %s: %v", srcPath, err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	swagger.OpenAPISpecHandler().ServeHTTP(rec, req)

	if rec.Body.String() != string(src) {
		t.Fatal("embedded spec differs from api/openapi/openapi.yaml — run `make swagger-sync` and commit")
	}
}

func TestOpenAPISpecHandler_etagRevalidation(t *testing.T) {
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	swagger.OpenAPISpecHandler().ServeHTTP(rec1, req1)

	etag := rec1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag header missing on initial response")
	}
	if cc := rec1.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	req2.Header.Set("If-None-Match", etag)
	swagger.OpenAPISpecHandler().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotModified {
		t.Errorf("revalidation with matching ETag: status = %d, want 304", rec2.Code)
	}
	if rec2.Body.Len() != 0 {
		t.Errorf("304 response should have empty body, got %d bytes", rec2.Body.Len())
	}
}

func TestOpenAPISpecHandler_etagWildcard(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	req.Header.Set("If-None-Match", "*")
	swagger.OpenAPISpecHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Errorf("If-None-Match: * — status = %d, want 304 (RFC 7232 §3.2)", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 response should have empty body, got %d bytes", rec.Body.Len())
	}
}

func TestOpenAPISpecHandler_etagMismatch(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	req.Header.Set("If-None-Match", `"stale-etag-value"`)
	swagger.OpenAPISpecHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("mismatched If-None-Match — status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("mismatched If-None-Match should serve full body, got empty")
	}
}

func TestSwaggerUIHandler_servesIndexAtRoot(t *testing.T) {
	for _, path := range []string{"/", ""} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/"+strings.TrimPrefix(path, "/"), nil)
		swagger.SwaggerUIHandler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("path %q: status = %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("path %q: Content-Type = %q, want text/html", path, ct)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "swagger-ui") {
			t.Errorf("path %q: body should reference swagger-ui", path)
		}
		if !strings.Contains(body, "/openapi.yaml") {
			t.Errorf("path %q: body should reference /openapi.yaml", path)
		}
		if !strings.Contains(body, "longue-vue") {
			t.Errorf("path %q: body should reference longue-vue", path)
		}
	}
}

func TestSwaggerUIHandler_servesAssetsFromDist(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/swagger-ui-bundle.js", nil)
	swagger.SwaggerUIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want a javascript type", ct)
	}
}

func TestSwaggerUIHandler_returns404OnMissingAsset(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/does-not-exist.css", nil)
	swagger.SwaggerUIHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (no fallback to index — broken vendor copies must surface)", rec.Code)
	}
}
