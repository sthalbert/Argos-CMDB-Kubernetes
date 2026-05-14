package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// runReconcileEmptyAuditSkip POSTs an empty-store reconcile (n == 0 path)
// and asserts that the audit bag was flipped to skip with reason
// "reconcile_empty". Then POSTs a reconcile that DOES delete (n > 0) and
// asserts the bag is NOT flipped.
//
// path is the reconcile endpoint, emptyBody is a JSON body that hits the
// handler with no rows to delete, and deleteBody is a JSON body that
// triggers a delete (so n > 0).
func runReconcileEmptyAuditSkip(t *testing.T, h http.Handler, path, emptyBody, deleteBody string) {
	t.Helper()

	post := func(label, body string) *AuditBagTestView {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		ctx, bag := WithAuditBagForTest(r.Context())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r.WithContext(ctx))
		if w.Code != http.StatusOK {
			t.Fatalf("%s POST %s: %d %q", label, path, w.Code, w.Body.String())
		}
		return bag
	}

	bagEmpty := post("empty", emptyBody)
	if !bagEmpty.SkipForTest() {
		t.Fatalf("%s: empty reconcile (n==0) SHOULD skip", path)
	}
	if got := bagEmpty.SkipReasonForTest(); got != "reconcile_empty" {
		t.Fatalf("%s: empty reconcile reason = %q, want %q", path, got, "reconcile_empty")
	}
	if deleteBody == "" {
		return
	}
	bagDel := post("delete", deleteBody)
	if bagDel.SkipForTest() {
		t.Fatalf("%s: delete reconcile (n>0) should NOT skip", path)
	}
}

func seedPod(t *testing.T, h http.Handler, namespaceID, name string) {
	t.Helper()
	body := fmt.Sprintf(`{"namespace_id":%q,"name":%q,"phase":"Running"}`, namespaceID, name)
	rr := do(h, http.MethodPost, "/v1/pods", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed pod: %d %q", rr.Code, rr.Body.String())
	}
}

func seedWorkload(t *testing.T, h http.Handler, namespaceID, kind, name string) {
	t.Helper()
	body := fmt.Sprintf(`{"namespace_id":%q,"kind":%q,"name":%q,"replicas":1}`, namespaceID, kind, name)
	rr := do(h, http.MethodPost, "/v1/workloads", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed workload: %d %q", rr.Code, rr.Body.String())
	}
}

func seedService(t *testing.T, h http.Handler, namespaceID, name string) {
	t.Helper()
	body := fmt.Sprintf(`{"namespace_id":%q,"name":%q,"type":"ClusterIP","cluster_ip":"10.0.0.5"}`, namespaceID, name)
	rr := do(h, http.MethodPost, "/v1/services", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed service: %d %q", rr.Code, rr.Body.String())
	}
}

func seedIngress(t *testing.T, h http.Handler, namespaceID, name string) {
	t.Helper()
	body := fmt.Sprintf(`{"namespace_id":%q,"name":%q,"ingress_class_name":"nginx"}`, namespaceID, name)
	rr := do(h, http.MethodPost, "/v1/ingresses", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed ingress: %d %q", rr.Code, rr.Body.String())
	}
}

func seedPV(t *testing.T, h http.Handler, clusterID, name string) {
	t.Helper()
	body := fmt.Sprintf(`{"cluster_id":%q,"name":%q,"capacity":"10Gi","phase":"Available"}`, clusterID, name)
	rr := do(h, http.MethodPost, "/v1/persistentvolumes", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed pv: %d %q", rr.Code, rr.Body.String())
	}
}

func seedPVC(t *testing.T, h http.Handler, namespaceID, name string) {
	t.Helper()
	body := fmt.Sprintf(`{"namespace_id":%q,"name":%q,"phase":"Pending","requested_storage":"5Gi"}`, namespaceID, name)
	rr := do(h, http.MethodPost, "/v1/persistentvolumeclaims", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed pvc: %d %q", rr.Code, rr.Body.String())
	}
}

func TestReconcileNodes_EmptyCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, cluster, _ := newSkipTestEnv(t)
	clusterID := cluster.Id.String()
	_ = seedNode(t, h, clusterID, "node-keep")
	emptyBody := fmt.Sprintf(`{"cluster_id":%q,"keep_names":["node-keep"]}`, clusterID)
	deleteBody := fmt.Sprintf(`{"cluster_id":%q,"keep_names":[]}`, clusterID)
	runReconcileEmptyAuditSkip(t, h, "/v1/nodes/reconcile", emptyBody, deleteBody)
}

func TestReconcileNamespaces_EmptyCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, cluster, _ := newSkipTestEnv(t)
	clusterID := cluster.Id.String()
	_ = seedNamespace(t, h, clusterID, "ns-keep")
	emptyBody := fmt.Sprintf(`{"cluster_id":%q,"keep_names":["apps","ns-keep"]}`, clusterID)
	deleteBody := fmt.Sprintf(`{"cluster_id":%q,"keep_names":["apps"]}`, clusterID)
	runReconcileEmptyAuditSkip(t, h, "/v1/namespaces/reconcile", emptyBody, deleteBody)
}

func TestReconcilePods_EmptyCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, _, ns := newSkipTestEnv(t)
	nsID := ns.Id.String()
	seedPod(t, h, nsID, "pod-keep")
	emptyBody := fmt.Sprintf(`{"namespace_id":%q,"keep_names":["pod-keep"]}`, nsID)
	deleteBody := fmt.Sprintf(`{"namespace_id":%q,"keep_names":[]}`, nsID)
	runReconcileEmptyAuditSkip(t, h, "/v1/pods/reconcile", emptyBody, deleteBody)
}

func TestReconcileWorkloads_EmptyCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, _, ns := newSkipTestEnv(t)
	nsID := ns.Id.String()
	seedWorkload(t, h, nsID, "Deployment", "wl-keep")
	emptyBody := fmt.Sprintf(`{"namespace_id":%q,"keep_kinds":["Deployment"],"keep_names":["wl-keep"]}`, nsID)
	deleteBody := fmt.Sprintf(`{"namespace_id":%q,"keep_kinds":[],"keep_names":[]}`, nsID)
	runReconcileEmptyAuditSkip(t, h, "/v1/workloads/reconcile", emptyBody, deleteBody)
}

func TestReconcileServices_EmptyCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, _, ns := newSkipTestEnv(t)
	nsID := ns.Id.String()
	seedService(t, h, nsID, "svc-keep")
	emptyBody := fmt.Sprintf(`{"namespace_id":%q,"keep_names":["svc-keep"]}`, nsID)
	deleteBody := fmt.Sprintf(`{"namespace_id":%q,"keep_names":[]}`, nsID)
	runReconcileEmptyAuditSkip(t, h, "/v1/services/reconcile", emptyBody, deleteBody)
}

func TestReconcileIngresses_EmptyCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, _, ns := newSkipTestEnv(t)
	nsID := ns.Id.String()
	seedIngress(t, h, nsID, "ing-keep")
	emptyBody := fmt.Sprintf(`{"namespace_id":%q,"keep_names":["ing-keep"]}`, nsID)
	deleteBody := fmt.Sprintf(`{"namespace_id":%q,"keep_names":[]}`, nsID)
	runReconcileEmptyAuditSkip(t, h, "/v1/ingresses/reconcile", emptyBody, deleteBody)
}

func TestReconcilePersistentVolumes_EmptyCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, cluster, _ := newSkipTestEnv(t)
	clusterID := cluster.Id.String()
	seedPV(t, h, clusterID, "pv-keep")
	emptyBody := fmt.Sprintf(`{"cluster_id":%q,"keep_names":["pv-keep"]}`, clusterID)
	deleteBody := fmt.Sprintf(`{"cluster_id":%q,"keep_names":[]}`, clusterID)
	runReconcileEmptyAuditSkip(t, h, "/v1/persistentvolumes/reconcile", emptyBody, deleteBody)
}

func TestReconcilePersistentVolumeClaims_EmptyCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	h, _, ns := newSkipTestEnv(t)
	nsID := ns.Id.String()
	seedPVC(t, h, nsID, "pvc-keep")
	emptyBody := fmt.Sprintf(`{"namespace_id":%q,"keep_names":["pvc-keep"]}`, nsID)
	deleteBody := fmt.Sprintf(`{"namespace_id":%q,"keep_names":[]}`, nsID)
	runReconcileEmptyAuditSkip(t, h, "/v1/persistentvolumeclaims/reconcile", emptyBody, deleteBody)
}

