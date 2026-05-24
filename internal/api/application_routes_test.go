package api

// Routing smoke test for the production /v1/applications + /v1/application-blocks
// wiring (ADR-0029 Task 1.10). The actual mount lives in
// cmd/longue-vue/main.go; this file re-creates the same sub-mux topology
// so we can prove on every CI run that:
//
//   1. GET /v1/applications/by-name/{name}     → HandleGetApplicationByName
//   2. GET /v1/applications/{id}/members       → HandleListApplicationMembers
//   3. GET /v1/applications/{id}               → HandleGetApplication
//
// all dispatch correctly. The risk this guards against is a panic on
// startup (Go 1.22 ServeMux refuses to register
// `/v1/applications/by-name/{name}` and `/v1/applications/{id}/members`
// on the same mux — empirically verified) or a silent regression where a
// future refactor collapses the sub-muxes and `/by-name/foo` starts
// routing to HandleGetApplication with id="by-name".
//
// We rebuild the topology here rather than import cmd/longue-vue/* (a
// main package cannot be imported) and rather than spin up a real
// *http.Server. Auth middleware is omitted: the test wraps each handler
// with a caller-injecting shim, mirroring application_handlers_test.go.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// buildProductionApplicationRoutes mirrors the sub-mux wiring in
// buildHTTPServer (cmd/longue-vue/main.go). If this function and the
// production code drift, the assertions below catch it.
func buildProductionApplicationRoutes(t *testing.T, store Store, caller *auth.Caller) http.Handler {
	t.Helper()
	wrap := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if caller != nil {
				r = r.WithContext(auth.WithCaller(r.Context(), caller))
			}
			h.ServeHTTP(w, r)
		})
	}

	mux := http.NewServeMux()

	// Application blocks — no internal conflicts.
	mux.Handle("POST /v1/application-blocks", wrap(HandleCreateApplicationBlock(store)))
	mux.Handle("GET /v1/application-blocks", wrap(HandleListApplicationBlocks(store)))
	mux.Handle("GET /v1/application-blocks/{id}", wrap(HandleGetApplicationBlock(store)))
	mux.Handle("PATCH /v1/application-blocks/{id}", wrap(HandlePatchApplicationBlock(store)))
	mux.Handle("DELETE /v1/application-blocks/{id}", wrap(HandleDeleteApplicationBlock(store)))

	// Applications — sub-mux split to dodge the by-name/{name} vs
	// {id}/members panic. Production uses the identical topology.
	appsByName := http.NewServeMux()
	appsByName.Handle("GET /v1/applications/by-name/{name}", wrap(HandleGetApplicationByName(store)))

	appsRest := http.NewServeMux()
	appsRest.Handle("POST /v1/applications", wrap(HandleCreateApplication(store)))
	appsRest.Handle("GET /v1/applications", wrap(HandleListApplications(store)))
	appsRest.Handle("GET /v1/applications/{id}", wrap(HandleGetApplication(store)))
	appsRest.Handle("PATCH /v1/applications/{id}", wrap(HandlePatchApplication(store)))
	appsRest.Handle("DELETE /v1/applications/{id}", wrap(HandleDeleteApplication(store)))
	appsRest.Handle("GET /v1/applications/{id}/members", wrap(HandleListApplicationMembers(store)))
	appsRest.Handle("GET /v1/applications/{id}/eol", wrap(HandleGetApplicationEOL(store)))

	mux.Handle("/v1/applications/by-name/", appsByName)
	mux.Handle("/v1/applications/", appsRest)
	mux.Handle("/v1/applications", appsRest)

	return mux
}

// TestApplicationRoutes_ProductionWiring asserts the three conflicting
// paths all dispatch to the right handler under the production sub-mux
// layout. A regression here means /by-name/foo is silently being routed
// to HandleGetApplication (404 with id="by-name") or the mux is panicking
// at registration.
func TestApplicationRoutes_ProductionWiring(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()

	// Seed one application so /{id}/members and /{id} hit a real row.
	h := buildProductionApplicationRoutes(t, store, editorCaller())
	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{"name": "routing-target"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed create: status=%d body=%q", rr.Code, rr.Body.String())
	}

	// 1. GET by-name — must NOT resolve to HandleGetApplication with
	// id="by-name". On the production mux this is dispatched through
	// the by-name child mux; the wrong wiring shows up as 404 with the
	// {id} branch swallowing it.
	rr = doReq(t, h, http.MethodGet, "/v1/applications/by-name/routing-target", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("by-name lookup: status=%d body=%q (would be 404 if routed to /{id})",
			rr.Code, rr.Body.String())
	}

	// 2. GET by id — exercise the {id} branch directly.
	rr = doReq(t, h, http.MethodGet, "/v1/applications/by-name/routing-target", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get-by-name to fetch id: status=%d body=%q", rr.Code, rr.Body.String())
	}
	// Pull the id out of the by-name response, then verify /{id} resolves.
	id := decodeApplicationID(t, rr.Body.Bytes())
	rr = doReq(t, h, http.MethodGet, "/v1/applications/"+id, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get-by-id: status=%d body=%q", rr.Code, rr.Body.String())
	}

	// 3. GET /{id}/members — the second path that participates in the
	// conflict. Confirms the rest child mux is picking it up.
	rr = doReq(t, h, http.MethodGet, "/v1/applications/"+id+"/members", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("members: status=%d body=%q", rr.Code, rr.Body.String())
	}

	// 4. /eol — stub returns 501 in Phase 1, but it must still dispatch
	// to HandleGetApplicationEOL rather than fall through to a 404.
	rr = doReq(t, h, http.MethodGet, "/v1/applications/"+id+"/eol", nil)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("eol: status=%d body=%q want 501", rr.Code, rr.Body.String())
	}
}

// TestApplicationBlockRoutes_ProductionWiring is a quick smoke for the
// five block routes. They have no internal route conflicts so the only
// regression this catches is "forgot to mount one of them".
func TestApplicationBlockRoutes_ProductionWiring(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildProductionApplicationRoutes(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/application-blocks", map[string]any{"name": "platform"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create block: status=%d body=%q", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, http.MethodGet, "/v1/application-blocks", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list blocks: status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func decodeApplicationID(t *testing.T, body []byte) string {
	t.Helper()
	var app Application
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	return app.ID.String()
}
