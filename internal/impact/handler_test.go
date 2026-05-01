//nolint:noctx // httptest.NewRequest is fine in unit tests.
package impact

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// withCaller injects a minimal authenticated caller so HandleImpact's
// 401 short-circuit doesn't fire. The role/scopes don't matter here —
// authorisation is enforced by the route's OpenAPI spec, not the
// hand-written handler.
func withCaller(r *http.Request) *http.Request {
	c := &auth.Caller{Kind: auth.CallerKindUser, UserID: uuid.New()}
	return r.WithContext(auth.WithCaller(r.Context(), c))
}

// newRequest builds a request with PathValue set the way Go 1.22's mux
// would have set it for /v1/impact/{entity_type}/{id}.
func newRequest(t *testing.T, entityType, id, query string) *http.Request {
	t.Helper()
	url := "/v1/impact/" + entityType + "/" + id
	if query != "" {
		url += "?" + query
	}
	r := httptest.NewRequest(http.MethodGet, url, http.NoBody)
	r.SetPathValue("entity_type", entityType)
	r.SetPathValue("id", id)
	return r
}

func TestHandleImpact_Unauthenticated(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	h := HandleImpact(store)

	r := httptest.NewRequest(http.MethodGet, "/v1/impact/cluster/"+uuid.New().String(), http.NoBody)
	r.SetPathValue("entity_type", "cluster")
	r.SetPathValue("id", uuid.New().String())
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401", w.Code)
	}
}

func TestHandleImpact_BadRequest(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	h := HandleImpact(store)

	tests := []struct {
		name       string
		entityType string
		id         string
		query      string
		wantStatus int
	}{
		{"invalid entity type", "blob", uuid.New().String(), "", http.StatusBadRequest},
		{"invalid uuid", "cluster", "not-a-uuid", "", http.StatusBadRequest},
		{"depth too high", "cluster", uuid.New().String(), "depth=99", http.StatusBadRequest},
		{"depth zero", "cluster", uuid.New().String(), "depth=0", http.StatusBadRequest},
		{"depth not integer", "cluster", uuid.New().String(), "depth=abc", http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := withCaller(newRequest(t, tc.entityType, tc.id, tc.query))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.wantStatus {
				t.Errorf("status = %d; want %d", w.Code, tc.wantStatus)
			}
		})
	}
}

func TestHandleImpact_OK(t *testing.T) {
	t.Parallel()
	f := newFixture()
	h := HandleImpact(f.store)

	r := withCaller(newRequest(t, "cluster", f.clusterID.String(), "depth=2"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", got)
	}

	var g Graph
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if g.Root.Type != TypeCluster {
		t.Errorf("root type = %s; want cluster", g.Root.Type)
	}
	if len(g.Nodes) == 0 {
		t.Error("expected non-empty nodes list")
	}
}

func TestHandleImpact_TraverserError(t *testing.T) {
	t.Parallel()
	f := newFixture()
	// Force the root fetch to fail with a non-NotFound error.
	f.store.errOn["GetCluster"] = context.DeadlineExceeded
	h := HandleImpact(f.store)

	r := withCaller(newRequest(t, "cluster", f.clusterID.String(), ""))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500", w.Code)
	}
}

func TestHandleImpact_DefaultDepth(t *testing.T) {
	// No depth param → defaultDepth=2 → reaches workloads.
	t.Parallel()
	f := newFixture()
	h := HandleImpact(f.store)

	r := withCaller(newRequest(t, "cluster", f.clusterID.String(), ""))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", w.Code, w.Body.String())
	}
	var g Graph
	_ = json.Unmarshal(w.Body.Bytes(), &g)
	found := false
	for _, n := range g.Nodes {
		if n.Type == TypeWorkload {
			found = true
			break
		}
	}
	if !found {
		t.Error("default depth should reach workloads (depth=2)")
	}
}
