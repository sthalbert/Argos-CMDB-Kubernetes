package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleUpsertVirtualMachine_NoOpCallsSetAuditSkip exercises the
// audit no-op filter wiring on the VM upsert path: a brand-new POST
// must NOT mark the audit bag skip, an identical replay (same business
// fields, store classifies NoChange) MUST flip skip=true, and a real
// mutation MUST clear skip again.
func TestHandleUpsertVirtualMachine_NoOpCallsSetAuditSkip(t *testing.T) {
	resetCloudFake()
	store := newMemStore()
	enc := newTestEncrypter(t)

	muxAdmin := buildCloudMux(t, store, enc, adminCaller())
	rr := doReq(t, muxAdmin, http.MethodPost, "/v1/admin/cloud-accounts", map[string]any{
		"provider": "outscale", "name": "vm-skip-acct", "region": "eu-west-2",
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

	post := func(label string, body map[string]any) *AuditBagTestView {
		t.Helper()
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("%s: marshal: %v", label, err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/virtual-machines", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		ctx, bag := WithAuditBagForTest(req.Context())
		w := httptest.NewRecorder()
		muxCol.ServeHTTP(w, req.WithContext(ctx))
		if w.Code != http.StatusOK {
			t.Fatalf("%s POST /v1/virtual-machines: %d %q", label, w.Code, w.Body.String())
		}
		return bag
	}

	freshBody := map[string]any{
		"cloud_account_id": acct.ID,
		"provider_vm_id":   "i-aud-skip-1",
		"name":             "vm-aud-1",
		"power_state":      "running",
		"ready":            true,
	}
	mutatedBody := map[string]any{
		"cloud_account_id": acct.ID,
		"provider_vm_id":   "i-aud-skip-1",
		"name":             "vm-aud-1",
		"power_state":      "stopped",
		"ready":            false,
	}

	if bag := post("first", freshBody); bag.SkipForTest() {
		t.Fatalf("first POST should NOT skip (Inserted)")
	}
	if bag := post("identical", freshBody); !bag.SkipForTest() {
		t.Fatalf("identical POST SHOULD skip (NoChange), reason=%q", bag.SkipReasonForTest())
	}
	if bag := post("mutated", mutatedBody); bag.SkipForTest() {
		t.Fatalf("mutated POST should NOT skip (BusinessChanged)")
	}
}
