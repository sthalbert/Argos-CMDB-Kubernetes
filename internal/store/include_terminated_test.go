package store

import (
	"context"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

// TestListNodesIncludeTerminated verifies that ListNodes correctly filters
// terminated nodes based on the includeTerminated parameter (ADR-0021 phase 1).
//
//nolint:gocyclo // test-fixture sequence; complexity from straight-line setup-then-assertions
func TestListNodesIncludeTerminated(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// Create a cluster.
	clusterName := "test-cluster-include-terminated"
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: clusterName})
	if err != nil {
		t.Fatalf("EnsureCluster: %v", err)
	}
	if cluster.Id == nil {
		t.Fatal("cluster.Id is nil")
	}

	// Create three nodes: two live and one to be soft-deleted.
	node1, _, err := pg.UpsertNode(ctx, api.NodeCreate{
		ClusterId: *cluster.Id,
		Name:      "live-1",
	})
	if err != nil {
		t.Fatalf("UpsertNode live-1: %v", err)
	}
	if node1.Id == nil {
		t.Fatal("node1.Id is nil")
	}

	node2, _, err := pg.UpsertNode(ctx, api.NodeCreate{
		ClusterId: *cluster.Id,
		Name:      "live-2",
	})
	if err != nil {
		t.Fatalf("UpsertNode live-2: %v", err)
	}
	if node2.Id == nil {
		t.Fatal("node2.Id is nil")
	}

	node3, _, err := pg.UpsertNode(ctx, api.NodeCreate{
		ClusterId: *cluster.Id,
		Name:      "soon-dead",
	})
	if err != nil {
		t.Fatalf("UpsertNode soon-dead: %v", err)
	}
	if node3.Id == nil {
		t.Fatal("node3.Id is nil")
	}

	// Soft-delete node3 via DeleteNodesNotIn (keeps live-1 and live-2, deletes soon-dead).
	deleted, err := pg.DeleteNodesNotIn(ctx, *cluster.Id, []string{"live-1", "live-2"})
	if err != nil {
		t.Fatalf("DeleteNodesNotIn: %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteNodesNotIn deleted %d rows, want 1", deleted)
	}

	// Note: We can't directly verify TerminatedAt from the API since it's not exposed,
	// but the soft-delete did work (DeleteNodesNotIn returned 1 row deleted).

	// Test 1: ListNodes with includeTerminated=false (default).
	// Should return 2 nodes (live-1 and live-2).
	items, _, err := pg.ListNodes(ctx, cluster.Id, 0, "", false)
	if err != nil {
		t.Fatalf("ListNodes (includeTerminated=false): %v", err)
	}
	if len(items) != 2 {
		t.Errorf("ListNodes (includeTerminated=false) returned %d items, want 2", len(items))
		for i, n := range items {
			t.Logf("  [%d] %s", i, n.Name)
		}
	}

	// Test 2: ListNodes with includeTerminated=true.
	// Should return 3 nodes (live-1, live-2, and soon-dead).
	items, _, err = pg.ListNodes(ctx, cluster.Id, 0, "", true)
	if err != nil {
		t.Fatalf("ListNodes (includeTerminated=true): %v", err)
	}
	if len(items) != 3 {
		t.Errorf("ListNodes (includeTerminated=true) returned %d items, want 3", len(items))
		for i, n := range items {
			t.Logf("  [%d] %s", i, n.Name)
		}
	}

	// Verify that soon-dead is in the included list.
	found := false
	for _, n := range items {
		if n.Name == "soon-dead" {
			found = true
			break
		}
	}
	if !found {
		t.Error("soon-dead node not found in included list")
	}
}

// TestListNamespacesIncludeTerminated verifies that ListNamespaces correctly filters
// terminated namespaces based on the includeTerminated parameter (ADR-0021 phase 1).
//
//nolint:gocyclo // test-fixture sequence; complexity from straight-line setup-then-assertions
func TestListNamespacesIncludeTerminated(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// Create a cluster.
	clusterName := "test-cluster-ns-include-terminated"
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: clusterName})
	if err != nil {
		t.Fatalf("EnsureCluster: %v", err)
	}
	if cluster.Id == nil {
		t.Fatal("cluster.Id is nil")
	}

	// Create three namespaces: two live and one to be soft-deleted.
	ns1, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{
		ClusterId: *cluster.Id,
		Name:      "ns-live-1",
	})
	if err != nil {
		t.Fatalf("UpsertNamespace ns-live-1: %v", err)
	}
	if ns1.Id == nil {
		t.Fatal("ns1.Id is nil")
	}

	ns2, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{
		ClusterId: *cluster.Id,
		Name:      "ns-live-2",
	})
	if err != nil {
		t.Fatalf("UpsertNamespace ns-live-2: %v", err)
	}
	if ns2.Id == nil {
		t.Fatal("ns2.Id is nil")
	}

	ns3, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{
		ClusterId: *cluster.Id,
		Name:      "ns-soon-dead",
	})
	if err != nil {
		t.Fatalf("UpsertNamespace ns-soon-dead: %v", err)
	}
	if ns3.Id == nil {
		t.Fatal("ns3.Id is nil")
	}

	// Soft-delete ns3 via DeleteNamespacesNotIn.
	deleted, err := pg.DeleteNamespacesNotIn(ctx, *cluster.Id, []string{"ns-live-1", "ns-live-2"})
	if err != nil {
		t.Fatalf("DeleteNamespacesNotIn: %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteNamespacesNotIn deleted %d rows, want 1", deleted)
	}

	// Test 1: ListNamespaces with includeTerminated=false (default).
	// Should return 2 namespaces.
	items, _, err := pg.ListNamespaces(ctx, cluster.Id, 0, "", false)
	if err != nil {
		t.Fatalf("ListNamespaces (includeTerminated=false): %v", err)
	}
	if len(items) != 2 {
		t.Errorf("ListNamespaces (includeTerminated=false) returned %d items, want 2", len(items))
		for i, ns := range items {
			t.Logf("  [%d] %s", i, ns.Name)
		}
	}

	// Test 2: ListNamespaces with includeTerminated=true.
	// Should return 3 namespaces.
	items, _, err = pg.ListNamespaces(ctx, cluster.Id, 0, "", true)
	if err != nil {
		t.Fatalf("ListNamespaces (includeTerminated=true): %v", err)
	}
	if len(items) != 3 {
		t.Errorf("ListNamespaces (includeTerminated=true) returned %d items, want 3", len(items))
		for i, ns := range items {
			t.Logf("  [%d] %s", i, ns.Name)
		}
	}
}

// TestListClustersIncludeTerminated verifies that ListClusters correctly filters
// terminated clusters based on the includeTerminated parameter (ADR-0021 phase 1).
func TestListClustersIncludeTerminated(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// Create two clusters.
	cluster1, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{
		Name: "live-cluster-" + string(rune(len("test"))),
	})
	if err != nil {
		t.Fatalf("EnsureCluster 1: %v", err)
	}

	cluster2, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{
		Name: "soon-deleted-" + string(rune(len("test"))),
	})
	if err != nil {
		t.Fatalf("EnsureCluster 2: %v", err)
	}

	// Note: Clusters don't have soft-delete yet (only nodes, namespaces, and workloads do in phase 1).
	// This test just verifies the parameter is accepted.

	// Test 1: ListClusters with includeTerminated=false should return 2.
	items, _, err := pg.ListClusters(ctx, 0, "", false)
	if err != nil {
		t.Fatalf("ListClusters (includeTerminated=false): %v", err)
	}
	if len(items) != 2 {
		t.Errorf("ListClusters (includeTerminated=false) returned %d items, want 2", len(items))
	}

	// Test 2: ListClusters with includeTerminated=true should also return 2 (no deletions).
	items, _, err = pg.ListClusters(ctx, 0, "", true)
	if err != nil {
		t.Fatalf("ListClusters (includeTerminated=true): %v", err)
	}
	if len(items) != 2 {
		t.Errorf("ListClusters (includeTerminated=true) returned %d items, want 2", len(items))
	}

	_ = cluster1 // unused
	_ = cluster2 // unused
}
