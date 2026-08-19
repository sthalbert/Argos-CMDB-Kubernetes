package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

func buildKyvernoPostMux(t *testing.T, store Store, caller *auth.Caller) http.Handler {
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
	mux.Handle("POST /v1/cluster-policies", wrap(HandleCreateClusterPolicy(store)))
	mux.Handle("POST /v1/policy-reports", wrap(HandleCreatePolicyReport(store)))
	mux.Handle("DELETE /v1/cluster-policies/{id}", wrap(HandleDeleteClusterPolicy(store)))
	mux.Handle("DELETE /v1/policy-reports/{id}", wrap(HandleDeletePolicyReport(store)))
	return mux
}

func enablePolicies(t *testing.T, store Store) {
	t.Helper()
	on := true
	if _, err := store.UpdateSettings(t.Context(), SettingsPatch{PoliciesEnabled: &on}); err != nil {
		t.Fatalf("enable policies: %v", err)
	}
}

func TestCreateClusterPolicy_201(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "cluster",
		"spec_raw":      map[string]any{"rules": []any{}},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got["id"]; !ok {
		t.Error("response missing 'id'")
	}
}

func TestCreateClusterPolicy_400MissingName(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400MissingClusterID(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400MissingResourceType(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id": uuid.New(),
		"name":       "require-labels",
		"spec_raw":   map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400MissingSpecRaw(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400InvalidResourceType(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "InvalidType",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400InvalidScope(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "global",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_403WithoutWriteScope(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, viewerCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_409PoliciesDisabled(t *testing.T) {
	store := newMemStore()
	// PoliciesEnabled defaults to false — no enablePolicies call
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400PolicyWithoutNamespace(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "Policy",
		"scope":         "namespace",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (Policy requires namespace_id); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400ClusterPolicyWithNamespace(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"namespace_id":  uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "cluster",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (ClusterPolicy forbids namespace_id); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_DefaultScopeCluster(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_Normalisation(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":     uuid.New(),
		"name":           "require-labels",
		"resource_type":  "ClusterPolicy",
		"severity":       "HIGH",
		"action":         "ENFORCE",
		"failure_policy": "fail",
		"spec_raw":       map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["severity"] != "high" {
		t.Errorf("severity: got %v, want high", got["severity"])
	}
	if got["action"] != "enforce" {
		t.Errorf("action: got %v, want enforce", got["action"])
	}
	if got["failure_policy"] != "Fail" {
		t.Errorf("failure_policy: got %v, want Fail", got["failure_policy"])
	}
}

func TestCreatePolicyReport_201(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":   uuid.New(),
		"name":         "pr-ns-default",
		"scope_kind":   "Namespace",
		"scope_name":   "default",
		"summary_pass": 5,
		"summary_fail": 1,
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got["id"]; !ok {
		t.Error("response missing 'id'")
	}
}

func TestCreatePolicyReport_400MissingName(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id": uuid.New(),
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_400MissingClusterID(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"name": "pr-ns-default",
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_403WithoutWriteScope(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, viewerCaller())

	body := map[string]any{
		"cluster_id": uuid.New(),
		"name":       "pr-ns-default",
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_409PoliciesDisabled(t *testing.T) {
	store := newMemStore()
	// PoliciesEnabled defaults to false
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id": uuid.New(),
		"name":       "pr-ns-default",
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_ScopeKindStoredVerbatim(t *testing.T) {
	// scope_kind keeps the client's casing: the collector stores K8s kinds
	// verbatim, and title-casing here mangled CamelCase kinds
	// (ReplicaSet -> Replicaset), splitting the same kind across two
	// spellings. Filtering is case-insensitive instead (store layer).
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	for _, kind := range []string{"ReplicaSet", "DaemonSet", "deployment"} {
		body := map[string]any{
			"cluster_id":   uuid.New(),
			"name":         "pr-" + strings.ToLower(kind),
			"scope_kind":   kind,
			"scope_name":   "web",
			"summary_pass": 3,
		}
		rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
		if rr.Code != http.StatusCreated {
			t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got["scope_kind"] != kind {
			t.Errorf("scope_kind: got %v, want %s (verbatim)", got["scope_kind"], kind)
		}
	}
}

func TestCreateClusterPolicy_InvalidJSON(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	req := httptest.NewRequest(http.MethodPost, "/v1/cluster-policies", strings.NewReader("not-json")) //nolint:noctx // in-process handler test
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_InvalidJSON(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	req := httptest.NewRequest(http.MethodPost, "/v1/policy-reports", strings.NewReader("not-json")) //nolint:noctx // in-process handler test
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeleteClusterPolicy_204(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "api-policy",
		"resource_type": "ClusterPolicy",
		"scope":         "cluster",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	id, ok := created["id"].(string)
	if !ok {
		t.Fatalf("created id missing or not a string: %v", created["id"])
	}

	rr = doReq(t, h, http.MethodDelete, "/v1/cluster-policies/"+id, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeleteClusterPolicy_404CollectorManaged(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	collectorID := uuid.New()
	store.mu.Lock()
	store.clusterPolicies[collectorID] = ClusterPolicyRow{
		ID:           collectorID,
		ClusterID:    uuid.New(),
		Name:         "collector-policy",
		ResourceType: "ClusterPolicy",
		Scope:        "cluster",
		Source:       SourceCollector,
		SpecRaw:      json.RawMessage(`{}`),
	}
	store.mu.Unlock()

	rr := doReq(t, h, http.MethodDelete, "/v1/cluster-policies/"+collectorID.String(), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("delete collector-managed: got %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeleteClusterPolicy_404UnknownID(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodDelete, "/v1/cluster-policies/"+uuid.New().String(), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("delete unknown: got %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeleteClusterPolicy_403WithoutWriteScope(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, viewerCaller())

	rr := doReq(t, h, http.MethodDelete, "/v1/cluster-policies/"+uuid.New().String(), nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("delete without scope: got %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeletePolicyReport_204(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":   uuid.New(),
		"name":         "api-report",
		"scope_kind":   "Namespace",
		"scope_name":   "default",
		"summary_pass": 1,
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	id, ok := created["id"].(string)
	if !ok {
		t.Fatalf("created id missing or not a string: %v", created["id"])
	}

	rr = doReq(t, h, http.MethodDelete, "/v1/policy-reports/"+id, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
}

func TestDeletePolicyReport_404CollectorManaged(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	collectorID := uuid.New()
	store.mu.Lock()
	store.policyReports[collectorID] = PolicyReportRow{
		ID:        collectorID,
		ClusterID: uuid.New(),
		Name:      "collector-report",
		Source:    SourceCollector,
	}
	store.mu.Unlock()

	rr := doReq(t, h, http.MethodDelete, "/v1/policy-reports/"+collectorID.String(), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("delete collector-managed: got %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_409CollectorCollision(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	clusterID := uuid.New()
	store.mu.Lock()
	existingID := uuid.New()
	store.clusterPolicies[existingID] = ClusterPolicyRow{
		ID:           existingID,
		ClusterID:    clusterID,
		Name:         "require-labels",
		ResourceType: "ClusterPolicy",
		Scope:        "cluster",
		Source:       SourceCollector,
		SpecRaw:      json.RawMessage(`{}`),
	}
	store.mu.Unlock()

	body := map[string]any{
		"cluster_id":    clusterID,
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "cluster",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409 (API overwriting collector); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_409CollectorCollision(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	clusterID := uuid.New()
	store.mu.Lock()
	existingID := uuid.New()
	store.policyReports[existingID] = PolicyReportRow{
		ID:        existingID,
		ClusterID: clusterID,
		Name:      "pr-default",
		Source:    SourceCollector,
	}
	store.mu.Unlock()

	body := map[string]any{
		"cluster_id":   clusterID,
		"name":         "pr-default",
		"scope_kind":   "Namespace",
		"scope_name":   "default",
		"summary_pass": 1,
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409 (API overwriting collector); body=%s", rr.Code, rr.Body.String())
	}
}

func seedClusterWithNamespace(t *testing.T, store *memStore) (clusterID, nsID uuid.UUID) {
	t.Helper()
	clusterID = uuid.New()
	store.mu.Lock()
	store.byID[clusterID] = Cluster{Id: &clusterID, Name: "test-cluster"}
	store.mu.Unlock()
	nsID = uuid.New()
	store.mu.Lock()
	store.nsByID[nsID] = Namespace{
		Id:        &nsID,
		ClusterId: clusterID,
		Name:      "default",
	}
	store.mu.Unlock()
	return
}

func TestCreateClusterPolicy_422NamespaceNotExist(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"namespace_id":  uuid.New(),
		"name":          "require-labels",
		"resource_type": "Policy",
		"scope":         "namespace",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422 (namespace does not exist); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_422NamespaceWrongCluster(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	_, nsID := seedClusterWithNamespace(t, store)
	otherClusterID := uuid.New()
	store.mu.Lock()
	store.byID[otherClusterID] = Cluster{Id: &otherClusterID, Name: "other-cluster"}
	store.mu.Unlock()

	body := map[string]any{
		"cluster_id":    otherClusterID,
		"namespace_id":  nsID,
		"name":          "require-labels",
		"resource_type": "Policy",
		"scope":         "namespace",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422 (namespace in wrong cluster); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_201WithValidNamespace(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	clusterID, nsID := seedClusterWithNamespace(t, store)

	body := map[string]any{
		"cluster_id":    clusterID,
		"namespace_id":  nsID,
		"name":          "require-labels",
		"resource_type": "Policy",
		"scope":         "namespace",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_422NamespaceNotExist(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":   uuid.New(),
		"namespace_id": uuid.New(),
		"name":         "pr-ns-default",
		"summary_pass": 1,
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422 (namespace does not exist); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_422NamespaceWrongCluster(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	_, nsID := seedClusterWithNamespace(t, store)
	otherClusterID := uuid.New()
	store.mu.Lock()
	store.byID[otherClusterID] = Cluster{Id: &otherClusterID, Name: "other-cluster"}
	store.mu.Unlock()

	body := map[string]any{
		"cluster_id":   otherClusterID,
		"namespace_id": nsID,
		"name":         "pr-ns-default",
		"summary_pass": 1,
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status: got %d, want 422 (namespace in wrong cluster); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_ScopeNormalisedToLowercase(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "CLUSTER",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["scope"] != "cluster" {
		t.Errorf("scope: got %v, want cluster", got["scope"])
	}
}

func TestCreatePolicyReport_ScopeKindAnyKindAccepted(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":   uuid.New(),
		"name":         "pr-ns-default",
		"scope_kind":   "custom-resource",
		"scope_name":   "my-cr",
		"summary_pass": 1,
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201 (any scope_kind is accepted); body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["scope_kind"] != "custom-resource" {
		t.Errorf("scope_kind: got %v, want custom-resource (verbatim)", got["scope_kind"])
	}
}

func TestCreateClusterPolicy_400InvalidSeverity(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
		"severity":      "banana",
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400InvalidAction(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
		"action":        "block",
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400NegativeRulesCount(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
		"rules_count":   -5,
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400SpecRawNotObject(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      []any{1, 2},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (spec_raw must be an object); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_400ResultsRawNotArray(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":  uuid.New(),
		"name":        "pr-bad-results",
		"results_raw": map[string]any{"a": 1},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (results_raw must be an array); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_400SummaryTooLarge(t *testing.T) {
	// The columns are INTEGER; a count past MaxInt32 would fail the
	// INSERT with a generic 500 instead of a 400.
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":   uuid.New(),
		"name":         "pr-huge",
		"summary_pass": int64(3_000_000_000),
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400RulesCountTooLarge(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
		"rules_count":   int64(3_000_000_000),
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_ValidSeverityAndActionNormalised(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"spec_raw":      map[string]any{},
		"severity":      "HIGH",
		"action":        "Enforce",
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["severity"] != "high" {
		t.Errorf("severity: got %v, want high", got["severity"])
	}
	if got["action"] != "enforce" {
		t.Errorf("action: got %v, want enforce", got["action"])
	}
}

func TestCreateClusterPolicy_400ScopeMismatchPolicyRequiresNamespace(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "Policy",
		"scope":         "cluster",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (Policy must have namespace scope); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_400ScopeMismatchClusterPolicyRequiresCluster(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":    uuid.New(),
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "namespace",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (ClusterPolicy must have cluster scope); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_PolicyDefaultsToNamespaceScope(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	cID, nsID := seedClusterWithNamespace(t, store)
	body := map[string]any{
		"cluster_id":    cID,
		"namespace_id":  nsID,
		"name":          "require-labels",
		"resource_type": "Policy",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["scope"] != "namespace" {
		t.Errorf("scope: got %v, want namespace (default for Policy)", got["scope"])
	}
}

func TestCreateClusterPolicy_400SpecRawNull(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", strings.NewReader(
		`{"cluster_id":"`+uuid.New().String()+`","name":"p","resource_type":"ClusterPolicy","spec_raw":null}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (spec_raw null); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_400NegativeSummary(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	body := map[string]any{
		"cluster_id":   uuid.New(),
		"name":         "pr-negative",
		"summary_fail": -1,
	}
	rr := doReq(t, h, http.MethodPost, "/v1/policy-reports", body)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (negative summary); body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_ApiOverApiUpdate_201(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	clusterID := uuid.New()

	body1 := map[string]any{
		"cluster_id":    clusterID,
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "cluster",
		"spec_raw":      map[string]any{"rules": []any{}},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body1)
	if rr.Code != http.StatusCreated {
		t.Fatalf("first create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	body2 := map[string]any{
		"cluster_id":    clusterID,
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "cluster",
		"description":   "updated",
		"spec_raw":      map[string]any{"rules": []any{}},
	}
	rr = doReq(t, h, http.MethodPost, "/v1/cluster-policies", body2)
	if rr.Code != http.StatusCreated {
		t.Fatalf("api→api update: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var second map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if second["description"] != "updated" {
		t.Errorf("api→api update did not apply new description: got %v", second["description"])
	}
}

func TestCreateClusterPolicy_CollectorOverApiUpdate_409(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	clusterID := uuid.New()

	body1 := map[string]any{
		"cluster_id":    clusterID,
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "cluster",
		"spec_raw":      map[string]any{},
	}
	rr := doReq(t, h, http.MethodPost, "/v1/cluster-policies", body1)
	if rr.Code != http.StatusCreated {
		t.Fatalf("api create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	id, ok := created["id"].(string)
	if !ok {
		t.Fatalf("created id missing or not a string: %v", created["id"])
	}

	parsedID, _ := uuid.Parse(id)
	store.mu.Lock()
	cp := store.clusterPolicies[parsedID]
	cp.Source = SourceCollector
	store.clusterPolicies[parsedID] = cp
	store.mu.Unlock()

	body2 := map[string]any{
		"cluster_id":    clusterID,
		"name":          "require-labels",
		"resource_type": "ClusterPolicy",
		"scope":         "cluster",
		"description":   "try collector overwrite",
		"spec_raw":      map[string]any{},
	}
	rr = doReq(t, h, http.MethodPost, "/v1/cluster-policies", body2)
	if rr.Code != http.StatusConflict {
		t.Fatalf("collector→api flip: got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreateClusterPolicy_413BodyTooLarge(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	clusterID := uuid.New()
	big := `{"cluster_id":"` + clusterID.String() +
		`","name":"x","resource_type":"ClusterPolicy","spec_raw":{},"padding":"` +
		strings.Repeat("A", 1<<20) + `"}`

	req := httptest.NewRequest(http.MethodPost, "/v1/cluster-policies", strings.NewReader(big)) //nolint:noctx // in-process handler test
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithCaller(req.Context(), editorCaller()))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want 413; body=%s", rr.Code, rr.Body.String())
	}
}

func TestCreatePolicyReport_413BodyTooLarge(t *testing.T) {
	store := newMemStore()
	enablePolicies(t, store)
	h := buildKyvernoPostMux(t, store, editorCaller())

	clusterID := uuid.New()
	big := `{"cluster_id":"` + clusterID.String() +
		`","name":"x","padding":"` +
		strings.Repeat("A", 1<<20) + `"}`

	req := httptest.NewRequest(http.MethodPost, "/v1/policy-reports", strings.NewReader(big)) //nolint:noctx // in-process handler test
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithCaller(req.Context(), editorCaller()))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want 413; body=%s", rr.Code, rr.Body.String())
	}
}
