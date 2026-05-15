package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api/swagger"
)

// TestSwaggerRoutes_unauthShellIsPublic confirms the /docs/ shell is
// reachable without credentials — matches the /ui/* precedent.
func TestSwaggerRoutes_unauthShellIsPublic(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /docs/", http.StripPrefix("/docs", swagger.UIHandler()))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/docs/", http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /docs/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
