package store

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/api"
	"github.com/sthalbert/argos/internal/auth"
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
		// Wipe every data table between tests. CASCADE is required on
		// clusters because nodes (and namespaces, etc.) FK onto it; the
		// auth tables stand on their own so TRUNCATE them alongside.
		// api_tokens RESTRICTs on user deletion, so truncate them in
		// order (api_tokens → users gets CASCADEd via sessions / identities).
		_, _ = pg.pool.Exec(context.Background(),
			"TRUNCATE clusters, api_tokens, sessions, user_identities, oidc_auth_states, users CASCADE")
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

	items, _, err := pg.ListPods(ctx, api.PodListFilter{NamespaceID: ns.Id}, 10, "")
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
	items, _, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{NamespaceID: ns.Id, Kind: &depKind}, 10, "")
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
	items, _, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{NamespaceID: ns.Id}, 10, "")
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

// TestPGPodAndWorkloadContainersRoundTrip verifies the new containers JSONB
// column survives an upsert + read cycle on both Pod and Workload. Each entity
// type writes its own subset of fields into the generic map: Pod has
// image_id from containerStatuses; Workload template doesn't.
func TestPGPodAndWorkloadContainersRoundTrip(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "container-round-trip"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	podContainers := api.ContainerList{
		{"name": "app", "image": "nginx:1.25", "image_id": "docker.io/library/nginx@sha256:abc", "init": false},
		{"name": "migrate", "image": "busybox:1.36", "init": true},
	}
	storedPod, err := pg.UpsertPod(ctx, api.PodCreate{
		NamespaceId: *ns.Id,
		Name:        "web-0",
		Containers:  &podContainers,
	})
	if err != nil {
		t.Fatalf("upsert pod: %v", err)
	}
	got, err := pg.GetPod(ctx, *storedPod.Id)
	if err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if got.Containers == nil || len(*got.Containers) != 2 {
		t.Fatalf("pod containers after round-trip = %v, want 2 entries", got.Containers)
	}
	if (*got.Containers)[0]["image"] != "nginx:1.25" {
		t.Errorf("first container image = %v, want nginx:1.25", (*got.Containers)[0]["image"])
	}
	if (*got.Containers)[1]["init"] != true {
		t.Errorf("second container init = %v, want true", (*got.Containers)[1]["init"])
	}

	wlContainers := api.ContainerList{
		{"name": "app", "image": "nginx:1.25", "init": false},
	}
	storedWL, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{
		NamespaceId: *ns.Id,
		Kind:        api.Deployment,
		Name:        "web",
		Containers:  &wlContainers,
	})
	if err != nil {
		t.Fatalf("upsert workload: %v", err)
	}
	gotWL, err := pg.GetWorkload(ctx, *storedWL.Id)
	if err != nil {
		t.Fatalf("get workload: %v", err)
	}
	if gotWL.Containers == nil || len(*gotWL.Containers) != 1 {
		t.Fatalf("workload containers after round-trip = %v", gotWL.Containers)
	}
	if (*gotWL.Containers)[0]["image"] != "nginx:1.25" {
		t.Errorf("workload container image = %v", (*gotWL.Containers)[0]["image"])
	}
}

// Exercises the Pod -> Workload foreign key added in migration 00009:
//   - CreatePod / UpsertPod / UpdatePod all round-trip workload_id
//   - Unknown workload_id surfaces as ErrNotFound
//   - Deleting the parent Workload sets child pods' workload_id to NULL
//     (ON DELETE SET NULL) rather than cascading the pod away
func TestPGPodWorkloadFK(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "pod-wl-fk"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}
	wl, err := pg.CreateWorkload(ctx, api.WorkloadCreate{
		NamespaceId: *ns.Id,
		Kind:        api.Deployment,
		Name:        "web",
	})
	if err != nil {
		t.Fatalf("workload: %v", err)
	}

	// CreatePod with workload_id set.
	created, err := pg.CreatePod(ctx, api.PodCreate{
		NamespaceId: *ns.Id,
		Name:        "web-abc-1",
		WorkloadId:  wl.Id,
	})
	if err != nil {
		t.Fatalf("create pod: %v", err)
	}
	if created.WorkloadId == nil || *created.WorkloadId != *wl.Id {
		t.Errorf("created pod workload_id=%v, want %v", created.WorkloadId, wl.Id)
	}

	// GetPod round-trips the FK.
	got, err := pg.GetPod(ctx, *created.Id)
	if err != nil {
		t.Fatalf("get pod: %v", err)
	}
	if got.WorkloadId == nil || *got.WorkloadId != *wl.Id {
		t.Errorf("get pod workload_id=%v, want %v", got.WorkloadId, wl.Id)
	}

	// Unknown workload_id at create-time maps to ErrNotFound (FK violation,
	// disambiguated by classifyPodFKError against the namespace check).
	bogus := uuid.New()
	if _, err := pg.CreatePod(ctx, api.PodCreate{
		NamespaceId: *ns.Id,
		Name:        "bogus",
		WorkloadId:  &bogus,
	}); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("create pod with unknown workload_id: want ErrNotFound, got %v", err)
	}

	// UpsertPod updates workload_id on conflict.
	wl2, err := pg.CreateWorkload(ctx, api.WorkloadCreate{
		NamespaceId: *ns.Id,
		Kind:        api.StatefulSet,
		Name:        "web",
	})
	if err != nil {
		t.Fatalf("second workload: %v", err)
	}
	upserted, err := pg.UpsertPod(ctx, api.PodCreate{
		NamespaceId: *ns.Id,
		Name:        "web-abc-1",
		WorkloadId:  wl2.Id,
	})
	if err != nil {
		t.Fatalf("upsert pod: %v", err)
	}
	if upserted.WorkloadId == nil || *upserted.WorkloadId != *wl2.Id {
		t.Errorf("upsert pod workload_id=%v, want %v (repointed to sts)", upserted.WorkloadId, wl2.Id)
	}

	// UpdatePod re-points workload_id.
	updated, err := pg.UpdatePod(ctx, *created.Id, api.PodUpdate{WorkloadId: wl.Id})
	if err != nil {
		t.Fatalf("update pod: %v", err)
	}
	if updated.WorkloadId == nil || *updated.WorkloadId != *wl.Id {
		t.Errorf("updated pod workload_id=%v, want %v", updated.WorkloadId, wl.Id)
	}

	// ON DELETE SET NULL: removing the workload nulls the child pod's FK
	// rather than cascading the pod away.
	if err := pg.DeleteWorkload(ctx, *wl.Id); err != nil {
		t.Fatalf("delete workload: %v", err)
	}
	after, err := pg.GetPod(ctx, *created.Id)
	if err != nil {
		t.Fatalf("get pod after workload delete: %v", err)
	}
	if after.WorkloadId != nil {
		t.Errorf("pod workload_id=%v after parent delete, want nil (SET NULL)", after.WorkloadId)
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

// Exercises migrations 00010 + 00011: PV + PVC round-trip with FK, and
// ON DELETE SET NULL on bound_volume_id when the parent PV is removed.
func TestPGPersistentVolumeAndClaimFK(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "pv-fk"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	capacity := "10Gi"
	modes := []string{"ReadWriteOnce"}
	pv, err := pg.CreatePersistentVolume(ctx, api.PersistentVolumeCreate{
		ClusterId:   *cluster.Id,
		Name:        "pv-a",
		Capacity:    &capacity,
		AccessModes: &modes,
	})
	if err != nil {
		t.Fatalf("create pv: %v", err)
	}
	if pv.Capacity == nil || *pv.Capacity != "10Gi" {
		t.Errorf("pv capacity=%v, want 10Gi", pv.Capacity)
	}
	if pv.AccessModes == nil || (*pv.AccessModes)[0] != "ReadWriteOnce" {
		t.Errorf("pv access_modes=%v, want [ReadWriteOnce]", pv.AccessModes)
	}

	// PVC bound to the PV — FK round-trip.
	req := "5Gi"
	pvc, err := pg.CreatePersistentVolumeClaim(ctx, api.PersistentVolumeClaimCreate{
		NamespaceId:      *ns.Id,
		Name:             "data-0",
		VolumeName:       &pv.Name,
		BoundVolumeId:    pv.Id,
		RequestedStorage: &req,
	})
	if err != nil {
		t.Fatalf("create pvc: %v", err)
	}
	if pvc.BoundVolumeId == nil || *pvc.BoundVolumeId != *pv.Id {
		t.Errorf("pvc bound_volume_id=%v, want %v", pvc.BoundVolumeId, pv.Id)
	}

	// Unknown bound_volume_id at create-time -> ErrNotFound, disambiguated
	// by classifyPVCFKError against the namespace check.
	bogus := uuid.New()
	if _, err := pg.CreatePersistentVolumeClaim(ctx, api.PersistentVolumeClaimCreate{
		NamespaceId:   *ns.Id,
		Name:          "bogus",
		BoundVolumeId: &bogus,
	}); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("create pvc with unknown bound_volume_id: want ErrNotFound, got %v", err)
	}

	// Upsert round-trips the FK too.
	upserted, err := pg.UpsertPersistentVolumeClaim(ctx, api.PersistentVolumeClaimCreate{
		NamespaceId:      *ns.Id,
		Name:             "data-0",
		VolumeName:       &pv.Name,
		BoundVolumeId:    pv.Id,
		RequestedStorage: &req,
	})
	if err != nil {
		t.Fatalf("upsert pvc: %v", err)
	}
	if upserted.BoundVolumeId == nil || *upserted.BoundVolumeId != *pv.Id {
		t.Errorf("upserted bound_volume_id=%v, want %v", upserted.BoundVolumeId, pv.Id)
	}

	// ON DELETE SET NULL: removing the PV nulls the child PVC's FK rather
	// than cascading the PVC away.
	if err := pg.DeletePersistentVolume(ctx, *pv.Id); err != nil {
		t.Fatalf("delete pv: %v", err)
	}
	after, err := pg.GetPersistentVolumeClaim(ctx, *pvc.Id)
	if err != nil {
		t.Fatalf("get pvc after pv delete: %v", err)
	}
	if after.BoundVolumeId != nil {
		t.Errorf("pvc bound_volume_id=%v after parent delete, want nil (SET NULL)", after.BoundVolumeId)
	}

	// Cluster-scoped DeletePersistentVolumesNotIn — add a second PV then
	// reconcile to keep only one.
	if _, err := pg.CreatePersistentVolume(ctx, api.PersistentVolumeCreate{
		ClusterId: *cluster.Id,
		Name:      "pv-b",
	}); err != nil {
		t.Fatalf("create pv-b: %v", err)
	}
	if _, err := pg.CreatePersistentVolume(ctx, api.PersistentVolumeCreate{
		ClusterId: *cluster.Id,
		Name:      "pv-c",
	}); err != nil {
		t.Fatalf("create pv-c: %v", err)
	}
	n, err := pg.DeletePersistentVolumesNotIn(ctx, *cluster.Id, []string{"pv-b"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted=%d, want 1", n)
	}
	items, _, err := pg.ListPersistentVolumes(ctx, cluster.Id, 10, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Name != "pv-b" {
		t.Errorf("list=%v, want only pv-b", items)
	}
}

// Exercises the image-substring and node_name filters on ListPods and
// ListWorkloads (needed for the UI's image search + Node detail view).
// JSONB ILIKE over jsonb_array_elements('containers') is the load-bearing
// bit — we seed containers with a mix of matching / non-matching images
// and confirm the case-insensitive substring predicate picks only the hits.
func TestPGListFiltersForImageAndNode(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "filter-test"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	ns, err := pg.CreateNamespace(ctx, api.NamespaceCreate{ClusterId: *cluster.Id, Name: "apps"})
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	node1 := "worker-01"
	node2 := "worker-02"
	nginxContainers := api.ContainerList{{"name": "web", "image": "NGINX:1.27-alpine", "init": false}}
	log4jContainers := api.ContainerList{{"name": "app", "image": "registry.example.com/shop/api:1.4.2", "init": false}, {"name": "logger", "image": "log4j:2.15.0", "init": true}}
	otherContainers := api.ContainerList{{"name": "misc", "image": "busybox:1.36", "init": false}}

	if _, err := pg.UpsertPod(ctx, api.PodCreate{NamespaceId: *ns.Id, Name: "web-1", NodeName: &node1, Containers: &nginxContainers}); err != nil {
		t.Fatalf("web-1: %v", err)
	}
	if _, err := pg.UpsertPod(ctx, api.PodCreate{NamespaceId: *ns.Id, Name: "api-1", NodeName: &node1, Containers: &log4jContainers}); err != nil {
		t.Fatalf("api-1: %v", err)
	}
	if _, err := pg.UpsertPod(ctx, api.PodCreate{NamespaceId: *ns.Id, Name: "misc-1", NodeName: &node2, Containers: &otherContainers}); err != nil {
		t.Fatalf("misc-1: %v", err)
	}

	// node_name filter — only worker-01 pods.
	node1Filter := api.PodListFilter{NodeName: &node1}
	gotByNode, _, err := pg.ListPods(ctx, node1Filter, 10, "")
	if err != nil {
		t.Fatalf("list by node: %v", err)
	}
	if len(gotByNode) != 2 {
		t.Errorf("node=%s matched %d pods, want 2", node1, len(gotByNode))
	}

	// Image filter, case-insensitive substring — "nginx" matches NGINX:1.27-alpine only.
	nginxSub := "nginx"
	gotNginx, _, err := pg.ListPods(ctx, api.PodListFilter{ImageSubstring: &nginxSub}, 10, "")
	if err != nil {
		t.Fatalf("list by image nginx: %v", err)
	}
	if len(gotNginx) != 1 || gotNginx[0].Name != "web-1" {
		t.Errorf("image=nginx matched=%v, want [web-1]", gotNginx)
	}

	// Init containers participate — "log4j:2.15" hits api-1 via its init container.
	log4jSub := "log4j:2.15"
	gotLog4j, _, err := pg.ListPods(ctx, api.PodListFilter{ImageSubstring: &log4jSub}, 10, "")
	if err != nil {
		t.Fatalf("list by image log4j: %v", err)
	}
	if len(gotLog4j) != 1 || gotLog4j[0].Name != "api-1" {
		t.Errorf("image=log4j:2.15 matched=%v, want [api-1]", gotLog4j)
	}

	// Empty match returns no rows — no false positives.
	noMatch := "definitely-not-present"
	gotNone, _, err := pg.ListPods(ctx, api.PodListFilter{ImageSubstring: &noMatch}, 10, "")
	if err != nil {
		t.Fatalf("list by image none: %v", err)
	}
	if len(gotNone) != 0 {
		t.Errorf("image=bogus matched=%v, want none", gotNone)
	}

	// Combined: node + image filter AND together.
	both, _, err := pg.ListPods(ctx, api.PodListFilter{NodeName: &node1, ImageSubstring: &nginxSub}, 10, "")
	if err != nil {
		t.Fatalf("list combined: %v", err)
	}
	if len(both) != 1 || both[0].Name != "web-1" {
		t.Errorf("combined=%v, want [web-1]", both)
	}

	// Same image filter works for workloads.
	if _, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{NamespaceId: *ns.Id, Kind: api.Deployment, Name: "api", Containers: &log4jContainers}); err != nil {
		t.Fatalf("workload api: %v", err)
	}
	if _, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{NamespaceId: *ns.Id, Kind: api.Deployment, Name: "web", Containers: &nginxContainers}); err != nil {
		t.Fatalf("workload web: %v", err)
	}
	wls, _, err := pg.ListWorkloads(ctx, api.WorkloadListFilter{ImageSubstring: &log4jSub}, 10, "")
	if err != nil {
		t.Fatalf("list workloads: %v", err)
	}
	if len(wls) != 1 || wls[0].Name != "api" {
		t.Errorf("workload image=log4j matched=%v, want [api]", wls)
	}
}

// Exercises migration 00014 via the full auth CRUD path: user creation
// (argon2id storage), case-insensitive username lookup, session
// lifecycle, forced password rotation cascading to session deletion,
// machine-token minting + prefix lookup + argon2id verify + revocation.
// The bits the middleware hits live behind auth.Store — covered here
// too via the PG impl so CI catches regressions.
func TestPGAuthSubstrate(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// --- user creation + case-insensitive lookup --------------------
	hash, err := auth.HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := pg.CreateUser(ctx, api.UserInsert{
		Username:           "Alice",
		PasswordHash:       hash,
		Role:               auth.RoleAdmin,
		MustChangePassword: true,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if u.Id == nil {
		t.Fatal("created user missing id")
	}

	// Case-insensitive username uniqueness.
	if _, err := pg.CreateUser(ctx, api.UserInsert{
		Username: "ALICE", PasswordHash: hash, Role: auth.RoleViewer,
	}); !errors.Is(err, api.ErrConflict) {
		t.Errorf("case-insensitive dup: got %v, want ErrConflict", err)
	}

	// Lookup via GetUserByUsername matches case-insensitively.
	got, err := pg.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("get by username: %v", err)
	}
	if got.PasswordHash != hash {
		t.Errorf("hash mismatch on read-back")
	}
	if err := auth.VerifyPassword("correct-horse-battery-staple", got.PasswordHash); err != nil {
		t.Errorf("verify after round-trip: %v", err)
	}

	// --- count active admins --------------------------------------
	n, err := pg.CountActiveAdmins(ctx)
	if err != nil {
		t.Fatalf("count admins: %v", err)
	}
	if n != 1 {
		t.Errorf("admin count=%d, want 1", n)
	}

	// --- sessions ------------------------------------------------
	sid, err := auth.RandomSecret(32)
	if err != nil {
		t.Fatalf("mint session id: %v", err)
	}
	now := time.Now().UTC()
	expires := now.Add(auth.SessionDuration)
	if err := pg.CreateSession(ctx, api.SessionInsert{
		ID:        sid,
		UserID:    *u.Id,
		CreatedAt: now,
		ExpiresAt: expires,
		UserAgent: "go-test/1.0",
		SourceIP:  "10.0.0.1",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	active, err := pg.GetActiveSession(ctx, sid)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if active.UserID != *u.Id {
		t.Errorf("session user mismatch")
	}

	// Touch extends expiry.
	later := now.Add(time.Hour)
	newExpiry := later.Add(auth.SessionDuration)
	if err := pg.TouchSession(ctx, sid, later, newExpiry); err != nil {
		t.Fatalf("touch: %v", err)
	}

	// --- password change cascades to session revocation -----------
	newHash, _ := auth.HashPassword("another-good-passphrase")
	if err := pg.SetUserPassword(ctx, *u.Id, newHash, false); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if _, err := pg.GetActiveSession(ctx, sid); !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("session after password change: got %v, want ErrUnauthorized", err)
	}

	// --- api token mint + verify + revoke -----------------------
	minted, err := auth.MintToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	tok, err := pg.CreateAPIToken(ctx, api.APITokenInsert{
		ID:              uuidMustParse(t, (*u.Id).String()),
		Name:            "ci",
		Prefix:          minted.Prefix,
		Hash:            minted.Hash,
		Scopes:          []string{auth.ScopeRead, auth.ScopeWrite},
		CreatedByUserID: *u.Id,
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	got2, err := pg.GetActiveTokenByPrefix(ctx, minted.Prefix)
	if err != nil {
		t.Fatalf("lookup by prefix: %v", err)
	}
	if err := auth.VerifyPassword(minted.Plaintext, got2.Hash); err != nil {
		t.Errorf("verify token hash: %v", err)
	}

	if err := pg.RevokeAPIToken(ctx, *tok.Id, time.Now().UTC()); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := pg.GetActiveTokenByPrefix(ctx, minted.Prefix); !errors.Is(err, auth.ErrUnauthorized) {
		t.Errorf("revoked token still looks active: %v", err)
	}
	// Second revoke is idempotent.
	if err := pg.RevokeAPIToken(ctx, *tok.Id, time.Now().UTC()); err != nil {
		t.Errorf("second revoke: %v", err)
	}

	// --- FK RESTRICT on user delete with outstanding tokens -----
	if err := pg.DeleteUser(ctx, *u.Id); !errors.Is(err, api.ErrConflict) {
		t.Errorf("delete user with active tokens: got %v, want ErrConflict", err)
	}
}

func uuidMustParse(t *testing.T, s string) uuid.UUID {
	t.Helper()
	// In this test we want a brand-new UUID — the argument is only
	// used to satisfy the signature; return uuid.New().
	return uuid.New()
}

// Exercises the enriched Node columns added in migration 00012. Upserts
// a node with a full Mercator-aligned payload (role / cloud identity /
// OS stack / capacity+allocatable pairs / conditions / taints) and
// confirms every field round-trips through scanNode.
func TestPGNodeEnrichmentRoundTrip(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, err := pg.CreateCluster(ctx, api.ClusterCreate{Name: "node-enrich"})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}

	role := "worker"
	kubeletV := "v1.30.3"
	kubeProxyV := "v1.30.3"
	runtimeV := "containerd://1.7.13"
	osImage := "Bottlerocket OS 1.20.0"
	operatingSys := "linux"
	kernelV := "6.1.84"
	arch := "amd64"
	internalIP := "10.0.1.23"
	externalIP := "54.12.34.56"
	podCIDR := "10.244.1.0/24"
	providerID := "aws:///eu-west-1a/i-0abc1234567890def"
	inst := "m6i.xlarge"
	zone := "eu-west-1a"
	capCPU := "4"
	capMem := "16Gi"
	capPods := "110"
	capEph := "100Gi"
	allocCPU := "3900m"
	allocMem := "15Gi"
	allocPods := "110"
	allocEph := "95Gi"
	unschedulable := false
	ready := true
	conditions := []map[string]interface{}{
		{"type": "Ready", "status": "True", "reason": "KubeletReady", "message": "kubelet is posting ready status"},
		{"type": "MemoryPressure", "status": "False", "reason": "KubeletHasSufficientMemory"},
	}
	taints := []map[string]interface{}{
		{"key": "dedicated", "value": "gpu", "effect": "NoSchedule"},
	}
	labels := map[string]string{"node.kubernetes.io/instance-type": inst}

	in := api.NodeCreate{
		ClusterId:                   *cluster.Id,
		Name:                        "worker-01",
		Role:                        &role,
		KubeletVersion:              &kubeletV,
		KubeProxyVersion:            &kubeProxyV,
		ContainerRuntimeVersion:     &runtimeV,
		OsImage:                     &osImage,
		OperatingSystem:             &operatingSys,
		KernelVersion:               &kernelV,
		Architecture:                &arch,
		InternalIp:                  &internalIP,
		ExternalIp:                  &externalIP,
		PodCidr:                     &podCIDR,
		ProviderId:                  &providerID,
		InstanceType:                &inst,
		Zone:                        &zone,
		CapacityCpu:                 &capCPU,
		CapacityMemory:              &capMem,
		CapacityPods:                &capPods,
		CapacityEphemeralStorage:    &capEph,
		AllocatableCpu:              &allocCPU,
		AllocatableMemory:           &allocMem,
		AllocatablePods:             &allocPods,
		AllocatableEphemeralStorage: &allocEph,
		Conditions:                  &conditions,
		Taints:                      &taints,
		Unschedulable:               &unschedulable,
		Ready:                       &ready,
		Labels:                      &labels,
	}

	stored, err := pg.UpsertNode(ctx, in)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := pg.GetNode(ctx, *stored.Id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// Spot-check a representative sample of every field family. String
	// equality is enough since every setter is a plain assignment.
	wantStrPairs := []struct{ name, want string; got *string }{
		{"role", role, got.Role},
		{"kubelet_version", kubeletV, got.KubeletVersion},
		{"kube_proxy_version", kubeProxyV, got.KubeProxyVersion},
		{"container_runtime_version", runtimeV, got.ContainerRuntimeVersion},
		{"operating_system", operatingSys, got.OperatingSystem},
		{"kernel_version", kernelV, got.KernelVersion},
		{"internal_ip", internalIP, got.InternalIp},
		{"external_ip", externalIP, got.ExternalIp},
		{"pod_cidr", podCIDR, got.PodCidr},
		{"provider_id", providerID, got.ProviderId},
		{"instance_type", inst, got.InstanceType},
		{"zone", zone, got.Zone},
		{"capacity_cpu", capCPU, got.CapacityCpu},
		{"capacity_memory", capMem, got.CapacityMemory},
		{"capacity_ephemeral_storage", capEph, got.CapacityEphemeralStorage},
		{"allocatable_cpu", allocCPU, got.AllocatableCpu},
	}
	for _, p := range wantStrPairs {
		if p.got == nil || *p.got != p.want {
			t.Errorf("%s: got %v, want %q", p.name, p.got, p.want)
		}
	}

	if got.Ready == nil || !*got.Ready {
		t.Errorf("ready: got %v, want true", got.Ready)
	}
	if got.Unschedulable == nil || *got.Unschedulable {
		t.Errorf("unschedulable: got %v, want false", got.Unschedulable)
	}

	if got.Conditions == nil || len(*got.Conditions) != 2 {
		t.Fatalf("conditions: got %v, want 2 entries", got.Conditions)
	}
	if (*got.Conditions)[0]["type"] != "Ready" {
		t.Errorf("first condition type = %v, want Ready", (*got.Conditions)[0]["type"])
	}

	if got.Taints == nil || len(*got.Taints) != 1 {
		t.Fatalf("taints: got %v, want 1 entry", got.Taints)
	}
	if (*got.Taints)[0]["effect"] != "NoSchedule" {
		t.Errorf("taint effect = %v, want NoSchedule", (*got.Taints)[0]["effect"])
	}

	// Upsert again with a *different* role and fewer conditions to verify
	// the ON CONFLICT DO UPDATE path rewrites everything atomically.
	cordoned := "control-plane"
	empty := []map[string]interface{}{}
	unschTrue := true
	reDown := false
	in2 := in
	in2.Role = &cordoned
	in2.Conditions = &empty
	in2.Unschedulable = &unschTrue
	in2.Ready = &reDown
	if _, err := pg.UpsertNode(ctx, in2); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got2, err := pg.GetNode(ctx, *stored.Id)
	if err != nil {
		t.Fatalf("re-get: %v", err)
	}
	if got2.Role == nil || *got2.Role != cordoned {
		t.Errorf("role after re-upsert: got %v, want %q", got2.Role, cordoned)
	}
	if got2.Unschedulable == nil || !*got2.Unschedulable {
		t.Errorf("unschedulable after re-upsert: got %v, want true", got2.Unschedulable)
	}
	if got2.Ready == nil || *got2.Ready {
		t.Errorf("ready after re-upsert: got %v, want false", got2.Ready)
	}
	if got2.Conditions != nil {
		t.Errorf("conditions after clear: got %v, want nil", got2.Conditions)
	}
}

// Exercises the OIDC-specific store methods: the (issuer, subject) → user
// shadow mapping, atomic create-with-identity, and the one-shot auth
// state consume + sweep.
func TestPGOIDCShadowUsersAndAuthState(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// --- CreateUserWithIdentity + GetUserByIdentity round-trip ------
	u, err := pg.CreateUserWithIdentity(ctx,
		api.UserInsert{
			Username:     "alice-oidc",
			PasswordHash: "$argon2id$shadow$oidc",
			Role:         auth.RoleViewer,
		},
		api.UserIdentityInsert{
			Issuer:  "https://idp.example.com",
			Subject: "sub-alice",
			Email:   "alice@example.com",
		},
	)
	if err != nil {
		t.Fatalf("create user with identity: %v", err)
	}
	if u.Id == nil {
		t.Fatal("created user missing id")
	}

	got, err := pg.GetUserByIdentity(ctx, "https://idp.example.com", "sub-alice")
	if err != nil {
		t.Fatalf("get by identity: %v", err)
	}
	if *got.Id != *u.Id {
		t.Errorf("identity lookup returned wrong user")
	}

	// --- duplicate (issuer, subject) rejected -----------------------
	if _, err := pg.CreateUserWithIdentity(ctx,
		api.UserInsert{
			Username: "bob-oidc", PasswordHash: "$argon2id$shadow$oidc", Role: auth.RoleViewer,
		},
		api.UserIdentityInsert{
			Issuer: "https://idp.example.com", Subject: "sub-alice",
		},
	); !errors.Is(err, api.ErrConflict) {
		t.Errorf("dup identity: got %v, want ErrConflict", err)
	}

	// --- missing identity returns ErrNotFound, not some other error --
	if _, err := pg.GetUserByIdentity(ctx, "https://idp.example.com", "no-such-sub"); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("unknown identity: got %v, want ErrNotFound", err)
	}

	// --- TouchUserIdentity updates last_seen_at --------------------
	before := time.Now().UTC()
	later := before.Add(2 * time.Minute)
	if err := pg.TouchUserIdentity(ctx, *u.Id, "https://idp.example.com", "sub-alice", later); err != nil {
		t.Fatalf("touch identity: %v", err)
	}

	// --- disabled user is filtered out of GetUserByIdentity --------
	disabled := true
	if _, err := pg.UpdateUser(ctx, *u.Id, api.UserPatch{Disabled: &disabled}); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if _, err := pg.GetUserByIdentity(ctx, "https://idp.example.com", "sub-alice"); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("disabled-user identity lookup: got %v, want ErrNotFound", err)
	}

	// --- OIDC auth state: consume is one-shot ----------------------
	state, err := auth.GenerateOIDCState()
	if err != nil {
		t.Fatalf("mint state: %v", err)
	}
	now := time.Now().UTC()
	if err := pg.CreateOidcAuthState(ctx, api.OidcAuthStateInsert{
		State:        state,
		CodeVerifier: "verifier-123",
		Nonce:        "nonce-abc",
		CreatedAt:    now,
		ExpiresAt:    now.Add(5 * time.Minute),
	}); err != nil {
		t.Fatalf("create auth state: %v", err)
	}

	v, n, err := pg.ConsumeOidcAuthState(ctx, state)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if v != "verifier-123" || n != "nonce-abc" {
		t.Errorf("wrong payload: verifier=%q nonce=%q", v, n)
	}

	// Second consume on the same state must miss (one-shot).
	if _, _, err := pg.ConsumeOidcAuthState(ctx, state); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("second consume: got %v, want ErrNotFound", err)
	}

	// --- expired row is swept, not returned ------------------------
	expiredState, _ := auth.GenerateOIDCState()
	past := now.Add(-10 * time.Minute)
	if err := pg.CreateOidcAuthState(ctx, api.OidcAuthStateInsert{
		State:        expiredState,
		CodeVerifier: "v",
		Nonce:        "n",
		CreatedAt:    past,
		ExpiresAt:    now.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("create expired state: %v", err)
	}
	if _, _, err := pg.ConsumeOidcAuthState(ctx, expiredState); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("expired consume: got %v, want ErrNotFound", err)
	}
}
