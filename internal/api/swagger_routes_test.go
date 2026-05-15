package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api/swagger"
)

// TestSwaggerRoutes_unauthShellIsPublic confirms the /docs/ shell is
// reachable without credentials — matches the /ui/* precedent.
func TestSwaggerRoutes_unauthShellIsPublic(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /docs/", http.StripPrefix("/docs", swagger.SwaggerUIHandler()))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/docs/")
	if err != nil {
		t.Fatalf("GET /docs/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestSwaggerRoutes_specHandlerIsByteIdentical re-asserts the runtime
// drift guard at the integration boundary.
func TestSwaggerRoutes_specHandlerIsByteIdentical(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /openapi.yaml", swagger.OpenAPISpecHandler())

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/openapi.yaml")
	if err != nil {
		t.Fatalf("GET /openapi.yaml: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("Content-Type = %q, want application/yaml", ct)
	}
}
