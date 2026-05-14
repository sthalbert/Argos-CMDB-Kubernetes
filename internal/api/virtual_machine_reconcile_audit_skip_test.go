package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleReconcileVirtualMachines_EmptyCallsSetAuditSkip exercises
// the audit no-op filter on the VM reconcile path: a reconcile that
// tombstones nothing (n == 0) MUST flip skip with reason
// "reconcile_empty". A reconcile that does tombstone something MUST
// leave skip clear.
func TestHandleReconcileVirtualMachines_EmptyCallsSetAuditSkip(t *testing.T) {
	resetCloudFake()
	store := newMemStore()
	enc := newTestEncrypter(t)

	muxAdmin := buildCloudMux(t, store, enc, adminCaller())
	rr := doReq(t, muxAdmin, http.MethodPost, "/v1/admin/cloud-accounts", map[string]any{
		"provider": "outscale", "name": "vm-rec-skip", "region": "eu-west-2",
		"access_key": "ak", "secret_key": "sk",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create cloud account: %d %q", rr.Code, rr.Body.String())
	}
	var acct CloudAccount
	if err := json.Unmarshal(rr.Body.Bytes(), &acct); err != nil {
		t.Fatalf("decode account: %v", err)
	}

	muxCol := buildCloudMux(t, store, enc, collectorCaller(&acct.ID))

	// Seed one VM so the "delete" branch has something to tombstone.
	rr = doReq(t, muxCol, http.MethodPost, "/v1/virtual-machines", map[string]any{
		"cloud_account_id": acct.ID,
		"provider_vm_id":   "i-rec-1",
		"name":             "vm-rec-1",
		"power_state":      "running",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("seed VM: %d %q", rr.Code, rr.Body.String())
	}

	post := func(label string, body map[string]any) *AuditBagTestView {
		t.Helper()
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("%s: marshal: %v", label, err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/virtual-machines/reconcile", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		ctx, bag := WithAuditBagForTest(req.Context())
		w := httptest.NewRecorder()
		muxCol.ServeHTTP(w, req.WithContext(ctx))
		if w.Code != http.StatusOK {
			t.Fatalf("%s POST /v1/virtual-machines/reconcile: %d %q", label, w.Code, w.Body.String())
		}
		return bag
	}

	// Empty path: keep the only VM → n == 0 → MUST skip.
	bagEmpty := post("empty", map[string]any{
		"cloud_account_id":     acct.ID,
		"keep_provider_vm_ids": []string{"i-rec-1"},
	})
	if !bagEmpty.SkipForTest() {
		t.Fatalf("empty reconcile (n==0) SHOULD skip")
	}
	if got := bagEmpty.SkipReasonForTest(); got != "reconcile_empty" {
		t.Fatalf("empty reconcile reason = %q, want %q", got, "reconcile_empty")
	}

	// Delete path: drop the VM from keep → n == 1 → MUST NOT skip.
	bagDel := post("delete", map[string]any{
		"cloud_account_id":     acct.ID,
		"keep_provider_vm_ids": []string{},
	})
	if bagDel.SkipForTest() {
		t.Fatalf("reconcile with tombstones (n>0) should NOT skip")
	}
}
