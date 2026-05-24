package api

// Handler-level tests for the virtual_machine → application linking
// surface (ADR-0029 §2.3 / §2.4). Exercises the hand-written PATCH and
// LIST handlers on top of the in-memory memStore so the tests run
// without a Postgres dependency.
//
// The contract under test, summarised:
//   - PATCH with application_id sets the row-level link.
//   - PATCH with application_name resolves server-side.
//   - PATCH with both → id wins (mirrors ADR-0019 cloud_account precedence).
//   - PATCH with an unknown id or name → 400.
//   - PATCH with applications[] entries carrying application_name resolves
//     each entry's name to an id; unknown name → 400.
//   - LIST with application_id / application_name filters returns the
//     expected subset.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// seedVMLinkFixtures creates a cloud account + two VMs + two applications
// so individual cases can drive the precedence and filter matrices.
// Returns (vmA, vmB, primaryApp, secondaryApp). The cloud account itself
// is internal scaffolding only — callers operate on the VMs.
func seedVMLinkFixtures(t *testing.T, store Store) (
	vmA, vmB VirtualMachine,
	primary, secondary Application,
) {
	t.Helper()
	resetCloudFake()
	resetApplicationFake()
	ctx := context.Background()

	acct, err := store.UpsertCloudAccount(ctx, CloudAccountUpsert{
		Provider: "outscale", Name: "vm-link", Region: "eu-west-2",
	})
	if err != nil {
		t.Fatalf("seed cloud account: %v", err)
	}
	vmA, _, err = store.UpsertVirtualMachine(ctx, VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-vmlink-a000",
		Name:           "vm-a",
		PowerState:     "running",
		Ready:          true,
	})
	if err != nil {
		t.Fatalf("seed vm a: %v", err)
	}
	vmB, _, err = store.UpsertVirtualMachine(ctx, VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-vmlink-b000",
		Name:           "vm-b",
		PowerState:     "running",
		Ready:          true,
	})
	if err != nil {
		t.Fatalf("seed vm b: %v", err)
	}
	primary, err = store.CreateApplication(ctx, ApplicationCreate{Name: "billing"})
	if err != nil {
		t.Fatalf("seed application primary: %v", err)
	}
	secondary, err = store.CreateApplication(ctx, ApplicationCreate{Name: "checkout"})
	if err != nil {
		t.Fatalf("seed application secondary: %v", err)
	}
	return vmA, vmB, primary, secondary
}

func TestPatchVirtualMachine_LinkByName(t *testing.T) {
	store := newMemStore()
	enc := newTestEncrypter(t)
	vmA, _, primary, _ := seedVMLinkFixtures(t, store)
	mux := buildCloudMux(t, store, enc, adminCaller())

	body := map[string]any{"application_name": primary.Name}
	rr := doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID), body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var vm VirtualMachine
	if err := json.Unmarshal(rr.Body.Bytes(), &vm); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if vm.ApplicationID == nil || *vm.ApplicationID != primary.ID {
		t.Fatalf("expected application_id=%v, got %v", primary.ID, vm.ApplicationID)
	}
}

func TestPatchVirtualMachine_UnknownNameIs400(t *testing.T) {
	store := newMemStore()
	enc := newTestEncrypter(t)
	vmA, _, _, _ := seedVMLinkFixtures(t, store)
	mux := buildCloudMux(t, store, enc, adminCaller())

	body := map[string]any{"application_name": "no-such-application"}
	rr := doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID), body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown application_name, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestPatchVirtualMachine_IDBeatsName(t *testing.T) {
	store := newMemStore()
	enc := newTestEncrypter(t)
	vmA, _, primary, secondary := seedVMLinkFixtures(t, store)
	mux := buildCloudMux(t, store, enc, adminCaller())

	// id points at primary, name points at secondary. id must win.
	body := map[string]any{
		"application_id":   primary.ID.String(),
		"application_name": secondary.Name,
	}
	rr := doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID), body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var vm VirtualMachine
	if err := json.Unmarshal(rr.Body.Bytes(), &vm); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if vm.ApplicationID == nil || *vm.ApplicationID != primary.ID {
		t.Fatalf("expected primary id %v to win, got %v", primary.ID, vm.ApplicationID)
	}
}

// TestPatchVirtualMachine_UnlinkClearsLink confirms an explicit
// `"application_id": null` clears the row-level link (ADR-0029 §2.3).
func TestPatchVirtualMachine_UnlinkClearsLink(t *testing.T) {
	store := newMemStore()
	enc := newTestEncrypter(t)
	vmA, _, primary, _ := seedVMLinkFixtures(t, store)
	mux := buildCloudMux(t, store, enc, adminCaller())

	// Link it first.
	rr := doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID),
		map[string]any{"application_id": primary.ID.String()})
	if rr.Code != http.StatusOK {
		t.Fatalf("seed link: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Now unlink with explicit null.
	rr = doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID),
		map[string]any{"application_id": nil})
	if rr.Code != http.StatusOK {
		t.Fatalf("unlink: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var vm VirtualMachine
	if err := json.Unmarshal(rr.Body.Bytes(), &vm); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if vm.ApplicationID != nil {
		t.Fatalf("unlink failed: application_id=%v, want nil", *vm.ApplicationID)
	}
}

// TestPatchVirtualMachine_AbsentKeyLeavesLink confirms a PATCH that omits
// application_id (touching a different field) leaves the link unchanged.
func TestPatchVirtualMachine_AbsentKeyLeavesLink(t *testing.T) {
	store := newMemStore()
	enc := newTestEncrypter(t)
	vmA, _, primary, _ := seedVMLinkFixtures(t, store)
	mux := buildCloudMux(t, store, enc, adminCaller())

	rr := doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID),
		map[string]any{"application_id": primary.ID.String()})
	if rr.Code != http.StatusOK {
		t.Fatalf("seed link: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// PATCH an unrelated field; the link must survive.
	rr = doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID),
		map[string]any{"owner": "team-x"})
	if rr.Code != http.StatusOK {
		t.Fatalf("owner patch: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var vm VirtualMachine
	if err := json.Unmarshal(rr.Body.Bytes(), &vm); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if vm.ApplicationID == nil || *vm.ApplicationID != primary.ID {
		t.Fatalf("link should survive a non-link patch: application_id=%v", vm.ApplicationID)
	}
}

func TestPatchVirtualMachine_PerEntryNameResolves(t *testing.T) {
	store := newMemStore()
	enc := newTestEncrypter(t)
	vmA, _, primary, secondary := seedVMLinkFixtures(t, store)
	mux := buildCloudMux(t, store, enc, adminCaller())

	// Two entries — first uses application_name (handler resolves), second
	// uses application_id directly. Both must end up with the right id and
	// no application_name in the persisted shape.
	body := map[string]any{
		"applications": []map[string]any{
			{"product": "vault", "version": "1.15.4", "application_name": primary.Name},
			{"product": "bind", "version": "9.18", "application_id": secondary.ID.String()},
		},
	}
	rr := doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID), body)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var vm VirtualMachine
	if err := json.Unmarshal(rr.Body.Bytes(), &vm); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(vm.Applications) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(vm.Applications))
	}
	if vm.Applications[0].ApplicationID == nil || *vm.Applications[0].ApplicationID != primary.ID {
		t.Fatalf("vault entry: expected application_id=%v, got %v",
			primary.ID, vm.Applications[0].ApplicationID)
	}
	if vm.Applications[0].ApplicationName != nil {
		t.Fatalf("vault entry: application_name should be stripped, got %q", *vm.Applications[0].ApplicationName)
	}
	if vm.Applications[1].ApplicationID == nil || *vm.Applications[1].ApplicationID != secondary.ID {
		t.Fatalf("bind entry: expected application_id=%v, got %v",
			secondary.ID, vm.Applications[1].ApplicationID)
	}
}

func TestPatchVirtualMachine_PerEntryUnknownNameIs400(t *testing.T) {
	store := newMemStore()
	enc := newTestEncrypter(t)
	vmA, _, _, _ := seedVMLinkFixtures(t, store)
	mux := buildCloudMux(t, store, enc, adminCaller())

	body := map[string]any{
		"applications": []map[string]any{
			{"product": "vault", "version": "1.15.4", "application_name": "no-such-application"},
		},
	}
	rr := doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID), body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestPatchVirtualMachine_PerEntryUnknownIDIs400(t *testing.T) {
	store := newMemStore()
	enc := newTestEncrypter(t)
	vmA, _, _, _ := seedVMLinkFixtures(t, store)
	mux := buildCloudMux(t, store, enc, adminCaller())

	body := map[string]any{
		"applications": []map[string]any{
			{"product": "vault", "version": "1.15.4", "application_id": uuid.New().String()},
		},
	}
	rr := doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID), body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestListVirtualMachines_FilterByApplicationID(t *testing.T) {
	store := newMemStore()
	enc := newTestEncrypter(t)
	vmA, _, primary, _ := seedVMLinkFixtures(t, store)
	mux := buildCloudMux(t, store, enc, adminCaller())

	// Link vmA to primary; vmB stays unlinked.
	linkBody := map[string]any{"application_id": primary.ID.String()}
	if rr := doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID), linkBody); rr.Code != http.StatusOK {
		t.Fatalf("seed link: %d %s", rr.Code, rr.Body.String())
	}

	rr := doReq(t, mux, http.MethodGet,
		fmt.Sprintf("/v1/virtual-machines?application_id=%s", primary.ID), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Items []VirtualMachine `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != vmA.ID {
		t.Fatalf("expected 1 item (vmA), got %d items: %+v", len(resp.Items), resp.Items)
	}
}

func TestListVirtualMachines_FilterByApplicationName(t *testing.T) {
	store := newMemStore()
	enc := newTestEncrypter(t)
	vmA, _, primary, _ := seedVMLinkFixtures(t, store)
	mux := buildCloudMux(t, store, enc, adminCaller())

	linkBody := map[string]any{"application_id": primary.ID.String()}
	if rr := doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID), linkBody); rr.Code != http.StatusOK {
		t.Fatalf("seed link: %d %s", rr.Code, rr.Body.String())
	}

	rr := doReq(t, mux, http.MethodGet,
		fmt.Sprintf("/v1/virtual-machines?application_name=%s", primary.Name), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Items []VirtualMachine `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != vmA.ID {
		t.Fatalf("expected 1 item (vmA), got %d items: %+v", len(resp.Items), resp.Items)
	}
}

func TestListVirtualMachines_FilterUnlinked(t *testing.T) {
	store := newMemStore()
	enc := newTestEncrypter(t)
	vmA, vmB, primary, _ := seedVMLinkFixtures(t, store)
	mux := buildCloudMux(t, store, enc, adminCaller())

	linkBody := map[string]any{"application_id": primary.ID.String()}
	if rr := doReq(t, mux, http.MethodPatch, fmt.Sprintf("/v1/virtual-machines/%s", vmA.ID), linkBody); rr.Code != http.StatusOK {
		t.Fatalf("seed link: %d %s", rr.Code, rr.Body.String())
	}

	rr := doReq(t, mux, http.MethodGet, "/v1/virtual-machines?unlinked=true", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Items []VirtualMachine `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != vmB.ID {
		t.Fatalf("expected 1 item (vmB), got %d items: %+v", len(resp.Items), resp.Items)
	}
}

func TestListVirtualMachines_InvalidApplicationID(t *testing.T) {
	store := newMemStore()
	enc := newTestEncrypter(t)
	_, _, _, _ = seedVMLinkFixtures(t, store)
	mux := buildCloudMux(t, store, enc, adminCaller())

	rr := doReq(t, mux, http.MethodGet, "/v1/virtual-machines?application_id=not-a-uuid", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed application_id, got %d", rr.Code)
	}
}
