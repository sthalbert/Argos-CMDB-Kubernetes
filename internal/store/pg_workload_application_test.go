package store

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// TestPGWorkloadApplicationLinkage exercises the ADR-0029 wiring on the
// workload side end-to-end at the store layer:
//
//   - PATCH workload with application_id → row carries the link.
//   - ListWorkloads filter by application_id, application_name, unlinked.
//   - Collector invariant: UpsertWorkload (re-run reconcile) must NOT
//     touch the curator's link.
//
// The handler-level "id wins on conflict" precedence is exercised by
// TestResolveApplicationID in internal/api; the store-side filter helper
// honours the same rule when both fields are non-nil and is verified
// here too.
//
//nolint:gocyclo,gocognit // integration test exercises several distinct paths
func TestPGWorkloadApplicationLinkage(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// --- seed cluster/namespace ---------------------------------------------
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "wlapp"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	// --- seed two applications so we can exercise both id and name paths ---
	billing, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: "billing"})
	if err != nil {
		t.Fatalf("create application billing: %v", err)
	}
	checkout, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: "checkout"})
	if err != nil {
		t.Fatalf("create application checkout: %v", err)
	}

	// --- seed two workloads (one to link, one to keep unlinked) ------------
	wlLinked, err := pg.CreateWorkload(ctx, api.WorkloadCreate{
		NamespaceId: *ns.Id, Kind: api.Deployment, Name: "web",
	})
	if err != nil {
		t.Fatalf("create wlLinked: %v", err)
	}
	wlUnlinked, err := pg.CreateWorkload(ctx, api.WorkloadCreate{
		NamespaceId: *ns.Id, Kind: api.Deployment, Name: "worker",
	})
	if err != nil {
		t.Fatalf("create wlUnlinked: %v", err)
	}

	// --- 1. PATCH with application_id → row picks up the link --------------
	updated, err := pg.UpdateWorkload(ctx, *wlLinked.Id, api.WorkloadUpdate{
		ApplicationId: &billing.ID,
	})
	if err != nil {
		t.Fatalf("update workload: %v", err)
	}
	if updated.ApplicationId == nil || *updated.ApplicationId != billing.ID {
		t.Fatalf("expected application_id=%v, got %v", billing.ID, updated.ApplicationId)
	}

	// Re-fetch to confirm scanWorkload exposes application_id from the
	// JOIN-decorated read path too (not just the PATCH return value).
	got, err := pg.GetWorkload(ctx, *wlLinked.Id)
	if err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if got.ApplicationId == nil || *got.ApplicationId != billing.ID {
		t.Fatalf("GET: expected application_id=%v, got %v", billing.ID, got.ApplicationId)
	}

	// --- 2. ListWorkloads filter by application_id → returns only linked ---
	items, _, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{
		NamespaceID:   ns.Id,
		ApplicationID: &billing.ID,
	}, 50, "")
	if err != nil {
		t.Fatalf("list by application_id: %v", err)
	}
	if len(items) != 1 || *items[0].Id != *wlLinked.Id {
		t.Fatalf("list by application_id: got %d items, want 1 (wlLinked); items=%+v", len(items), items)
	}

	// --- 3. ListWorkloads filter by application_name → same, resolved -----
	billingName := "billing"
	items, _, err = pg.ListWorkloads(ctx, api.WorkloadListFilter{
		NamespaceID:     ns.Id,
		ApplicationName: &billingName,
	}, 50, "")
	if err != nil {
		t.Fatalf("list by application_name: %v", err)
	}
	if len(items) != 1 || *items[0].Id != *wlLinked.Id {
		t.Fatalf("list by application_name: got %d items, want 1 (wlLinked)", len(items))
	}

	// --- 3a. application_name input is normalised (kebab-case) ------------
	mixedCase := "Billing"
	items, _, err = pg.ListWorkloads(ctx, api.WorkloadListFilter{
		NamespaceID:     ns.Id,
		ApplicationName: &mixedCase,
	}, 50, "")
	if err != nil {
		t.Fatalf("list by application_name (mixed case): %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("normalisation broke the lookup: got %d items, want 1", len(items))
	}

	// --- 3b. id wins on conflict (store-side precedence) ------------------
	otherName := "checkout"
	items, _, err = pg.ListWorkloads(ctx, api.WorkloadListFilter{
		NamespaceID:     ns.Id,
		ApplicationID:   &billing.ID,
		ApplicationName: &otherName,
	}, 50, "")
	if err != nil {
		t.Fatalf("list with id+name: %v", err)
	}
	if len(items) != 1 || *items[0].Id != *wlLinked.Id {
		t.Fatalf("id should win on conflict: got %+v", items)
	}

	// --- 4. unlinked=true → returns only the unlinked row ------------------
	yes := true
	items, _, err = pg.ListWorkloads(ctx, api.WorkloadListFilter{
		NamespaceID: ns.Id,
		Unlinked:    &yes,
	}, 50, "")
	if err != nil {
		t.Fatalf("list unlinked: %v", err)
	}
	if len(items) != 1 || *items[0].Id != *wlUnlinked.Id {
		t.Fatalf("list unlinked: got %d items, want 1 (wlUnlinked); items=%+v", len(items), items)
	}

	// --- 5. ListWorkloads with unknown application_name → 0 rows (sub-SELECT
	//        returns NULL, sql equality with NULL is unknown).
	bogus := "no-such-app"
	items, _, err = pg.ListWorkloads(ctx, api.WorkloadListFilter{
		NamespaceID:     ns.Id,
		ApplicationName: &bogus,
	}, 50, "")
	if err != nil {
		t.Fatalf("list bogus app name: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("unknown application_name should yield 0 rows, got %d", len(items))
	}

	// --- 6. Collector invariant: UpsertWorkload preserves application_id --
	// Simulate a collector reconcile tick by re-upserting the linked
	// workload with the same (namespace_id, kind, name). The application_id
	// MUST survive. This pins ADR-0029 §7.
	replicas := 5
	_, _, err = pg.UpsertWorkload(ctx, api.WorkloadCreate{
		NamespaceId: *ns.Id, Kind: api.Deployment, Name: "web",
		Replicas: &replicas,
	})
	if err != nil {
		t.Fatalf("upsert (collector tick): %v", err)
	}
	got, err = pg.GetWorkload(ctx, *wlLinked.Id)
	if err != nil {
		t.Fatalf("get after upsert: %v", err)
	}
	if got.ApplicationId == nil || *got.ApplicationId != billing.ID {
		t.Fatalf("collector invariant violated: application_id=%v after upsert, want %v",
			got.ApplicationId, billing.ID)
	}
	if got.Replicas == nil || *got.Replicas != replicas {
		t.Fatalf("upsert should still update collector fields: replicas=%v", got.Replicas)
	}

	// --- 7. Re-link the same workload to a different application --------
	updated, err = pg.UpdateWorkload(ctx, *wlLinked.Id, api.WorkloadUpdate{
		ApplicationId: &checkout.ID,
	})
	if err != nil {
		t.Fatalf("re-link: %v", err)
	}
	if updated.ApplicationId == nil || *updated.ApplicationId != checkout.ID {
		t.Fatalf("expected application_id=%v after re-link, got %v", checkout.ID, updated.ApplicationId)
	}

	// --- 8. Deleting the application sets the FK to NULL (ON DELETE SET NULL).
	if err := pg.DeleteApplication(ctx, checkout.ID); err != nil {
		t.Fatalf("delete application: %v", err)
	}
	got, err = pg.GetWorkload(ctx, *wlLinked.Id)
	if err != nil {
		t.Fatalf("get after delete app: %v", err)
	}
	if got.ApplicationId != nil {
		t.Fatalf("ON DELETE SET NULL violated: application_id=%v", *got.ApplicationId)
	}

	_ = uuid.UUID{} // keep the import even if Go decides one branch is enough.
}
