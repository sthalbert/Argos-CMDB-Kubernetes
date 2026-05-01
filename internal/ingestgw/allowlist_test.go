package ingestgw

import (
	"net/http"
	"testing"
)

const testUUID = "550e8400-e29b-41d4-a716-446655440000"

func TestMatchAllowlist_Positive(t *testing.T) {
	t.Parallel()
	for _, r := range Routes {
		path := r.Pattern
		// Substitute the {uuid} placeholder with a valid UUID.
		if path == "/v1/clusters/{uuid}" {
			path = "/v1/clusters/" + testUUID
		}
		t.Run(r.Method+" "+path, func(t *testing.T) {
			t.Parallel()
			_, ok := matchAllowlist(r.Method, path)
			if !ok {
				t.Errorf("matchAllowlist(%q, %q) = false; want true", r.Method, path)
			}
		})
	}
}

func TestMatchAllowlist_Negative(t *testing.T) {
	t.Parallel()

	cases := []struct {
		method string
		path   string
		reason string
	}{
		// Wrong method for POST-only routes.
		{http.MethodGet, "/v1/clusters", "GET of POST-only route"},
		{http.MethodGet, "/v1/nodes", "GET of POST-only route"},
		{http.MethodGet, "/v1/namespaces", "GET of POST-only route"},
		{http.MethodGet, "/v1/pods", "GET of POST-only route"},
		{http.MethodGet, "/v1/workloads", "GET of POST-only route"},
		{http.MethodGet, "/v1/services", "GET of POST-only route"},
		{http.MethodGet, "/v1/ingresses", "GET of POST-only route"},
		{http.MethodGet, "/v1/persistentvolumes", "GET of POST-only route"},
		{http.MethodGet, "/v1/persistentvolumeclaims", "GET of POST-only route"},
		{http.MethodPut, "/v1/clusters/" + testUUID, "PUT instead of PATCH"},
		{http.MethodPost, "/v1/clusters/" + testUUID, "POST to PATCH-only UUID route"},
		{http.MethodDelete, "/v1/clusters/" + testUUID, "DELETE of PATCH route"},
		// Admin endpoints.
		{http.MethodGet, "/v1/admin/users", "admin users list"},
		{http.MethodPost, "/v1/admin/users", "admin user create"},
		{http.MethodGet, "/v1/admin/tokens", "admin tokens list"},
		{http.MethodPost, "/v1/admin/tokens", "admin token create"},
		{http.MethodGet, "/v1/admin/audit", "audit log"},
		{http.MethodPatch, "/v1/admin/settings", "settings PATCH"},
		// Auth endpoints not on gateway.
		{http.MethodPost, "/v1/auth/login", "login not forwarded"},
		{http.MethodPost, "/v1/auth/verify", "verify is longue-vue-internal"},
		// Read endpoints.
		{http.MethodGet, "/v1/clusters/" + testUUID, "GET cluster by id"},
		{http.MethodGet, "/v1/nodes", "GET nodes"},
		{http.MethodGet, "/v1/pods", "GET pods"},
		{http.MethodGet, "/v1/workloads", "GET workloads"},
		// Malformed UUID in PATCH path.
		{http.MethodPatch, "/v1/clusters/not-a-uuid", "non-UUID segment"},
		{http.MethodPatch, "/v1/clusters/550e8400", "short UUID"},
		{http.MethodPatch, "/v1/clusters/", "empty UUID segment"},
		// Path traversal payloads.
		{http.MethodPost, "/v1/pods/../admin/users", "traversal with .."},
		{http.MethodPost, "/v1/pods/%2e%2e/admin/users", "percent-encoded .."},
		{http.MethodPost, "/v1/pods/..%2fadmin/users", "mixed encoding"},
		// Bogus paths.
		{http.MethodPost, "/", "root path"},
		{http.MethodPost, "", "empty path"},
		{http.MethodPost, "/v1", "partial prefix"},
		{http.MethodGet, "/healthz", "healthz"},
		// Extra segments.
		{http.MethodPost, "/v1/nodes/extra", "extra segment after POST route"},
		{http.MethodPost, "/v1/nodes/reconcile/extra", "extra after reconcile"},
	}

	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			t.Parallel()
			_, ok := matchAllowlist(tc.method, tc.path)
			if ok {
				t.Errorf("matchAllowlist(%q, %q) = true; want false (%s)", tc.method, tc.path, tc.reason)
			}
		})
	}
}

func TestMatchAllowlist_ReturnsPattern(t *testing.T) {
	t.Parallel()

	t.Run("static route returns itself", func(t *testing.T) {
		t.Parallel()
		got, ok := matchAllowlist(http.MethodPost, "/v1/clusters")
		if !ok {
			t.Fatal("expected match")
		}
		if got != "/v1/clusters" {
			t.Errorf("pattern = %q; want /v1/clusters", got)
		}
	})

	t.Run("uuid route returns template", func(t *testing.T) {
		t.Parallel()
		got, ok := matchAllowlist(http.MethodPatch, "/v1/clusters/"+testUUID)
		if !ok {
			t.Fatal("expected match")
		}
		if got != "/v1/clusters/{uuid}" {
			t.Errorf("pattern = %q; want /v1/clusters/{uuid}", got)
		}
	})
}

func TestRouteCount(t *testing.T) {
	t.Parallel()
	// Routes contains exactly 18 write paths (no verify — that's longue-vue-internal).
	const wantRoutes = 18
	if len(Routes) != wantRoutes {
		t.Errorf("len(Routes) = %d; want %d", len(Routes), wantRoutes)
	}
}
