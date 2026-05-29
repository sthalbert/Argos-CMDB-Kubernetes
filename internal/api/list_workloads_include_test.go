package api

// Handler-level test for the opt-in ?include=containers_versions enrichment
// on GET /v1/workloads (ADR-0022/0032). Runs on the in-memory memStore so no
// Postgres is required.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sthalbert/longue-vue/internal/auth"
)

func TestListWorkloads_IncludeContainersVersions(t *testing.T) {
	ms := newMemStore()
	s := NewServer("test", ms, auth.SecureNever, nil, NewLoginRateLimiter(), NewVerifyRateLimiter())
	ctx := context.Background()

	cluster, _, err := ms.EnsureCluster(ctx, ClusterCreate{Name: "wl-include"})
	if err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	ns, err := ms.CreateNamespace(ctx, NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
	containers := ContainerList{{"name": "web", "image": "nginx:1.25.3"}}
	if _, err := ms.CreateWorkload(ctx, WorkloadCreate{
		NamespaceId: *ns.Id,
		Kind:        Deployment,
		Name:        "web",
		Containers:  &containers,
	}); err != nil {
		t.Fatalf("seed workload: %v", err)
	}

	latest := "1.27.4"
	if _, err := ms.UpsertImageVersion(ctx, ImageVersionUpsert{
		ImageRepo:     "docker.io/library/nginx",
		Variant:       "",
		Registry:      "docker.io",
		LatestTag:     &latest,
		Annotation:    json.RawMessage(`{}`),
		Source:        "registry",
		LastCheckedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert image version: %v", err)
	}

	// With include: containers_versions should be populated.
	inc := IncludeContainersVersions
	resp, err := s.ListWorkloads(ctx, ListWorkloadsRequestObject{Params: ListWorkloadsParams{Include: &inc}})
	if err != nil {
		t.Fatalf("ListWorkloads (include): %v", err)
	}
	list, ok := resp.(ListWorkloads200JSONResponse)
	if !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 workload, got %d", len(list.Items))
	}
	cv := list.Items[0].ContainersVersions
	if cv == nil {
		t.Fatal("containers_versions not populated with ?include=containers_versions")
	}
	web, ok := (*cv)["web"]
	if !ok {
		t.Fatalf("expected 'web' container enriched, got %v", *cv)
	}
	if web.EolStatus == nil || string(*web.EolStatus) != "eol" {
		t.Errorf("web.EolStatus: want eol (1.25 vs 1.27 = 2 minors), got %v", web.EolStatus)
	}

	// Without include: containers_versions stays nil (cheap list).
	resp2, err := s.ListWorkloads(ctx, ListWorkloadsRequestObject{})
	if err != nil {
		t.Fatalf("ListWorkloads (no include): %v", err)
	}
	list2, ok := resp2.(ListWorkloads200JSONResponse)
	if !ok {
		t.Fatalf("expected 200, got %T", resp2)
	}
	if len(list2.Items) != 1 {
		t.Fatalf("expected 1 workload, got %d", len(list2.Items))
	}
	if list2.Items[0].ContainersVersions != nil {
		t.Errorf("containers_versions should be nil without ?include, got %v", *list2.Items[0].ContainersVersions)
	}
}

// TestListWorkloads_IncludeNothingEnrichable verifies that requesting the
// enrichment for a workload whose containers cannot be enriched (no matching
// image_versions row) omits containers_versions entirely rather than
// serialising it as null — the schema is non-nullable.
func TestListWorkloads_IncludeNothingEnrichable(t *testing.T) {
	ms := newMemStore()
	s := NewServer("test", ms, auth.SecureNever, nil, NewLoginRateLimiter(), NewVerifyRateLimiter())
	ctx := context.Background()

	cluster, _, err := ms.EnsureCluster(ctx, ClusterCreate{Name: "wl-include-empty"})
	if err != nil {
		t.Fatalf("seed cluster: %v", err)
	}
	ns, err := ms.CreateNamespace(ctx, NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
	// Container present, but no image_versions row exists for it → unenrichable.
	containers := ContainerList{{"name": "web", "image": "nginx:1.25.3"}}
	if _, err := ms.CreateWorkload(ctx, WorkloadCreate{
		NamespaceId: *ns.Id,
		Kind:        Deployment,
		Name:        "web",
		Containers:  &containers,
	}); err != nil {
		t.Fatalf("seed workload: %v", err)
	}

	inc := IncludeContainersVersions
	resp, err := s.ListWorkloads(ctx, ListWorkloadsRequestObject{Params: ListWorkloadsParams{Include: &inc}})
	if err != nil {
		t.Fatalf("ListWorkloads (include, nothing enrichable): %v", err)
	}
	list, ok := resp.(ListWorkloads200JSONResponse)
	if !ok {
		t.Fatalf("expected 200, got %T", resp)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 workload, got %d", len(list.Items))
	}
	if list.Items[0].ContainersVersions != nil {
		t.Errorf("containers_versions should be omitted when nothing is enrichable, got %v", *list.Items[0].ContainersVersions)
	}
}
