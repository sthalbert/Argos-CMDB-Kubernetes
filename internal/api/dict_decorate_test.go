package api

// Handler-level tests for the read-only effective_dict projection on
// Workload + VM responses (ADR-0029 §6). Runs on the in-memory memStore so
// no Postgres is required.

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// seedDICTApp creates an application carrying the given DICT axes.
func seedDICTApp(t *testing.T, ms *memStore, name string, d *int) Application {
	t.Helper()
	app, err := ms.CreateApplication(context.Background(), ApplicationCreate{
		Name:             name,
		SecDisponibilite: d,
	})
	if err != nil {
		t.Fatalf("seed application %q: %v", name, err)
	}
	return app
}

func TestGetWorkload_EffectiveDICT_FromApplication(t *testing.T) {
	s, ms := newWorkloadLinkServer(t)
	wlID, _, _ := seedWorkloadLinkFixtures(t, s, ms)
	ctx := context.Background()

	app := seedDICTApp(t, ms, "secure-app", intp(3))
	// Link the workload to the DICT-bearing application.
	link := UpdateWorkloadApplicationMergePatchPlusJSONRequestBody{ApplicationId: &app.ID}
	if _, err := s.UpdateWorkload(ctx, UpdateWorkloadRequestObject{Id: wlID, Body: &link}); err != nil {
		t.Fatalf("link workload: %v", err)
	}

	resp, err := s.GetWorkload(ctx, GetWorkloadRequestObject{Id: wlID})
	if err != nil {
		t.Fatalf("GetWorkload: %v", err)
	}
	got, ok := resp.(GetWorkloadEnrichedResponse)
	if !ok {
		t.Fatalf("expected enriched response, got %T", resp)
	}
	if got.Workload.EffectiveDict == nil {
		t.Fatal("effective_dict not set")
	}
	if got.Workload.EffectiveDict.Source != DICTSourceApplication {
		t.Errorf("source = %q, want application", got.Workload.EffectiveDict.Source)
	}
	if got.Workload.EffectiveDict.Disponibilite == nil || *got.Workload.EffectiveDict.Disponibilite != 3 {
		t.Errorf("disponibilite = %v, want 3", got.Workload.EffectiveDict.Disponibilite)
	}
}

func TestGetWorkload_EffectiveDICT_Unlinked_None(t *testing.T) {
	// Workloads carry no DICT columns of their own, so an unlinked workload
	// resolves to source="none" (there is no workload-level fallback today).
	s, ms := newWorkloadLinkServer(t)
	wlID, _, _ := seedWorkloadLinkFixtures(t, s, ms)
	ctx := context.Background()

	resp, err := s.GetWorkload(ctx, GetWorkloadRequestObject{Id: wlID})
	if err != nil {
		t.Fatalf("GetWorkload: %v", err)
	}
	got, ok := resp.(GetWorkloadEnrichedResponse)
	if !ok {
		t.Fatalf("expected enriched response, got %T", resp)
	}
	if got.Workload.EffectiveDict == nil {
		t.Fatal("effective_dict not set")
	}
	if got.Workload.EffectiveDict.Source != DICTSourceNone {
		t.Errorf("source = %q, want none", got.Workload.EffectiveDict.Source)
	}
}

// listN1Store wraps memStore to count GetApplicationsByIDs calls, proving the
// list path bulk-fetches instead of N+1.
type listN1Store struct {
	*memStore
	batchCalls int
}

func (l *listN1Store) GetApplicationsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Application, error) {
	l.batchCalls++
	return l.memStore.GetApplicationsByIDs(ctx, ids)
}

func TestListWorkloads_EffectiveDICT_NoN1(t *testing.T) {
	resetApplicationFake()
	ms := newMemStore()
	counting := &listN1Store{memStore: ms}
	s := NewServer("test", counting, auth.SecureNever, nil, NewLoginRateLimiter(), NewVerifyRateLimiter())
	ctx := context.Background()

	seedListWorkloadsWithDICTApp(t, s, ms)

	counting.batchCalls = 0 // reset after setup
	resp, err := s.ListWorkloads(ctx, ListWorkloadsRequestObject{})
	if err != nil {
		t.Fatalf("ListWorkloads: %v", err)
	}
	list, ok := resp.(ListWorkloads200JSONResponse)
	if !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if got := len(list.Items); got != 3 {
		t.Fatalf("expected 3 workloads, got %d", got)
	}
	if counting.batchCalls != 1 {
		t.Errorf("expected exactly 1 bulk GetApplicationsByIDs call, got %d (N+1 regression)", counting.batchCalls)
	}

	linked, none := countDICTBuckets(t, list.Items)
	if linked != 2 || none != 1 {
		t.Errorf("buckets: linked=%d none=%d, want 2/1", linked, none)
	}
}

// seedListWorkloadsWithDICTApp creates a cluster/namespace and three
// workloads — two linked to a DICT-bearing application, one unlinked.
// Extracted to keep the test body under the cyclomatic-complexity threshold.
func seedListWorkloadsWithDICTApp(t *testing.T, s *Server, ms *memStore) {
	t.Helper()
	ctx := context.Background()

	cluster, _, err := ms.EnsureCluster(ctx, ClusterCreate{Name: "wlapp-list"})
	if err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	ns, err := ms.CreateNamespace(ctx, NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
	app := seedDICTApp(t, ms, "secure-app", intp(2))

	linkedIDs := make([]uuid.UUID, 0, 2)
	for _, name := range []string{"web", "api"} {
		wl, err := ms.CreateWorkload(ctx, WorkloadCreate{NamespaceId: *ns.Id, Kind: Deployment, Name: name})
		if err != nil {
			t.Fatalf("seed workload %q: %v", name, err)
		}
		linkedIDs = append(linkedIDs, *wl.Id)
	}
	if _, err := ms.CreateWorkload(ctx, WorkloadCreate{NamespaceId: *ns.Id, Kind: Deployment, Name: "worker"}); err != nil {
		t.Fatalf("seed unlinked workload: %v", err)
	}
	for _, id := range linkedIDs {
		link := UpdateWorkloadApplicationMergePatchPlusJSONRequestBody{ApplicationId: &app.ID}
		if _, err := s.UpdateWorkload(ctx, UpdateWorkloadRequestObject{Id: id, Body: &link}); err != nil {
			t.Fatalf("link workload %v: %v", id, err)
		}
	}
}

// countDICTBuckets tallies workloads by effective-DICT source and verifies
// linked rows carry the expected disponibilite (2). Extracted to keep the
// test body under the cyclomatic-complexity threshold.
func countDICTBuckets(t *testing.T, items []Workload) (linked, none int) {
	t.Helper()
	for i := range items {
		wl := items[i]
		if wl.EffectiveDict == nil {
			t.Fatalf("workload %q missing effective_dict", wl.Name)
		}
		switch wl.EffectiveDict.Source {
		case DICTSourceApplication:
			linked++
			if wl.EffectiveDict.Disponibilite == nil || *wl.EffectiveDict.Disponibilite != 2 {
				t.Errorf("linked workload %q disponibilite = %v, want 2", wl.Name, wl.EffectiveDict.Disponibilite)
			}
		case DICTSourceNone:
			none++
		default:
			t.Errorf("workload %q unexpected source %q", wl.Name, wl.EffectiveDict.Source)
		}
	}
	return linked, none
}

func TestGetVirtualMachine_EffectiveDICT_FromApplication(t *testing.T) {
	resetCloudFake()
	resetApplicationFake()
	ms := newMemStore()
	ctx := context.Background()

	app := seedDICTApp(t, ms, "vm-secure-app", intp(4))

	acct, err := ms.UpsertCloudAccount(ctx, CloudAccountUpsert{
		Provider: "outscale", Name: "vm-dict", Region: "eu-west-2",
	})
	if err != nil {
		t.Fatalf("seed cloud account: %v", err)
	}
	vm, _, err := ms.UpsertVirtualMachine(ctx, VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-123",
		Name:           "vm-1",
		PowerState:     "running",
		Ready:          true,
	})
	if err != nil {
		t.Fatalf("seed vm: %v", err)
	}
	if _, err := ms.UpdateVirtualMachine(ctx, vm.ID, VirtualMachinePatch{ApplicationID: &app.ID}); err != nil {
		t.Fatalf("link vm: %v", err)
	}

	got, err := ms.GetVirtualMachine(ctx, vm.ID)
	if err != nil {
		t.Fatalf("GetVirtualMachine: %v", err)
	}
	decorateVMDICT(ctx, ms, &got)
	if got.EffectiveDict == nil {
		t.Fatal("effective_dict not set on vm")
	}
	if got.EffectiveDict.Source != DICTSourceApplication {
		t.Errorf("source = %q, want application", got.EffectiveDict.Source)
	}
	if got.EffectiveDict.Disponibilite == nil || *got.EffectiveDict.Disponibilite != 4 {
		t.Errorf("disponibilite = %v, want 4", got.EffectiveDict.Disponibilite)
	}
}
