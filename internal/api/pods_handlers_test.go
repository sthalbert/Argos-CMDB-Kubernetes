package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCreatePod_NoOpCallsSetAuditSkip exercises the audit no-op filter
// wiring: a brand-new POST must NOT mark the audit bag skip, while an
// identical replay (same business fields, store classifies NoChange)
// MUST flip skip=true so AuditMiddleware drops the row.
func TestCreatePod_NoOpCallsSetAuditSkip(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	h := newTestHandler(t, store)

	clusterResp := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-pod-skip"}`)
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

	podBody := fmt.Sprintf(`{"namespace_id":%q,"name":"pod-a","phase":"Running"}`, ns.Id.String())

	// First POST: new pod, must NOT skip.
	r1 := httptest.NewRequest(http.MethodPost, "/v1/pods", strings.NewReader(podBody))
	r1.Header.Set("Content-Type", "application/json")
	ctx1, bag1 := WithAuditBagForTest(r1.Context())
	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, r1.WithContext(ctx1))
	if w1.Code != http.StatusCreated {
		t.Fatalf("first POST: %d %q", w1.Code, w1.Body.String())
	}
	if bag1.SkipForTest() {
		t.Fatalf("first POST should NOT skip audit (outcome=Inserted)")
	}

	// Second POST: identical body, store classifies NoChange, handler
	// MUST mark skip.
	r2 := httptest.NewRequest(http.MethodPost, "/v1/pods", strings.NewReader(podBody))
	r2.Header.Set("Content-Type", "application/json")
	ctx2, bag2 := WithAuditBagForTest(r2.Context())
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2.WithContext(ctx2))
	if w2.Code != http.StatusCreated {
		t.Fatalf("second POST: %d %q", w2.Code, w2.Body.String())
	}
	if !bag2.SkipForTest() {
		t.Fatalf("identical second POST SHOULD skip audit (outcome=NoChange)")
	}

	// Third POST: change phase → BusinessChanged, must NOT skip.
	changedBody := fmt.Sprintf(`{"namespace_id":%q,"name":"pod-a","phase":"Succeeded"}`, ns.Id.String())
	r3 := httptest.NewRequest(http.MethodPost, "/v1/pods", strings.NewReader(changedBody))
	r3.Header.Set("Content-Type", "application/json")
	ctx3, bag3 := WithAuditBagForTest(r3.Context())
	w3 := httptest.NewRecorder()
	h.ServeHTTP(w3, r3.WithContext(ctx3))
	if w3.Code != http.StatusCreated {
		t.Fatalf("third POST: %d %q", w3.Code, w3.Body.String())
	}
	if bag3.SkipForTest() {
		t.Fatalf("changed phase should NOT skip audit (outcome=BusinessChanged)")
	}
}
