package store

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// TestPGDICTCoverageCounts seeds three workloads exercising each
// effective-DICT bucket (ADR-0029 §6) and asserts DICTCoverageCounts
// returns the expected (application, workload, none) tuple.
//
// Workloads carry no DICT columns of their own, so the `workload` bucket is
// always 0 today; the unlinked workload and the workload linked to a
// DICT-less application both fall into `none`.
func TestPGDICTCoverageCounts(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "dictcov"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	d := 3
	secureApp, err := pg.CreateApplication(ctx, api.ApplicationCreate{
		Name:             "secure-app",
		SecDisponibilite: &d,
	})
	if err != nil {
		t.Fatalf("create secure-app: %v", err)
	}
	plainApp, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: "plain-app"})
	if err != nil {
		t.Fatalf("create plain-app: %v", err)
	}

	// 1) linked to a DICT-bearing app → bucket "application".
	seedLinkedWorkload(t, pg, *ns.Id, "web", &secureApp.ID)
	// 2) linked to a DICT-less app → bucket "none".
	seedLinkedWorkload(t, pg, *ns.Id, "api", &plainApp.ID)
	// 3) unlinked → bucket "none".
	seedLinkedWorkload(t, pg, *ns.Id, "worker", nil)

	application, workload, none, err := pg.DICTCoverageCounts(ctx)
	if err != nil {
		t.Fatalf("DICTCoverageCounts: %v", err)
	}
	if application != 1 {
		t.Errorf("application bucket = %d, want 1", application)
	}
	if workload != 0 {
		t.Errorf("workload bucket = %d, want 0 (workloads have no DICT columns)", workload)
	}
	if none != 2 {
		t.Errorf("none bucket = %d, want 2", none)
	}
}

// seedLinkedWorkload creates a workload and, when appID is non-nil, links it
// to that application. Extracted to keep the test body under the
// cyclomatic-complexity threshold.
func seedLinkedWorkload(t *testing.T, pg *PG, nsID uuid.UUID, name string, appID *uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	wl, err := pg.CreateWorkload(ctx, api.WorkloadCreate{NamespaceId: nsID, Kind: api.Deployment, Name: name})
	if err != nil {
		t.Fatalf("create workload %q: %v", name, err)
	}
	if appID == nil {
		return
	}
	if _, err := pg.UpdateWorkload(ctx, *wl.Id, api.WorkloadUpdate{ApplicationId: appID}); err != nil {
		t.Fatalf("link workload %q: %v", name, err)
	}
}
