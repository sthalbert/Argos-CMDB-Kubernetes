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
