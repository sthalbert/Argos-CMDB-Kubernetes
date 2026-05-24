package api

// Handler-level tests for the workload → application linking surface
// (ADR-0029 §2.3). Exercises the strict-server PATCH path on top of the
// in-memory memStore so the test runs without a Postgres dependency.
//
// The contract under test, summarised:
//   - PATCH with application_id sets the link.
//   - PATCH with application_name resolves server-side.
//   - PATCH with both → id wins (mirrors ADR-0019 cloud_account precedence).
//   - PATCH with an unknown id or name → 400.
//   - PATCH with no link fields touches nothing (omitted fields = no change).

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

func newWorkloadLinkServer(t *testing.T) (*Server, *memStore) {
	t.Helper()
	resetApplicationFake()
	ms := newMemStore()
	return NewServer("test", ms, auth.SecureNever, nil, NewLoginRateLimiter(), NewVerifyRateLimiter()), ms
}

// seedWorkloadLinkFixtures creates a cluster/namespace/workload chain plus
// two seeded applications. Returns the seeded workload id and both
// application ids so individual cases can drive the precedence matrix.
func seedWorkloadLinkFixtures(t *testing.T, s *Server, ms *memStore) (wlID uuid.UUID, primary, secondary Application) {
	t.Helper()
	ctx := context.Background()

	cluster, _, err := ms.EnsureCluster(ctx, ClusterCreate{Name: "wlapp-handler"})
	if err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	ns, err := ms.CreateNamespace(ctx, NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
	wl, err := ms.CreateWorkload(ctx, WorkloadCreate{
		NamespaceId: *ns.Id, Kind: Deployment, Name: "web",
	})
	if err != nil {
		t.Fatalf("seed workload: %v", err)
	}

	primary, err = ms.CreateApplication(ctx, ApplicationCreate{Name: "billing"})
	if err != nil {
		t.Fatalf("seed application primary: %v", err)
	}
	secondary, err = ms.CreateApplication(ctx, ApplicationCreate{Name: "checkout"})
	if err != nil {
		t.Fatalf("seed application secondary: %v", err)
	}
	_ = s
	return *wl.Id, primary, secondary
}

func TestUpdateWorkload_LinkByID(t *testing.T) {
	s, ms := newWorkloadLinkServer(t)
	wlID, primary, _ := seedWorkloadLinkFixtures(t, s, ms)
	ctx := context.Background()

	body := UpdateWorkloadApplicationMergePatchPlusJSONRequestBody{
		ApplicationId: &primary.ID,
	}
	resp, err := s.UpdateWorkload(ctx, UpdateWorkloadRequestObject{Id: wlID, Body: &body})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	ok, isOK := resp.(UpdateWorkload200JSONResponse)
	if !isOK {
		t.Fatalf("expected 200, got %T", resp)
	}
	if ok.ApplicationId == nil || *ok.ApplicationId != primary.ID {
		t.Fatalf("expected application_id=%v, got %v", primary.ID, ok.ApplicationId)
	}
}

func TestUpdateWorkload_LinkByName(t *testing.T) {
	s, ms := newWorkloadLinkServer(t)
	wlID, primary, _ := seedWorkloadLinkFixtures(t, s, ms)
	ctx := context.Background()

	name := "billing"
	body := UpdateWorkloadApplicationMergePatchPlusJSONRequestBody{
		ApplicationName: &name,
	}
	resp, err := s.UpdateWorkload(ctx, UpdateWorkloadRequestObject{Id: wlID, Body: &body})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	ok, isOK := resp.(UpdateWorkload200JSONResponse)
	if !isOK {
		t.Fatalf("expected 200, got %T", resp)
	}
	if ok.ApplicationId == nil || *ok.ApplicationId != primary.ID {
		t.Fatalf("expected application_id=%v after name resolution, got %v", primary.ID, ok.ApplicationId)
	}
}

func TestUpdateWorkload_IDBeatsName(t *testing.T) {
	s, ms := newWorkloadLinkServer(t)
	wlID, primary, secondary := seedWorkloadLinkFixtures(t, s, ms)
	ctx := context.Background()

	// id points at primary, name points at secondary. id must win.
	secondaryName := secondary.Name
	body := UpdateWorkloadApplicationMergePatchPlusJSONRequestBody{
		ApplicationId:   &primary.ID,
		ApplicationName: &secondaryName,
	}
	resp, err := s.UpdateWorkload(ctx, UpdateWorkloadRequestObject{Id: wlID, Body: &body})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	ok, isOK := resp.(UpdateWorkload200JSONResponse)
	if !isOK {
		t.Fatalf("expected 200, got %T", resp)
	}
	if ok.ApplicationId == nil || *ok.ApplicationId != primary.ID {
		t.Fatalf("expected primary id %v to win, got %v", primary.ID, ok.ApplicationId)
	}
}

func TestUpdateWorkload_UnknownIDIs400(t *testing.T) {
	s, ms := newWorkloadLinkServer(t)
	wlID, _, _ := seedWorkloadLinkFixtures(t, s, ms)
	ctx := context.Background()

	bogus := uuid.New()
	body := UpdateWorkloadApplicationMergePatchPlusJSONRequestBody{
		ApplicationId: &bogus,
	}
	resp, err := s.UpdateWorkload(ctx, UpdateWorkloadRequestObject{Id: wlID, Body: &body})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if _, isBad := resp.(UpdateWorkload400ApplicationProblemPlusJSONResponse); !isBad {
		t.Fatalf("expected 400 for unknown application_id, got %T", resp)
	}
}

func TestUpdateWorkload_UnknownNameIs400(t *testing.T) {
	s, ms := newWorkloadLinkServer(t)
	wlID, _, _ := seedWorkloadLinkFixtures(t, s, ms)
	ctx := context.Background()

	missing := "no-such-application"
	body := UpdateWorkloadApplicationMergePatchPlusJSONRequestBody{
		ApplicationName: &missing,
	}
	resp, err := s.UpdateWorkload(ctx, UpdateWorkloadRequestObject{Id: wlID, Body: &body})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if _, isBad := resp.(UpdateWorkload400ApplicationProblemPlusJSONResponse); !isBad {
		t.Fatalf("expected 400 for unknown application_name, got %T", resp)
	}
}

// TestUpdateWorkload_NoLinkFields confirms that omitting both fields
// leaves the existing link untouched (merge-patch semantics).
func TestUpdateWorkload_NoLinkFields(t *testing.T) {
	s, ms := newWorkloadLinkServer(t)
	wlID, primary, _ := seedWorkloadLinkFixtures(t, s, ms)
	ctx := context.Background()

	// First, link it.
	link := UpdateWorkloadApplicationMergePatchPlusJSONRequestBody{ApplicationId: &primary.ID}
	if _, err := s.UpdateWorkload(ctx, UpdateWorkloadRequestObject{Id: wlID, Body: &link}); err != nil {
		t.Fatalf("seed link: %v", err)
	}

	// Then PATCH a non-link field — replicas — and assert the link survives.
	replicas := 7
	body := UpdateWorkloadApplicationMergePatchPlusJSONRequestBody{Replicas: &replicas}
	resp, err := s.UpdateWorkload(ctx, UpdateWorkloadRequestObject{Id: wlID, Body: &body})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	ok, isOK := resp.(UpdateWorkload200JSONResponse)
	if !isOK {
		t.Fatalf("expected 200, got %T", resp)
	}
	if ok.ApplicationId == nil || *ok.ApplicationId != primary.ID {
		t.Fatalf("link should survive a non-link PATCH: application_id=%v", ok.ApplicationId)
	}
	if ok.Replicas == nil || *ok.Replicas != replicas {
		t.Fatalf("replicas not updated: %v", ok.Replicas)
	}
}
