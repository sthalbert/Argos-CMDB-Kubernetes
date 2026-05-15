//nolint:noctx // unit tests for shouldAudit use httptest.NewRequest for brevity; context is unused by the tested function
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShouldAudit_AllowlistsExtractRoutes(t *testing.T) {
	cases := []string{
		"/v1/search/extract",
		"/v1/search/extract.zip",
		"/v1/eol/extract",
	}
	for _, path := range cases {
		req := httptest.NewRequest("GET", path, http.NoBody)
		if !shouldAudit(req) {
			t.Errorf("shouldAudit(%q) = false, want true", path)
		}
	}
}

func TestShouldAudit_DoesNotAllowlistOtherSearchPaths(t *testing.T) {
	for _, path := range []string{"/v1/search", "/v1/search/something"} {
		req := httptest.NewRequest("GET", path, http.NoBody)
		if shouldAudit(req) {
			t.Errorf("shouldAudit(%q) = true, want false", path)
		}
	}
}
