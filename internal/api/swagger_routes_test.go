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
