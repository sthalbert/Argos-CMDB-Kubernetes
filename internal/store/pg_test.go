package store

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/api"
)

// newTestPG returns a PG connected to PGX_TEST_DATABASE, or calls t.Skip
// when the env var is unset. Every test runs against a freshly migrated
// schema and is cleaned up with TRUNCATE on t.Cleanup.
func newTestPG(t *testing.T) *PG {
	t.Helper()
	dsn := os.Getenv("PGX_TEST_DATABASE")
	if dsn == "" {
		t.Skip("PGX_TEST_DATABASE not set; skipping integration test")
	}

	ctx := context.Background()
	pg, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := pg.Migrate(ctx); err != nil {
		pg.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		// CASCADE is required because nodes has a FK to clusters; without it
		// a plain TRUNCATE fails when the nodes table is non-empty and test
		// residue leaks across tests that share the same database.
		_, _ = pg.pool.Exec(context.Background(), "TRUNCATE clusters CASCADE")
		pg.Close()
	})
	return pg
}

func TestPGClusterCRUD(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	name := "test-" + strconv.FormatInt(int64(uuid.New().ID()), 16)
	env := "staging"
	created, err := pg.CreateCluster(ctx, api.ClusterCreate{
		Name:        name,
		Environment: &env,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Id == nil {
		t.Fatal("created.Id is nil")
	}

	got, err := pg.GetCluster(ctx, *created.Id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != name {
		t.Errorf("name = %q, want %q", got.Name, name)
	}
	if got.Environment == nil || *got.Environment != env {
		t.Errorf("environment = %v, want %q", got.Environment, env)
	}

	_, err = pg.CreateCluster(ctx, api.ClusterCreate{Name: name})
	if !errors.Is(err, api.ErrConflict) {
		t.Errorf("duplicate should be ErrConflict, got %v", err)
	}

	prov := "gke"
	updated, err := pg.UpdateCluster(ctx, *created.Id, api.ClusterUpdate{Provider: &prov})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Provider == nil || *updated.Provider != prov {
		t.Errorf("provider after update = %v, want %q", updated.Provider, prov)
	}

	if err := pg.DeleteCluster(ctx, *created.Id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := pg.DeleteCluster(ctx, *created.Id); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("second delete should be ErrNotFound, got %v", err)
	}
	if _, err := pg.GetCluster(ctx, *created.Id); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("get after delete should be ErrNotFound, got %v", err)
	}
}

func TestPGNodeCRUD(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "nodes-test"})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	kv := "v1.29.5"
	node, err := pg.CreateNode(ctx, api.NodeCreate{
		ClusterId:      *cluster.Id,
		Name:           "node-a",
		KubeletVersion: &kv,
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	if node.Id == nil {
		t.Fatal("node.Id nil")
	}

	if _, err := pg.CreateNode(ctx, api.NodeCreate{ClusterId: *cluster.Id, Name: "node-a"}); !errors.Is(err, api.ErrConflict) {
		t.Errorf("duplicate should be ErrConflict, got %v", err)
	}

	if _, err := pg.CreateNode(ctx, api.NodeCreate{ClusterId: uuid.New(), Name: "orphan"}); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("unknown cluster should be ErrNotFound, got %v", err)
	}

	got, err := pg.GetNode(ctx, *node.Id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "node-a" {
		t.Errorf("name=%q", got.Name)
	}
	if got.KubeletVersion == nil || *got.KubeletVersion != kv {
		t.Errorf("kubelet_version=%v", got.KubeletVersion)
	}

	arch := "arm64"
	updated, err := pg.UpdateNode(ctx, *node.Id, api.NodeUpdate{Architecture: &arch})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Architecture == nil || *updated.Architecture != arch {
		t.Errorf("architecture=%v", updated.Architecture)
	}

	items, _, err := pg.ListNodes(ctx, cluster.Id, 10, "")
	if err != nil {
		t.Fatalf("list filtered: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("filtered list len=%d", len(items))
	}

	other := uuid.New()
	items, _, err = pg.ListNodes(ctx, &other, 10, "")
	if err != nil {
		t.Fatalf("list foreign cluster: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("foreign-cluster list len=%d", len(items))
	}

	// FK cascade: deleting the cluster removes its nodes.
	if err := pg.DeleteCluster(ctx, *cluster.Id); err != nil {
		t.Fatalf("delete cluster: %v", err)
	}
	if _, err := pg.GetNode(ctx, *node.Id); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("node should have cascaded with cluster delete, got %v", err)
	}
}

func TestPGUpsertNode(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "upsert-test"})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	kv1 := "v1.29.5"
	first, err := pg.UpsertNode(ctx, api.NodeCreate{
		ClusterId:      *cluster.Id,
		Name:           "node-a",
		KubeletVersion: &kv1,
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if first.Id == nil {
		t.Fatal("first.Id nil")
	}

	// Second upsert with new kubelet version must mutate the SAME row.
	kv2 := "v1.29.6"
	second, err := pg.UpsertNode(ctx, api.NodeCreate{
		ClusterId:      *cluster.Id,
		Name:           "node-a",
		KubeletVersion: &kv2,
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if *second.Id != *first.Id {
		t.Errorf("id changed across upsert: first=%v second=%v", *first.Id, *second.Id)
	}
	if second.KubeletVersion == nil || *second.KubeletVersion != kv2 {
		t.Errorf("kubelet_version=%v, want %q", second.KubeletVersion, kv2)
	}
	if second.CreatedAt == nil || first.CreatedAt == nil || !second.CreatedAt.Equal(*first.CreatedAt) {
		t.Errorf("created_at should be preserved on conflict: first=%v second=%v", first.CreatedAt, second.CreatedAt)
	}
	if second.UpdatedAt == nil || first.UpdatedAt == nil || !second.UpdatedAt.After(*first.UpdatedAt) {
		t.Errorf("updated_at should advance on conflict: first=%v second=%v", first.UpdatedAt, second.UpdatedAt)
	}

	// Unknown cluster yields NotFound, not Conflict.
	if _, err := pg.UpsertNode(ctx, api.NodeCreate{ClusterId: uuid.New(), Name: "x"}); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("upsert with unknown cluster: want ErrNotFound, got %v", err)
	}
}

func TestPGNamespaceCRUD(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "ns-test"})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	phase := "Active"
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{
		ClusterId: *cluster.Id,
		Name:      "kube-system",
		Phase:     &phase,
	})
	if err != nil {
		t.Fatalf("create ns: %v", err)
	}

	if _, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "kube-system"}); !errors.Is(err, api.ErrConflict) {
		t.Errorf("duplicate should be ErrConflict, got %v", err)
	}
	if _, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: uuid.New(), Name: "x"}); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("unknown cluster should be ErrNotFound, got %v", err)
	}

	newPhase := "Terminating"
	updated, err := pg.UpdateNamespace(ctx, *ns.Id, api.NamespaceUpdate{Phase: &newPhase})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Phase == nil || *updated.Phase != newPhase {
		t.Errorf("phase=%v", updated.Phase)
	}

	items, _, err := pg.ListNamespaces(ctx, cluster.Id, 10, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("list len=%d", len(items))
	}

	// Cascade: deleting cluster removes namespaces.
	if err := pg.DeleteCluster(ctx, *cluster.Id); err != nil {
		t.Fatalf("delete cluster: %v", err)
	}
	if _, err := pg.GetNamespace(ctx, *ns.Id); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("namespace should have cascaded, got %v", err)
	}
}

func TestPGUpsertNamespace(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "ns-upsert"})
	if err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	phaseA := "Active"
	first, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{
		ClusterId: *cluster.Id,
		Name:      "default",
		Phase:     &phaseA,
	})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	phaseB := "Terminating"
	second, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{
		ClusterId: *cluster.Id,
		Name:      "default",
		Phase:     &phaseB,
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if *second.Id != *first.Id {
		t.Errorf("id changed across upsert: first=%v second=%v", *first.Id, *second.Id)
	}
	if second.Phase == nil || *second.Phase != phaseB {
		t.Errorf("phase=%v", second.Phase)
	}
	if !second.CreatedAt.Equal(*first.CreatedAt) {
		t.Errorf("created_at changed: first=%v second=%v", first.CreatedAt, second.CreatedAt)
	}
	if !second.UpdatedAt.After(*first.UpdatedAt) {
		t.Errorf("updated_at did not advance: first=%v second=%v", first.UpdatedAt, second.UpdatedAt)
	}
}

func TestPGDeleteNodesNotIn(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "reconcile-nodes"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, err := pg.CreateNode(ctx, api.NodeCreate{ClusterId: *cluster.Id, Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	// Keep only "b"; a and c should be deleted.
	deleted, err := pg.DeleteNodesNotIn(ctx, *cluster.Id, []string{"b"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted=%d, want 2", deleted)
	}

	items, _, err := pg.ListNodes(ctx, cluster.Id, 10, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Name != "b" {
		t.Errorf("survivors=%v, want [b]", items)
	}

	// Nil keepNames must delete everything remaining (pgx encodes nil as NULL;
	// the store's COALESCE handles that).
	deleted, err = pg.DeleteNodesNotIn(ctx, *cluster.Id, nil)
	if err != nil {
		t.Fatalf("reconcile nil keep: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted=%d (nil keep), want 1", deleted)
	}

	// Re-seed and verify the explicit empty-slice path matches the nil path.
	if _, err := pg.CreateNode(ctx, api.NodeCreate{ClusterId: *cluster.Id, Name: "z"}); err != nil {
		t.Fatalf("re-seed z: %v", err)
	}
	deleted, err = pg.DeleteNodesNotIn(ctx, *cluster.Id, []string{})
	if err != nil {
		t.Fatalf("reconcile empty keep: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted=%d (empty keep), want 1", deleted)
	}
}

func TestPGDeleteNamespacesNotIn(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "reconcile-ns"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	for _, name := range []string{"default", "kube-system", "extra"} {
		if _, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	deleted, err := pg.DeleteNamespacesNotIn(ctx, *cluster.Id, []string{"default", "kube-system"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted=%d, want 1", deleted)
	}

	items, _, err := pg.ListNamespaces(ctx, cluster.Id, 10, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("survivors=%d, want 2", len(items))
	}
}

func TestPGPodCRUD(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "pod-test"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "kube-system"})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	phase := "Running"
	pod, err := pg.CreatePod(ctx, api.PodCreate{
		NamespaceId: *ns.Id,
		Name:        "coredns-abc",
		Phase:       &phase,
	})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}

	if _, err := pg.CreatePod(ctx, api.PodCreate{NamespaceId: *ns.Id, Name: "coredns-abc"}); !errors.Is(err, api.ErrConflict) {
		t.Errorf("duplicate should be ErrConflict, got %v", err)
	}
	if _, err := pg.CreatePod(ctx, api.PodCreate{NamespaceId: uuid.New(), Name: "x"}); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("unknown namespace should be ErrNotFound, got %v", err)
	}

	newPhase := "Succeeded"
	updated, err := pg.UpdatePod(ctx, *pod.Id, api.PodUpdate{Phase: &newPhase})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Phase == nil || *updated.Phase != newPhase {
		t.Errorf("phase=%v", updated.Phase)
	}

	items, _, err := pg.ListPods(ctx, ns.Id, 10, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("list len=%d", len(items))
	}

	// Cascade: deleting the cluster removes namespace and pods.
	if err := pg.DeleteCluster(ctx, *cluster.Id); err != nil {
		t.Fatalf("cluster delete: %v", err)
	}
	if _, err := pg.GetPod(ctx, *pod.Id); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("pod should have cascaded via namespace->cluster, got %v", err)
	}
}

func TestPGUpsertPodAndDeleteNotIn(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "pod-upsert"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "default"})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	phaseA := "Pending"
	first, err := pg.UpsertPod(ctx, api.PodCreate{NamespaceId: *ns.Id, Name: "a", Phase: &phaseA})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	phaseB := "Running"
	second, err := pg.UpsertPod(ctx, api.PodCreate{NamespaceId: *ns.Id, Name: "a", Phase: &phaseB})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if *second.Id != *first.Id {
		t.Errorf("id changed across upsert")
	}
	if !second.UpdatedAt.After(*first.UpdatedAt) {
		t.Errorf("updated_at did not advance")
	}

	// Seed a second pod then reconcile to keep only one.
	if _, err := pg.UpsertPod(ctx, api.PodCreate{NamespaceId: *ns.Id, Name: "b"}); err != nil {
		t.Fatalf("create b: %v", err)
	}
	deleted, err := pg.DeletePodsNotIn(ctx, *ns.Id, []string{"a"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted=%d, want 1", deleted)
	}

	// Nil keep clears everything remaining.
	deleted, err = pg.DeletePodsNotIn(ctx, *ns.Id, nil)
	if err != nil {
		t.Fatalf("nil reconcile: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted=%d on nil keep, want 1", deleted)
	}
}

func TestPGWorkloadCRUD(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "workload-test"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	replicas := 3
	wl, err := pg.CreateWorkload(ctx, api.WorkloadCreate{
		NamespaceId: *ns.Id,
		Kind:        api.Deployment,
		Name:        "web",
		Replicas:    &replicas,
	})
	if err != nil {
		t.Fatalf("create workload: %v", err)
	}
	if wl.Id == nil || wl.Kind != api.Deployment {
		t.Fatalf("unexpected returned workload: %+v", wl)
	}

	// Same name + same namespace but different kind is allowed.
	if _, err := pg.CreateWorkload(ctx, api.WorkloadCreate{NamespaceId: *ns.Id, Kind: api.StatefulSet, Name: "web"}); err != nil {
		t.Errorf("(sts, web) should coexist with (deployment, web): %v", err)
	}
	// Same (namespace, kind, name) → ErrConflict.
	if _, err := pg.CreateWorkload(ctx, api.WorkloadCreate{NamespaceId: *ns.Id, Kind: api.Deployment, Name: "web"}); !errors.Is(err, api.ErrConflict) {
		t.Errorf("duplicate should be ErrConflict, got %v", err)
	}
	// Unknown namespace → ErrNotFound.
	if _, err := pg.CreateWorkload(ctx, api.WorkloadCreate{NamespaceId: uuid.New(), Kind: api.Deployment, Name: "x"}); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("unknown namespace should be ErrNotFound, got %v", err)
	}

	newReplicas := 5
	updated, err := pg.UpdateWorkload(ctx, *wl.Id, api.WorkloadUpdate{Replicas: &newReplicas})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Replicas == nil || *updated.Replicas != newReplicas {
		t.Errorf("replicas=%v, want %d", updated.Replicas, newReplicas)
	}

	// Filter by kind.
	depKind := api.Deployment
	items, _, err := pg.ListWorkloads(ctx, ns.Id, &depKind, 10, "")
	if err != nil {
		t.Fatalf("list by kind: %v", err)
	}
	if len(items) != 1 || items[0].Kind != api.Deployment {
		t.Errorf("filter-by-kind returned %+v", items)
	}

	// Cascade delete: drop cluster -> namespace cascade -> workloads cascade.
	if err := pg.DeleteCluster(ctx, *cluster.Id); err != nil {
		t.Fatalf("cluster delete: %v", err)
	}
	if _, err := pg.GetWorkload(ctx, *wl.Id); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("workload should have cascaded via namespace->cluster, got %v", err)
	}
}

func TestPGUpsertWorkloadAndReconcileByKindName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "workload-upsert"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	// Upsert (Deployment, web) twice — id preserved, updated_at advances.
	r1 := 2
	first, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{NamespaceId: *ns.Id, Kind: api.Deployment, Name: "web", Replicas: &r1})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	r2 := 4
	second, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{NamespaceId: *ns.Id, Kind: api.Deployment, Name: "web", Replicas: &r2})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if *second.Id != *first.Id {
		t.Errorf("id changed across upsert")
	}
	if !second.UpdatedAt.After(*first.UpdatedAt) {
		t.Errorf("updated_at did not advance")
	}

	// Seed a StatefulSet with the same name and a DaemonSet — reconcile keeping only Deployment/web.
	if _, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{NamespaceId: *ns.Id, Kind: api.StatefulSet, Name: "web"}); err != nil {
		t.Fatalf("sts: %v", err)
	}
	if _, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{NamespaceId: *ns.Id, Kind: api.DaemonSet, Name: "fluent-bit"}); err != nil {
		t.Fatalf("ds: %v", err)
	}

	deleted, err := pg.DeleteWorkloadsNotIn(ctx, *ns.Id, []string{"Deployment"}, []string{"web"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted=%d, want 2 (sts/web + ds/fluent-bit)", deleted)
	}

	// Sanity: only Deployment/web remains.
	items, _, err := pg.ListWorkloads(ctx, ns.Id, nil, 10, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Kind != api.Deployment || items[0].Name != "web" {
		t.Errorf("after reconcile got %+v", items)
	}

	// Nil keep clears everything remaining (pgx nil-array COALESCE guard).
	deleted, err = pg.DeleteWorkloadsNotIn(ctx, *ns.Id, nil, nil)
	if err != nil {
		t.Fatalf("nil reconcile: %v", err)
	}
	if deleted != 1 {
		t.Errorf("nil reconcile deleted=%d, want 1", deleted)
	}
}

func TestPGServiceCRUD(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "service-test"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	svcType := api.ClusterIP
	clusterIP := "10.0.0.1"
	svc, err := pg.CreateService(ctx, api.ServiceCreate{
		NamespaceId: *ns.Id,
		Name:        "web",
		Type:        &svcType,
		ClusterIp:   &clusterIP,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if svc.Id == nil || svc.Type == nil || *svc.Type != api.ClusterIP {
		t.Fatalf("unexpected: %+v", svc)
	}

	if _, err := pg.CreateService(ctx, api.ServiceCreate{NamespaceId: *ns.Id, Name: "web"}); !errors.Is(err, api.ErrConflict) {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := pg.CreateService(ctx, api.ServiceCreate{NamespaceId: uuid.New(), Name: "x"}); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("unknown namespace: %v", err)
	}

	newType := api.LoadBalancer
	updated, err := pg.UpdateService(ctx, *svc.Id, api.ServiceUpdate{Type: &newType})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Type == nil || *updated.Type != api.LoadBalancer {
		t.Errorf("type=%v", updated.Type)
	}

	// Cascade via namespace -> cluster.
	if err := pg.DeleteCluster(ctx, *cluster.Id); err != nil {
		t.Fatalf("cluster delete: %v", err)
	}
	if _, err := pg.GetService(ctx, *svc.Id); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("service should have cascaded: %v", err)
	}
}

func TestPGUpsertServiceAndDeleteNotIn(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "svc-upsert"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	first, err := pg.UpsertService(ctx, api.ServiceCreate{NamespaceId: *ns.Id, Name: "a"})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	newType := api.LoadBalancer
	second, err := pg.UpsertService(ctx, api.ServiceCreate{NamespaceId: *ns.Id, Name: "a", Type: &newType})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if *second.Id != *first.Id {
		t.Errorf("id changed across upsert")
	}
	if !second.UpdatedAt.After(*first.UpdatedAt) {
		t.Errorf("updated_at did not advance")
	}

	if _, err := pg.UpsertService(ctx, api.ServiceCreate{NamespaceId: *ns.Id, Name: "b"}); err != nil {
		t.Fatalf("b: %v", err)
	}
	deleted, err := pg.DeleteServicesNotIn(ctx, *ns.Id, []string{"a"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted=%d, want 1", deleted)
	}

	deleted, err = pg.DeleteServicesNotIn(ctx, *ns.Id, nil)
	if err != nil {
		t.Fatalf("nil reconcile: %v", err)
	}
	if deleted != 1 {
		t.Errorf("nil reconcile deleted=%d, want 1", deleted)
	}
}

func TestPGGetClusterByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	created, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "by-name-test"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := pg.GetClusterByName(ctx, "by-name-test")
	if err != nil {
		t.Fatalf("lookup by name: %v", err)
	}
	if got.Id == nil || *got.Id != *created.Id {
		t.Errorf("id mismatch: got=%v created=%v", got.Id, created.Id)
	}

	if _, err := pg.GetClusterByName(ctx, "does-not-exist"); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("missing cluster: want ErrNotFound, got %v", err)
	}
}

func TestPGListPagination(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		name := "page-" + strconv.Itoa(i)
		if _, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	page1, next, err := pg.ListClusters(ctx, 2, "")
	if err != nil {
		t.Fatalf("list page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len=%d, want 2", len(page1))
	}
	if next == "" {
		t.Fatal("next cursor empty after page1")
	}

	page2, next, err := pg.ListClusters(ctx, 2, next)
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2 len=%d, want 2", len(page2))
	}

	page3, next, err := pg.ListClusters(ctx, 2, next)
	if err != nil {
		t.Fatalf("list page3: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3 len=%d, want 1", len(page3))
	}
	if next != "" {
		t.Errorf("next should be empty on last page, got %q", next)
	}

	seen := make(map[uuid.UUID]bool)
	for _, c := range append(append(page1, page2...), page3...) {
		if c.Id == nil {
			t.Fatal("cluster id nil")
		}
		if seen[*c.Id] {
			t.Errorf("duplicate id %v across pages", *c.Id)
		}
		seen[*c.Id] = true
	}
}
