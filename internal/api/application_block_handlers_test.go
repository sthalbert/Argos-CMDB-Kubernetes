package api

// Handler-level tests for /v1/application-blocks (ADR-0029 §2.2).
//
// Fake-store strategy: the memStore stubs for the six ApplicationBlock*
// methods are upgraded to a real in-memory implementation in
// server_application_block_fake_test.go (mirrors the cloudFake pattern in
// server_cloud_fake_test.go). That lets these handler tests run without
// a Postgres dependency.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// buildApplicationBlockMux wires the five hand-written routes that
// /v1/application-blocks exposes (ADR-0029 §2.2). The wrap closure
// injects the test caller into context, matching the cloud handler test
// helper.
func buildApplicationBlockMux(t *testing.T, store Store, caller *auth.Caller) http.Handler {
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

	mux.Handle("POST /v1/application-blocks", wrap(HandleCreateApplicationBlock(store)))
	mux.Handle("GET /v1/application-blocks", wrap(HandleListApplicationBlocks(store)))
	mux.Handle("GET /v1/application-blocks/{id}", wrap(HandleGetApplicationBlock(store)))
	mux.Handle("PATCH /v1/application-blocks/{id}", wrap(HandlePatchApplicationBlock(store)))
	mux.Handle("DELETE /v1/application-blocks/{id}", wrap(HandleDeleteApplicationBlock(store)))
	return mux
}

func editorCaller() *auth.Caller {
	return &auth.Caller{
		Kind:     auth.CallerKindUser,
		UserID:   uuid.New(),
		Username: "editor",
		Role:     auth.RoleEditor,
		Scopes:   auth.ScopesForRole(auth.RoleEditor),
	}
}

func viewerCaller() *auth.Caller {
	return &auth.Caller{
		Kind:     auth.CallerKindUser,
		UserID:   uuid.New(),
		Username: "viewer",
		Role:     auth.RoleViewer,
		Scopes:   auth.ScopesForRole(auth.RoleViewer),
	}
}

func TestApplicationBlockCreate_Editor(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()
	h := buildApplicationBlockMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/application-blocks", map[string]any{
		"name":        "office-apps",
		"description": "office tools",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var blk ApplicationBlock
	if err := json.Unmarshal(rr.Body.Bytes(), &blk); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if blk.Name != "office-apps" {
		t.Errorf("name = %q", blk.Name)
	}
	if blk.Description == nil || *blk.Description != "office tools" {
		t.Errorf("description = %v", blk.Description)
	}
	if blk.ID == uuid.Nil {
		t.Errorf("id is zero")
	}
}

func TestApplicationBlockCreate_Viewer(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()
	h := buildApplicationBlockMux(t, store, viewerCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/application-blocks", map[string]any{
		"name": "office-apps",
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationBlockCreate_DuplicateIsIdempotent(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()
	h := buildApplicationBlockMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/application-blocks", map[string]any{
		"name": "billing-platform",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("first create status=%d body=%q", rr.Code, rr.Body.String())
	}
	var first ApplicationBlock
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first: %v", err)
	}

	rr = doReq(t, h, http.MethodPost, "/v1/application-blocks", map[string]any{
		"name": "billing-platform",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("second create status=%d body=%q want 200", rr.Code, rr.Body.String())
	}
	var second ApplicationBlock
	if err := json.Unmarshal(rr.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("idempotent create returned different ids: %s vs %s", first.ID, second.ID)
	}
}

func TestApplicationBlockCreate_MissingName(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()
	h := buildApplicationBlockMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/application-blocks", map[string]any{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationBlockGet_OK(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()
	h := buildApplicationBlockMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/application-blocks", map[string]any{
		"name": "data-platform",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var created ApplicationBlock
	_ = json.Unmarshal(rr.Body.Bytes(), &created)

	rr = doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/application-blocks/%s", created.ID), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%q", rr.Code, rr.Body.String())
	}
	var got ApplicationBlock
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != created.ID || got.Name != created.Name {
		t.Errorf("get returned mismatched row: %+v vs %+v", got, created)
	}
}

func TestApplicationBlockGet_NotFound(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()
	h := buildApplicationBlockMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodGet, fmt.Sprintf("/v1/application-blocks/%s", uuid.New()), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationBlockGet_BadID(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()
	h := buildApplicationBlockMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodGet, "/v1/application-blocks/not-a-uuid", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
	}
}

//nolint:gocyclo // two-page list test exercises cursor round-trip; splitting would obscure intent
func TestApplicationBlockList_OK(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()
	h := buildApplicationBlockMux(t, store, editorCaller())

	for _, name := range []string{"alpha-block", "beta-block", "gamma-block"} {
		rr := doReq(t, h, http.MethodPost, "/v1/application-blocks", map[string]any{"name": name})
		if rr.Code != http.StatusCreated {
			t.Fatalf("seed %q status=%d body=%q", name, rr.Code, rr.Body.String())
		}
	}

	// Page 1 (limit=2) → expect 2 items + next_cursor.
	rr := doReq(t, h, http.MethodGet, "/v1/application-blocks?limit=2", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%q", rr.Code, rr.Body.String())
	}
	var page struct {
		Items      []ApplicationBlock `json:"items"`
		NextCursor string             `json:"next_cursor"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("page 1: got %d items, want 2", len(page.Items))
	}
	if page.NextCursor == "" {
		t.Fatalf("page 1: next_cursor empty, expected continuation")
	}

	// Page 2 → 1 remaining, no next cursor.
	rr = doReq(t, h, http.MethodGet,
		fmt.Sprintf("/v1/application-blocks?limit=2&cursor=%s", page.NextCursor), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list page2 status=%d body=%q", rr.Code, rr.Body.String())
	}
	var page2 struct {
		Items      []ApplicationBlock `json:"items"`
		NextCursor string             `json:"next_cursor"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode page2: %v", err)
	}
	if len(page2.Items) != 1 {
		t.Fatalf("page 2: got %d items, want 1", len(page2.Items))
	}
	if page2.NextCursor != "" {
		t.Errorf("page 2: next_cursor=%q want empty", page2.NextCursor)
	}
}

func TestApplicationBlockList_NameFilter(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()
	h := buildApplicationBlockMux(t, store, editorCaller())

	for _, name := range []string{"office-suite", "office-mobile", "data-platform"} {
		rr := doReq(t, h, http.MethodPost, "/v1/application-blocks", map[string]any{"name": name})
		if rr.Code != http.StatusCreated {
			t.Fatalf("seed %q status=%d body=%q", name, rr.Code, rr.Body.String())
		}
	}

	// Case-insensitive filter on substring "OFFICE".
	rr := doReq(t, h, http.MethodGet, "/v1/application-blocks?name=OFFICE", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%q", rr.Code, rr.Body.String())
	}
	var page struct {
		Items []ApplicationBlock `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("name filter: got %d items, want 2", len(page.Items))
	}
	for _, item := range page.Items {
		if item.Name != "office-suite" && item.Name != "office-mobile" {
			t.Errorf("unexpected item %q", item.Name)
		}
	}
}

func TestApplicationBlockPatch_Editor(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()
	h := buildApplicationBlockMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/application-blocks", map[string]any{
		"name": "ops-block",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var blk ApplicationBlock
	_ = json.Unmarshal(rr.Body.Bytes(), &blk)

	rr = doReq(t, h, http.MethodPatch, fmt.Sprintf("/v1/application-blocks/%s", blk.ID),
		map[string]any{"notes": "Owned by SRE rotation"})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%q", rr.Code, rr.Body.String())
	}
	var updated ApplicationBlock
	if err := json.Unmarshal(rr.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if updated.Notes == nil || *updated.Notes != "Owned by SRE rotation" {
		t.Errorf("notes = %v", updated.Notes)
	}
}

func TestApplicationBlockPatch_Viewer(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()

	// Seed via editor so the row exists.
	hEd := buildApplicationBlockMux(t, store, editorCaller())
	rr := doReq(t, hEd, http.MethodPost, "/v1/application-blocks", map[string]any{
		"name": "ops-block",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var blk ApplicationBlock
	_ = json.Unmarshal(rr.Body.Bytes(), &blk)

	// Viewer attempts patch → 403.
	hV := buildApplicationBlockMux(t, store, viewerCaller())
	rr = doReq(t, hV, http.MethodPatch, fmt.Sprintf("/v1/application-blocks/%s", blk.ID),
		map[string]any{"notes": "should fail"})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationBlockPatch_NotFound(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()
	h := buildApplicationBlockMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPatch, fmt.Sprintf("/v1/application-blocks/%s", uuid.New()),
		map[string]any{"notes": "x"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationBlockDelete_Admin(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()

	hEd := buildApplicationBlockMux(t, store, editorCaller())
	rr := doReq(t, hEd, http.MethodPost, "/v1/application-blocks", map[string]any{
		"name": "to-delete",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var blk ApplicationBlock
	_ = json.Unmarshal(rr.Body.Bytes(), &blk)

	hAdmin := buildApplicationBlockMux(t, store, adminCaller())
	rr = doReq(t, hAdmin, http.MethodDelete, fmt.Sprintf("/v1/application-blocks/%s", blk.ID), nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%q", rr.Code, rr.Body.String())
	}

	// Verify the row is gone.
	rr = doReq(t, hAdmin, http.MethodGet, fmt.Sprintf("/v1/application-blocks/%s", blk.ID), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("post-delete get: status=%d want 404", rr.Code)
	}
}

func TestApplicationBlockDelete_Editor(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()

	hEd := buildApplicationBlockMux(t, store, editorCaller())
	rr := doReq(t, hEd, http.MethodPost, "/v1/application-blocks", map[string]any{
		"name": "editor-blocked",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed status=%d", rr.Code)
	}
	var blk ApplicationBlock
	_ = json.Unmarshal(rr.Body.Bytes(), &blk)

	// Editor tries to delete → 403 (admin-only).
	rr = doReq(t, hEd, http.MethodDelete, fmt.Sprintf("/v1/application-blocks/%s", blk.ID), nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestApplicationBlockDelete_NotFound(t *testing.T) {
	resetApplicationBlockFake()
	store := newMemStore()
	h := buildApplicationBlockMux(t, store, adminCaller())

	rr := doReq(t, h, http.MethodDelete, fmt.Sprintf("/v1/application-blocks/%s", uuid.New()), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%q", rr.Code, rr.Body.String())
	}
}
