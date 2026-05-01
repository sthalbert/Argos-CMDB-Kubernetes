package api

// Integration tests for the cloud-account hand-written handlers
// (ADR-0015). Builds a dedicated mux mirroring how main.go wires the
// routes, then issues HTTPS-equivalent calls against it.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/secrets"
)

// buildCloudMux constructs a router carrying the ADR-0015 hand-written
// routes plus an injection middleware that attaches a synthetic Caller
// to the request context. This avoids needing a real session cookie or
// a fully-hashed token in tests; the tests focus on handler logic,
// scope enforcement and binding.
func buildCloudMux(t *testing.T, store Store, enc *secrets.Encrypter, caller *auth.Caller) http.Handler {
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

	mux.Handle("GET /v1/admin/cloud-accounts", wrap(HandleListCloudAccounts(store)))
	mux.Handle("POST /v1/admin/cloud-accounts", wrap(HandleCreateCloudAccount(store, enc)))
	mux.Handle("GET /v1/admin/cloud-accounts/{id}", wrap(HandleGetCloudAccount(store)))
	mux.Handle("PATCH /v1/admin/cloud-accounts/{id}", wrap(HandlePatchCloudAccount(store)))
	mux.Handle("PATCH /v1/admin/cloud-accounts/{id}/credentials", wrap(HandlePatchCloudAccountCredentials(store, enc)))
	mux.Handle("POST /v1/admin/cloud-accounts/{id}/disable", wrap(HandleDisableCloudAccount(store)))
	mux.Handle("POST /v1/admin/cloud-accounts/{id}/enable", wrap(HandleEnableCloudAccount(store)))
	mux.Handle("DELETE /v1/admin/cloud-accounts/{id}", wrap(HandleDeleteCloudAccount(store)))

	mux.Handle("POST /v1/cloud-accounts", wrap(HandleCollectorRegisterCloudAccount(store)))
	mux.Handle("PATCH /v1/cloud-accounts/{id}/status", wrap(HandleCollectorPatchCloudAccountStatus(store)))
	mux.Handle("GET /v1/cloud-accounts/by-name/{name}/credentials", wrap(HandleCollectorGetCredentialsByName(store, enc)))
	mux.Handle("GET /v1/cloud-accounts/{id}/credentials", wrap(HandleCollectorGetCredentialsByID(store, enc)))

	mux.Handle("POST /v1/virtual-machines", wrap(HandleUpsertVirtualMachine(store)))
	mux.Handle("POST /v1/virtual-machines/reconcile", wrap(HandleReconcileVirtualMachines(store)))
	mux.Handle("GET /v1/virtual-machines", wrap(HandleListVirtualMachines(store)))
	mux.Handle("GET /v1/virtual-machines/{id}", wrap(HandleGetVirtualMachine(store)))
	mux.Handle("PATCH /v1/virtual-machines/{id}", wrap(HandlePatchVirtualMachine(store)))
	mux.Handle("DELETE /v1/virtual-machines/{id}", wrap(HandleDeleteVirtualMachine(store)))

	return mux
}

func newTestEncrypter(t *testing.T) *secrets.Encrypter {
	t.Helper()
	key := bytes.Repeat([]byte{0xAB}, secrets.MasterKeySize)
	enc, err := secrets.NewEncrypter(key)
	if err != nil {
		t.Fatalf("new encrypter: %v", err)
	}
	return enc
}

func adminCaller() *auth.Caller {
	return &auth.Caller{
		Kind:     auth.CallerKindUser,
		UserID:   uuid.New(),
		Username: "admin",
		Role:     auth.RoleAdmin,
		Scopes:   auth.ScopesForRole(auth.RoleAdmin),
	}
}

func collectorCaller(boundID *uuid.UUID) *auth.Caller {
	return &auth.Caller{
		Kind:                auth.CallerKindToken,
		TokenID:             uuid.New(),
		Scopes:              []string{auth.ScopeVMCollector},
		BoundCloudAccountID: boundID,
	}
}

func doReq(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr) //nolint:noctx // test helper; context is not needed for in-process handler tests
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestCloudAccountHybridOnboarding exercises the full onboarding flow
// from ADR-0015 §6: collector POSTs to register, admin sets credentials,
// collector fetches and gets the plaintext SK back.
//
//nolint:gocyclo // integration test — many sequential assertions; splitting would obscure the flow
func TestCloudAccountHybridOnboarding(t *testing.T) {
	resetCloudFake()
	store := newMemStore()
	enc := newTestEncrypter(t)

	// Step 1: admin creates a placeholder via POST /v1/admin/cloud-accounts
	// (no credentials yet). This is the row the vm-collector PAT will be
	// bound to before the collector ever boots.
	muxAdmin := buildCloudMux(t, store, enc, adminCaller())
	rr := doReq(t, muxAdmin, http.MethodPost, "/v1/admin/cloud-accounts", map[string]any{
		"provider": "outscale",
		"name":     "acme-prod",
		"region":   "eu-west-2",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("admin pre-register: status=%d body=%q", rr.Code, rr.Body.String())
	}
	var registered CloudAccount
	if err := json.Unmarshal(rr.Body.Bytes(), &registered); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if registered.Status != CloudAccountStatusPendingCredentials {
		t.Errorf("status = %q, want pending_credentials", registered.Status)
	}

	// Step 2: collector boots (PAT bound to the row admin just created)
	// and POSTs to /v1/cloud-accounts; idempotent — returns the row.
	col := collectorCaller(&registered.ID)
	mux := buildCloudMux(t, store, enc, col)
	rr = doReq(t, mux, http.MethodPost, "/v1/cloud-accounts", map[string]any{
		"provider": "outscale",
		"name":     "acme-prod",
		"region":   "eu-west-2",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("collector register: status=%d body=%q", rr.Code, rr.Body.String())
	}

	// Step 2b: collector tries to fetch credentials BEFORE admin sets them
	// → expect 404.
	rr = doReq(t, mux, http.MethodGet,
		"/v1/cloud-accounts/by-name/acme-prod/credentials", nil)
	if rr.Code != http.StatusNotFound {
		t.Errorf("fetch creds before admin: status=%d body=%q", rr.Code, rr.Body.String())
	}

	// Step 3: admin sets credentials.
	rr = doReq(t, muxAdmin, http.MethodPatch,
		fmt.Sprintf("/v1/admin/cloud-accounts/%s/credentials", registered.ID),
		map[string]any{
			"access_key": "AKIA-FAKE-AK",
			"secret_key": "very-secret-sk",
		})
	if rr.Code != http.StatusOK {
		t.Fatalf("admin set creds: status=%d body=%q", rr.Code, rr.Body.String())
	}
	var afterCreds CloudAccount
	if err := json.Unmarshal(rr.Body.Bytes(), &afterCreds); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if afterCreds.Status != CloudAccountStatusActive {
		t.Errorf("status after admin: %q want active", afterCreds.Status)
	}

	// Step 4: collector fetches credentials → 200, plaintext SK.
	rr = doReq(t, mux, http.MethodGet,
		"/v1/cloud-accounts/by-name/acme-prod/credentials", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("collector fetch creds: status=%d body=%q", rr.Code, rr.Body.String())
	}
	var creds credentialsResp
	if err := json.Unmarshal(rr.Body.Bytes(), &creds); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if creds.AccessKey != "AKIA-FAKE-AK" || creds.SecretKey != "very-secret-sk" {
		t.Errorf("creds = %+v", creds)
	}
	if creds.Region != "eu-west-2" || creds.Provider != "outscale" {
		t.Errorf("creds region/provider = %+v", creds)
	}
}

// TestCloudAccountBindingEnforcement asserts that a vm-collector PAT
// bound to account A cannot fetch credentials for account B.
func TestCloudAccountBindingEnforcement(t *testing.T) {
	resetCloudFake()
	store := newMemStore()
	enc := newTestEncrypter(t)

	// Admin pre-creates two accounts with credentials.
	muxAdmin := buildCloudMux(t, store, enc, adminCaller())
	createA := doReq(t, muxAdmin, http.MethodPost, "/v1/admin/cloud-accounts", map[string]any{
		"provider": "outscale", "name": "acct-a", "region": "eu-west-2",
		"access_key": "ak-a", "secret_key": "sk-a",
	})
	if createA.Code != http.StatusCreated {
		t.Fatalf("create A: status=%d body=%q", createA.Code, createA.Body.String())
	}
	var acctA CloudAccount
	_ = json.Unmarshal(createA.Body.Bytes(), &acctA)

	createB := doReq(t, muxAdmin, http.MethodPost, "/v1/admin/cloud-accounts", map[string]any{
		"provider": "outscale", "name": "acct-b", "region": "eu-west-2",
		"access_key": "ak-b", "secret_key": "sk-b",
	})
	if createB.Code != http.StatusCreated {
		t.Fatalf("create B: status=%d body=%q", createB.Code, createB.Body.String())
	}
	var acctB CloudAccount
	_ = json.Unmarshal(createB.Body.Bytes(), &acctB)

	// Collector PAT bound to A.
	muxCollector := buildCloudMux(t, store, enc, collectorCaller(&acctA.ID))

	// Fetch A → 200.
	rr := doReq(t, muxCollector, http.MethodGet,
		fmt.Sprintf("/v1/cloud-accounts/%s/credentials", acctA.ID), nil)
	if rr.Code != http.StatusOK {
		t.Errorf("fetch A creds: status=%d body=%q", rr.Code, rr.Body.String())
	}

	// Fetch B → 403.
	rr = doReq(t, muxCollector, http.MethodGet,
		fmt.Sprintf("/v1/cloud-accounts/%s/credentials", acctB.ID), nil)
	if rr.Code != http.StatusForbidden {
		t.Errorf("fetch B creds: status=%d body=%q", rr.Code, rr.Body.String())
	}

	// PATCH status on B → 403.
	rr = doReq(t, muxCollector, http.MethodPatch,
		fmt.Sprintf("/v1/cloud-accounts/%s/status", acctB.ID),
		map[string]any{"status": "active"})
	if rr.Code != http.StatusForbidden {
		t.Errorf("status B: status=%d body=%q", rr.Code, rr.Body.String())
	}

	// Upsert VM under B → 403.
	rr = doReq(t, muxCollector, http.MethodPost,
		"/v1/virtual-machines",
		map[string]any{
			"cloud_account_id": acctB.ID,
			"provider_vm_id":   "i-aabbcc",
			"name":             "vm-b",
			"power_state":      "running",
		})
	if rr.Code != http.StatusForbidden {
		t.Errorf("upsert VM B: status=%d body=%q", rr.Code, rr.Body.String())
	}
}

// TestCloudAccountDisableBlocksCredentials verifies disabled accounts
// return 403 on the credentials fetch endpoint.
func TestCloudAccountDisableBlocksCredentials(t *testing.T) {
	resetCloudFake()
	store := newMemStore()
	enc := newTestEncrypter(t)

	// Admin creates and disables an account.
	muxAdmin := buildCloudMux(t, store, enc, adminCaller())
	rr := doReq(t, muxAdmin, http.MethodPost, "/v1/admin/cloud-accounts", map[string]any{
		"provider": "outscale", "name": "x", "region": "eu-west-2",
		"access_key": "ak", "secret_key": "sk",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var acct CloudAccount
	_ = json.Unmarshal(rr.Body.Bytes(), &acct)

	rr = doReq(t, muxAdmin, http.MethodPost,
		fmt.Sprintf("/v1/admin/cloud-accounts/%s/disable", acct.ID), nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("disable: status=%d body=%q", rr.Code, rr.Body.String())
	}

	// Collector tries to fetch — gets 403.
	muxCol := buildCloudMux(t, store, enc, collectorCaller(&acct.ID))
	rr = doReq(t, muxCol, http.MethodGet,
		fmt.Sprintf("/v1/cloud-accounts/%s/credentials", acct.ID), nil)
	if rr.Code != http.StatusForbidden {
		t.Errorf("disabled fetch: status=%d body=%q", rr.Code, rr.Body.String())
	}
}

// TestVMUpsertConflictAlreadyKubeNode ensures the dedup-vs-nodes path
// returns the expected 409 error body.
func TestVMUpsertConflictAlreadyKubeNode(t *testing.T) {
	resetCloudFake()
	store := newMemStore()
	enc := newTestEncrypter(t)

	muxAdmin := buildCloudMux(t, store, enc, adminCaller())
	rr := doReq(t, muxAdmin, http.MethodPost, "/v1/admin/cloud-accounts", map[string]any{
		"provider": "outscale", "name": "x", "region": "eu-west-2",
		"access_key": "ak", "secret_key": "sk",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var acct CloudAccount
	_ = json.Unmarshal(rr.Body.Bytes(), &acct)

	col := collectorCaller(&acct.ID)
	muxCol := buildCloudMux(t, store, enc, col)
	rr = doReq(t, muxCol, http.MethodPost, "/v1/virtual-machines", map[string]any{
		"cloud_account_id": acct.ID,
		"provider_vm_id":   "i-deadbeef",
		"name":             "vm-1",
		"power_state":      "running",
		// Trigger the fake's dedup conflict path.
		"tags": map[string]string{"argos.test.is_kube": "true"},
	})
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%q", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] != "already_inventoried_as_kubernetes_node" {
		t.Errorf("error = %v", body["error"])
	}
}

// TestCloudAccountAuditScrub ensures access_key + secret_key are redacted
// in audit-captured request bodies.
func TestCloudAccountAuditScrub(t *testing.T) {
	body := []byte(`{"access_key":"ak","secret_key":"sk","other":"x"}`)
	got := scrubSecrets(body)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("scrubSecrets returned %T", got)
	}
	if m["access_key"] != redactedValue {
		t.Errorf("access_key = %v", m["access_key"])
	}
	if m["secret_key"] != redactedValue {
		t.Errorf("secret_key = %v", m["secret_key"])
	}
	if m["other"] != "x" {
		t.Errorf("other = %v", m["other"])
	}
}

// helper to silence unused warnings during incremental tests.
var (
	_ = strings.HasPrefix
	_ = context.Background
	_ = time.Now
)
