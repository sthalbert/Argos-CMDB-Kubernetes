package api

// Handler-level tests for /v1/applications (ADR-0029 §2.3).
//
// Mirrors application_block_handlers_test.go: the seven Store methods
// for Application live as a real in-memory implementation in
// server_application_fake_test.go so these tests can drive the full CRUD
// surface (and the members + EOL stub endpoints) without a Postgres
// dependency.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// buildApplicationMux wires the eight hand-written routes that
// /v1/applications exposes (ADR-0029 §2.3).
//
// Routing note: GET /v1/applications/{id}/members and
// GET /v1/applications/by-name/{name} would conflict on a plain
// net/http.ServeMux (Go 1.22 wildcards: both are three-segment patterns
// and neither is more specific, so registering both panics). We
// side-step that by funneling `/v1/applications/by-name/...` through a
// dedicated sub-mux ahead of the {id}-rooted patterns. The production
// mux uses the same trick (see RegisterApplicationRoutes when it lands
// in Task 1.10).
func buildApplicationMux(t *testing.T, store Store, caller *auth.Caller) http.Handler {
	t.Helper()
	mux := http.NewServeMux()

	wrap := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if caller != nil {
				r = r.WithContext(auth.WithCaller(r.Context(), caller))
			}
			h.ServeHTTP(w, r)
		})
	}

	// /v1/applications/by-name/{name} cannot coexist with the {id}-rooted
	// routes on the same ServeMux. Mount it on its own mux so the literal
	// "by-name" prefix wins routing before the {id} patterns get a look.
	byNameMux := http.NewServeMux()
	byNameMux.Handle("GET /v1/applications/by-name/{name}", wrap(HandleGetApplicationByName(store)))

	idMux := http.NewServeMux()
	idMux.Handle("POST /v1/applications", wrap(HandleCreateApplication(store)))
	idMux.Handle("GET /v1/applications", wrap(HandleListApplications(store)))
	idMux.Handle("GET /v1/applications/{id}", wrap(HandleGetApplication(store)))
	idMux.Handle("PATCH /v1/applications/{id}", wrap(HandlePatchApplication(store)))
	idMux.Handle("DELETE /v1/applications/{id}", wrap(HandleDeleteApplication(store)))
	idMux.Handle("GET /v1/applications/{id}/members", wrap(HandleListApplicationMembers(store)))
	idMux.Handle("GET /v1/applications/{id}/eol", wrap(HandleGetApplicationEOL(store)))

	mux.Handle("/v1/applications/by-name/", byNameMux)
	mux.Handle("/v1/applications/", idMux)
	mux.Handle("/v1/applications", idMux)
	return mux
}

// resetApplicationFakes resets both the Application and ApplicationBlock
// in-memory state so each test starts clean. Block state is reset too
// because some Application tests resolve blocks by name.
func resetApplicationFakes() {
	resetApplicationBlockFake()
	resetApplicationFake()
}

func TestApplicationCreate_Editor(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{
		"name":        "Billing API",
		"description": "internal billing",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var app Application
	if err := json.Unmarshal(rr.Body.Bytes(), &app); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if app.Name != "billing-api" {
		t.Errorf("name = %q want kebab-case", app.Name)
	}
	if app.ID == uuid.Nil {
		t.Errorf("id is zero")
	}
}

func TestApplicationCreate_Viewer(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, viewerCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{"name": "billing"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationCreate_Admin(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, adminCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{"name": "admin-app"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationCreate_MissingName(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationCreate_DICTOutOfRange(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{
		"name":              "dict-bad",
		"sec_disponibilite": 5,
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "sec_disponibilite") {
		t.Errorf("body should mention axis: %s", rr.Body.String())
	}
}

func TestApplicationCreate_SecNotesTooLong(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{
		"name":      "notes-too-long",
		"sec_notes": strings.Repeat("x", 4097),
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationCreate_DuplicateIsIdempotent(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{"name": "idem-app"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("first create status=%d body=%q", rr.Code, rr.Body.String())
	}
	var first Application
	_ = json.Unmarshal(rr.Body.Bytes(), &first)

	rr = doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{"name": "idem-app"})
	if rr.Code != http.StatusOK {
		t.Fatalf("second create status=%d body=%q want 200", rr.Code, rr.Body.String())
	}
	var second Application
	_ = json.Unmarshal(rr.Body.Bytes(), &second)
	if first.ID != second.ID {
		t.Errorf("idempotent create returned different ids: %s vs %s", first.ID, second.ID)
	}
}

func TestApplicationCreate_WithBlockName(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	// Seed the block first.
	rr := doReq(t, h, http.MethodPost, "/v1/application-blocks", map[string]any{
		"name": "payments-block",
	})
	// /v1/application-blocks isn't mounted on this mux; seed via the
	// store directly.
	_ = rr
	blk, err := store.CreateApplicationBlock(t.Context(), ApplicationBlockCreate{Name: "payments-block"})
	if err != nil {
		t.Fatalf("seed block: %v", err)
	}

	blkName := "payments-block"
	rr = doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{
		"name":                   "block-resolved",
		"application_block_name": blkName,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var app Application
	if err := json.Unmarshal(rr.Body.Bytes(), &app); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if app.ApplicationBlockID == nil || *app.ApplicationBlockID != blk.ID {
		t.Errorf("application_block_id = %v want %v", app.ApplicationBlockID, blk.ID)
	}
}

func TestApplicationGet_OK(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{"name": "get-me"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var created Application
	_ = json.Unmarshal(rr.Body.Bytes(), &created)

	rr = doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/applications/%s", created.ID), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%q", rr.Code, rr.Body.String())
	}
	var got Application
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != created.ID || got.Name != created.Name {
		t.Errorf("get returned mismatched row: %+v vs %+v", got, created)
	}
}

func TestApplicationGet_NotFound(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/applications/%s", uuid.New()), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationGet_BadID(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodGet, "/v1/applications/not-a-uuid", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationGetByName_OK(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{"name": "By Name App"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var created Application
	_ = json.Unmarshal(rr.Body.Bytes(), &created)

	// The handler normalises the path value, so the raw human form also
	// resolves to the same row.
	rr = doReq(t, h, http.MethodGet, "/v1/applications/by-name/by-name-app", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get-by-name status=%d body=%q", rr.Code, rr.Body.String())
	}
	var got Application
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.ID != created.ID {
		t.Errorf("by-name returned %s want %s", got.ID, created.ID)
	}
}

func TestApplicationGetByName_NotFound(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodGet, "/v1/applications/by-name/nope", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationList_NameFilter(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	for _, name := range []string{"office-suite", "office-mobile", "data-platform"} {
		rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{"name": name})
		if rr.Code != http.StatusCreated {
			t.Fatalf("seed %q status=%d body=%q", name, rr.Code, rr.Body.String())
		}
	}

	rr := doReq(t, h, http.MethodGet, "/v1/applications?name=OFFICE", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%q", rr.Code, rr.Body.String())
	}
	var page struct {
		Items []Application `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("name filter: got %d items, want 2", len(page.Items))
	}
}

func TestApplicationList_DICTMinFilter(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	for _, in := range []map[string]any{
		{"name": "low-dict", "sec_disponibilite": 1},
		{"name": "mid-dict", "sec_integrite": 3},
		{"name": "high-dict", "sec_confidentialite": 4},
	} {
		rr := doReq(t, h, http.MethodPost, "/v1/applications", in)
		if rr.Code != http.StatusCreated {
			t.Fatalf("seed status=%d body=%q", rr.Code, rr.Body.String())
		}
	}

	rr := doReq(t, h, http.MethodGet, "/v1/applications?dict_min=3", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%q", rr.Code, rr.Body.String())
	}
	var page struct {
		Items []Application `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("dict_min=3 filter: got %d items, want 2", len(page.Items))
	}
}

func TestApplicationList_BadBlockID(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodGet, "/v1/applications?application_block_id=not-a-uuid", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationPatch_Editor(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{"name": "patch-me"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var app Application
	_ = json.Unmarshal(rr.Body.Bytes(), &app)

	rr = doReq(t, h, http.MethodPatch, fmt.Sprintf("/v1/applications/%s", app.ID),
		map[string]any{"notes": "patched", "sec_disponibilite": 2})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%q", rr.Code, rr.Body.String())
	}
	var updated Application
	_ = json.Unmarshal(rr.Body.Bytes(), &updated)
	if updated.Notes == nil || *updated.Notes != "patched" {
		t.Errorf("notes = %v", updated.Notes)
	}
	if updated.SecDisponibilite == nil || *updated.SecDisponibilite != 2 {
		t.Errorf("sec_disponibilite = %v", updated.SecDisponibilite)
	}
}

func TestApplicationPatch_Viewer(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()

	hEd := buildApplicationMux(t, store, editorCaller())
	rr := doReq(t, hEd, http.MethodPost, "/v1/applications", map[string]any{"name": "viewer-blocked"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var app Application
	_ = json.Unmarshal(rr.Body.Bytes(), &app)

	hV := buildApplicationMux(t, store, viewerCaller())
	rr = doReq(t, hV, http.MethodPatch, fmt.Sprintf("/v1/applications/%s", app.ID),
		map[string]any{"notes": "no"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationPatch_DICTValidation(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{"name": "patch-dict"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var app Application
	_ = json.Unmarshal(rr.Body.Bytes(), &app)

	rr = doReq(t, h, http.MethodPatch, fmt.Sprintf("/v1/applications/%s", app.ID),
		map[string]any{"sec_tracabilite": 9})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationDelete_Admin(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()

	hEd := buildApplicationMux(t, store, editorCaller())
	rr := doReq(t, hEd, http.MethodPost, "/v1/applications", map[string]any{"name": "delete-me"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var app Application
	_ = json.Unmarshal(rr.Body.Bytes(), &app)

	hAdmin := buildApplicationMux(t, store, adminCaller())
	rr = doReq(t, hAdmin, http.MethodDelete, fmt.Sprintf("/v1/applications/%s", app.ID), nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%q", rr.Code, rr.Body.String())
	}

	rr = doReq(t, hAdmin, http.MethodGet, fmt.Sprintf("/v1/applications/%s", app.ID), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("post-delete get: status=%d want 404", rr.Code)
	}
}

func TestApplicationDelete_Editor(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()

	hEd := buildApplicationMux(t, store, editorCaller())
	rr := doReq(t, hEd, http.MethodPost, "/v1/applications", map[string]any{"name": "editor-blocked"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var app Application
	_ = json.Unmarshal(rr.Body.Bytes(), &app)

	rr = doReq(t, hEd, http.MethodDelete, fmt.Sprintf("/v1/applications/%s", app.ID), nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationDelete_NotFound(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, adminCaller())

	rr := doReq(t, h, http.MethodDelete, fmt.Sprintf("/v1/applications/%s", uuid.New()), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationListMembers_Empty(t *testing.T) {
	resetApplicationFakes()
	store := newMemStore()
	h := buildApplicationMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/applications", map[string]any{"name": "no-members"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var app Application
	_ = json.Unmarshal(rr.Body.Bytes(), &app)

	rr = doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/applications/%s/members", app.ID), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("members status=%d body=%q", rr.Code, rr.Body.String())
	}
	var page struct {
		Items      []ApplicationMember `json:"items"`
		NextCursor string              `json:"next_cursor"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("expected 0 members, got %d", len(page.Items))
	}
	if page.NextCursor != "" {
		t.Errorf("next_cursor = %q, want empty", page.NextCursor)
	}
}

// applicationEOLResp mirrors the {items: []ApplicationEOLRow} body shape.
type applicationEOLResp struct {
	Items []struct {
		Product         string `json:"product"`
		Cycle           string `json:"cycle"`
		EOLStatus       string `json:"eol_status"`
		LatestAvailable string `json:"latest_available"`
		Sources         []struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"sources"`
	} `json:"items"`
}

// createTestApplication inserts an application via the fake store and
// returns its id. Used to seed members for the EOL aggregation tests.
func createTestApplication(t *testing.T, store Store, name string) uuid.UUID {
	t.Helper()
	app, err := store.CreateApplication(context.Background(), ApplicationCreate{Name: name})
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	return app.ID
}

func TestApplicationEOL_UnknownID_404(t *testing.T) {
	resetApplicationFakes()
	resetCloudFake()
	store := newMemStore()
	h := buildApplicationMux(t, store, viewerCaller())

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/applications/%s/eol", uuid.New()), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("content-type = %q, want application/problem+json", ct)
	}
}

// TestApplicationEOL_NoCaller_Forbidden asserts the read-scope guard:
// with no Caller in context the handler refuses with 403 (the upstream
// auth middleware turns a missing credential into 401 before reaching
// here; at the handler level a scopeless caller is Forbidden).
func TestApplicationEOL_NoCaller_Forbidden(t *testing.T) {
	resetApplicationFakes()
	resetCloudFake()
	store := newMemStore()
	h := buildApplicationMux(t, store, nil)

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/applications/%s/eol", uuid.New()), nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationEOL_ViewerOK_EmptyMembers(t *testing.T) {
	resetApplicationFakes()
	resetCloudFake()
	store := newMemStore()
	appID := createTestApplication(t, store, "vault")
	h := buildApplicationMux(t, store, viewerCaller())

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/applications/%s/eol", appID), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}
	var resp applicationEOLResp
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("expected 0 rows for memberless application, got %d", len(resp.Items))
	}
}

func TestApplicationEOL_MergesWorkloadAndVMAppEntry(t *testing.T) {
	resetApplicationFakes()
	resetCloudFake()
	store := newMemStore()
	appID := createTestApplication(t, store, "vault")

	// Seed a workload linked to the application carrying image-versions
	// enrichment for a "vault" container.
	wlID := uuid.New()
	latest := "1.18.2"
	behind := true
	nsName := "kube-prod"
	store.mu.Lock()
	store.workloadsByID[wlID] = Workload{
		Id:            &wlID,
		Name:          "vault",
		NamespaceName: &nsName,
		Kind:          "Deployment",
		ApplicationId: &appID,
		ContainersVersions: &map[string]ContainerVersionInfo{
			"vault": {LatestTag: &latest, IsBehind: &behind},
		},
	}
	store.mu.Unlock()

	// Seed a VM (NOT row-level linked) whose applications[] entry carries a
	// per-entry application_id == appID, plus the matching annotation.
	vmID := uuid.New()
	vmDisplay := "bastion-eu"
	cloudFake.mu.Lock()
	cloudFake.vms[vmID] = VirtualMachine{
		ID:          vmID,
		Name:        "i-abc",
		DisplayName: &vmDisplay,
		PowerState:  "running",
		Annotations: map[string]string{
			"longue-vue.io/eol.vault": `{"product":"vault","cycle":"1.13","eol_status":"eol","latest_available":"1.18.2","checked_at":"2026-05-15T00:00:00Z"}`,
		},
		Applications: []VMApplication{{Product: "vault", Version: "1.13", ApplicationID: &appID}},
	}
	cloudFake.mu.Unlock()

	h := buildApplicationMux(t, store, viewerCaller())
	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/applications/%s/eol", appID), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rr.Code, rr.Body.String())
	}
	var resp applicationEOLResp
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 merged row, got %d: %+v", len(resp.Items), resp.Items)
	}
	row := resp.Items[0]
	if row.Product != "vault" {
		t.Errorf("product = %q want vault", row.Product)
	}
	// The VM-app entry's real annotation (eol) should win over the
	// workload's image-versions "outdated".
	if row.EOLStatus != "eol" {
		t.Errorf("eol_status = %q want eol", row.EOLStatus)
	}
	if len(row.Sources) != 2 {
		t.Fatalf("expected 2 sources (workload + vm_application), got %d: %+v", len(row.Sources), row.Sources)
	}
	kinds := map[string]bool{}
	for _, s := range row.Sources {
		kinds[s.Kind] = true
	}
	if !kinds["workload"] || !kinds["vm_application"] {
		t.Errorf("sources kinds = %v want workload + vm_application", kinds)
	}
}
