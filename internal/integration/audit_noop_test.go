package integration

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

// TestAuditNoOp_IdempotentUpsertsProduceOneAuditRow proves the end-to-end
// SecNumCloud-aligned no-op filter from ADR-0024: three identical POST
// /v1/pods calls collapse into exactly one audit row plus two skipped-
// counter increments. Validates the full chain — handler → store
// outcome detection → audit middleware bypass → metrics.
func TestAuditNoOp_IdempotentUpsertsProduceOneAuditRow(t *testing.T) {
	env := newTestEnv(t)

	// Seed: cluster + namespace (POSTs to /v1/clusters and /v1/namespaces
	// each fire one audit row of their own — irrelevant; we filter on
	// pod-only events below).
	var cluster api.Cluster
	if s := env.doJSON(t, http.MethodPost, "/v1/clusters", `{"name":"audit-noop-cluster"}`, &cluster); s != http.StatusCreated {
		t.Fatalf("seed cluster: %d", s)
	}
	clusterID := cluster.Id.String()

	var ns api.Namespace
	nsBody := fmt.Sprintf(`{"cluster_id":%q,"name":"apps"}`, clusterID)
	if s := env.doJSON(t, http.MethodPost, "/v1/namespaces", nsBody, &ns); s != http.StatusCreated {
		t.Fatalf("seed namespace: %d", s)
	}
	nsID := ns.Id.String()

	// Snapshot the prom counter — testutil.ToFloat64 reads the live value
	// of the labelset. PAT auth gives actor_kind=token (per audit.go).
	before := testutil.ToFloat64(metrics.AuditEventsSkippedFor("token", "pod", "no_change"))

	podBody := fmt.Sprintf(`{"namespace_id":%q,"name":"pod-noop","phase":"Running"}`, nsID)
	for i := 0; i < 3; i++ {
		resp := env.doReq(t, http.MethodPost, "/v1/pods", podBody)
		if resp.StatusCode != http.StatusCreated {
			_ = resp.Body.Close()
			t.Fatalf("post %d: status=%d", i, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	// Counter must have grown by exactly 2 (calls #2 and #3 skipped).
	after := testutil.ToFloat64(metrics.AuditEventsSkippedFor("token", "pod", "no_change"))
	if delta := after - before; delta != 2 {
		t.Errorf("audit_events_skipped_total{...,pod,no_change} delta = %v; want 2", delta)
	}

	// And the table must have grown by exactly 1 POST /v1/pods row.
	var auditList api.AuditEventList
	if s := env.doJSON(t, http.MethodGet, "/v1/admin/audit", "", &auditList); s != http.StatusOK {
		t.Fatalf("list audit: %d", s)
	}
	podPosts := 0
	for _, evt := range auditList.Items {
		if evt.HttpMethod == http.MethodPost && evt.HttpPath == "/v1/pods" {
			podPosts++
		}
	}
	if podPosts != 1 {
		t.Errorf("audit rows for POST /v1/pods = %d; want 1 (first insert only)", podPosts)
	}
}
