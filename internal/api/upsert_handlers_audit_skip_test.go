package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// runUpsertSkipScenario sends three POSTs against the same path:
//  1. fresh body  → outcome=Inserted   → bag MUST NOT skip
//  2. identical   → outcome=NoChange   → bag MUST skip
//  3. mutated     → outcome=Changed    → bag MUST NOT skip
//
// Used by D2-D8 wiring tests. The mutated body is supplied per entity to
// touch a single business field that the memStore tracks. All upserts in
// this suite return 201 on success.
func runUpsertSkipScenario(t *testing.T, h http.Handler, path, freshBody, mutatedBody string) {
	t.Helper()

	post := func(label, body string) *AuditBagTestView {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		ctx, bag := WithAuditBagForTest(r.Context())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r.WithContext(ctx))
		if w.Code != http.StatusCreated {
			t.Fatalf("%s POST %s: got %d, want %d, body=%q", label, path, w.Code, http.StatusCreated, w.Body.String())
		}
		return bag
	}

	if bag := post("first", freshBody); bag.SkipForTest() {
		t.Fatalf("%s: first POST should NOT skip (Inserted)", path)
	}
	if bag := post("identical", freshBody); !bag.SkipForTest() {
		t.Fatalf("%s: identical POST SHOULD skip (NoChange)", path)
	}
	if bag := post("mutated", mutatedBody); bag.SkipForTest() {
		t.Fatalf("%s: mutated POST should NOT skip (BusinessChanged)", path)
	}
}

func newSkipTestEnv(t *testing.T) (http.Handler, Cluster, Namespace) {
	t.Helper()
	store := newMemStore()
	h := newTestHandler(t, store)

	clusterResp := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-skip"}`)
	if clusterResp.Code != http.StatusCreated {
		t.Fatalf("create cluster: %d %q", clusterResp.Code, clusterResp.Body.String())
	}
	var cluster Cluster
	if err := json.Unmarshal(clusterResp.Body.Bytes(), &cluster); err != nil {
		t.Fatalf("decode cluster: %v", err)
	}

	nsResp := do(h, http.MethodPost, "/v1/namespaces", fmt.Sprintf(`{"cluster_id":%q,"name":"apps"}`, cluster.Id.String()))
	if nsResp.Code != http.StatusCreated {
		t.Fatalf("create namespace: %d %q", nsResp.Code, nsResp.Body.String())
	}
	var ns Namespace
	if err := json.Unmarshal(nsResp.Body.Bytes(), &ns); err != nil {
		t.Fatalf("decode namespace: %v", err)
	}
	return h, cluster, ns
}

func TestCreateNode_NoOpCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, cluster, _ := newSkipTestEnv(t)
	clusterID := cluster.Id.String()
	fresh := fmt.Sprintf(`{"cluster_id":%q,"name":"node-a","kubelet_version":"v1.30.0"}`, clusterID)
	mutated := fmt.Sprintf(`{"cluster_id":%q,"name":"node-a","kubelet_version":"v1.30.1"}`, clusterID)
	runUpsertSkipScenario(t, h, "/v1/nodes", fresh, mutated)
}

func TestCreateNamespace_NoOpCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, cluster, _ := newSkipTestEnv(t)
	clusterID := cluster.Id.String()
	fresh := fmt.Sprintf(`{"cluster_id":%q,"name":"ns-a","phase":"Active"}`, clusterID)
	mutated := fmt.Sprintf(`{"cluster_id":%q,"name":"ns-a","phase":"Terminating"}`, clusterID)
	runUpsertSkipScenario(t, h, "/v1/namespaces", fresh, mutated)
}

func TestCreateWorkload_NoOpCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, _, ns := newSkipTestEnv(t)
	nsID := ns.Id.String()
	fresh := fmt.Sprintf(`{"namespace_id":%q,"kind":"Deployment","name":"web","replicas":3}`, nsID)
	mutated := fmt.Sprintf(`{"namespace_id":%q,"kind":"Deployment","name":"web","replicas":5}`, nsID)
	runUpsertSkipScenario(t, h, "/v1/workloads", fresh, mutated)
}

func TestCreateService_NoOpCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, _, ns := newSkipTestEnv(t)
	nsID := ns.Id.String()
	fresh := fmt.Sprintf(`{"namespace_id":%q,"name":"svc-a","type":"ClusterIP","cluster_ip":"10.0.0.1"}`, nsID)
	mutated := fmt.Sprintf(`{"namespace_id":%q,"name":"svc-a","type":"LoadBalancer","cluster_ip":"10.0.0.1"}`, nsID)
	runUpsertSkipScenario(t, h, "/v1/services", fresh, mutated)
}

func TestCreateIngress_NoOpCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, _, ns := newSkipTestEnv(t)
	nsID := ns.Id.String()
	fresh := fmt.Sprintf(`{"namespace_id":%q,"name":"ing-a","ingress_class_name":"nginx"}`, nsID)
	mutated := fmt.Sprintf(`{"namespace_id":%q,"name":"ing-a","ingress_class_name":"traefik"}`, nsID)
	runUpsertSkipScenario(t, h, "/v1/ingresses", fresh, mutated)
}

func TestCreatePersistentVolume_NoOpCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, cluster, _ := newSkipTestEnv(t)
	clusterID := cluster.Id.String()
	fresh := fmt.Sprintf(`{"cluster_id":%q,"name":"pv-a","capacity":"10Gi","phase":"Available"}`, clusterID)
	mutated := fmt.Sprintf(`{"cluster_id":%q,"name":"pv-a","capacity":"20Gi","phase":"Available"}`, clusterID)
	runUpsertSkipScenario(t, h, "/v1/persistentvolumes", fresh, mutated)
}

func TestCreatePersistentVolumeClaim_NoOpCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, _, ns := newSkipTestEnv(t)
	nsID := ns.Id.String()
	fresh := fmt.Sprintf(`{"namespace_id":%q,"name":"pvc-a","phase":"Bound","requested_storage":"10Gi"}`, nsID)
	mutated := fmt.Sprintf(`{"namespace_id":%q,"name":"pvc-a","phase":"Bound","requested_storage":"20Gi"}`, nsID)
	runUpsertSkipScenario(t, h, "/v1/persistentvolumeclaims", fresh, mutated)
}
