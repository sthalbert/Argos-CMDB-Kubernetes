//nolint:goconst // duplicated literals in assertions are clearer than named constants.
package impact

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// graph fixture builder ------------------------------------------------------

type fixture struct {
	store *fakeStore

	clusterID uuid.UUID
	nodeAID   uuid.UUID
	nsAID     uuid.UUID
	wlID      uuid.UUID
	podID     uuid.UUID
	svcID     uuid.UUID
	ingID     uuid.UUID
	pvID      uuid.UUID
	pvcID     uuid.UUID
}

// newFixture builds a small but complete cluster:
//
//	cluster
//	├── node-a
//	├── ns-a
//	│   ├── workload (Deployment) → pod (on node-a)
//	│   ├── service
//	│   ├── ingress
//	│   └── pvc ── bound ──┐
//	└── pv  ←──────────────┘
func newFixture() *fixture {
	f := &fixture{
		store:     newFakeStore(),
		clusterID: uuid.New(),
		nodeAID:   uuid.New(),
		nsAID:     uuid.New(),
		wlID:      uuid.New(),
		podID:     uuid.New(),
		svcID:     uuid.New(),
		ingID:     uuid.New(),
		pvID:      uuid.New(),
		pvcID:     uuid.New(),
	}

	f.store.clusters = []api.Cluster{{
		Id:                ptrUUID(f.clusterID),
		Name:              "prod",
		KubernetesVersion: ptrStrLit("1.29.5"),
	}}
	f.store.nodes = []api.Node{{
		Id:        ptrUUID(f.nodeAID),
		ClusterId: f.clusterID,
		Name:      "node-a",
		Ready:     ptrBoolLit(true),
	}}
	f.store.nss = []api.Namespace{{
		Id:        ptrUUID(f.nsAID),
		ClusterId: f.clusterID,
		Name:      "ns-a",
		Phase:     ptrStrLit("Active"),
	}}
	f.store.wls = []api.Workload{{
		Id:            ptrUUID(f.wlID),
		NamespaceId:   f.nsAID,
		Name:          "web",
		Kind:          api.WorkloadKind("Deployment"),
		Replicas:      ptrIntLit(3),
		ReadyReplicas: ptrIntLit(3),
	}}
	nodeName := "node-a"
	f.store.pods = []api.Pod{{
		Id:          ptrUUID(f.podID),
		NamespaceId: f.nsAID,
		Name:        "web-xyz",
		NodeName:    &nodeName,
		WorkloadId:  ptrUUID(f.wlID),
		Phase:       ptrStrLit("Running"),
	}}
	f.store.svcs = []api.Service{{
		Id:          ptrUUID(f.svcID),
		NamespaceId: f.nsAID,
		Name:        "web-svc",
	}}
	f.store.ings = []api.Ingress{{
		Id:          ptrUUID(f.ingID),
		NamespaceId: f.nsAID,
		Name:        "web-ing",
	}}
	f.store.pvs = []api.PersistentVolume{{
		Id:        ptrUUID(f.pvID),
		ClusterId: f.clusterID,
		Name:      "pv-1",
		Phase:     ptrStrLit("Bound"),
	}}
	f.store.pvcs = []api.PersistentVolumeClaim{{
		Id:            ptrUUID(f.pvcID),
		NamespaceId:   f.nsAID,
		Name:          "data-claim",
		Phase:         ptrStrLit("Bound"),
		BoundVolumeId: ptrUUID(f.pvID),
	}}

	return f
}

// helpers ---------------------------------------------------------------------

func nodeIDs(g *Graph) map[string]EntityType {
	out := map[string]EntityType{}
	for _, n := range g.Nodes {
		out[n.ID] = n.Type
	}
	return out
}

func hasNode(g *Graph, id uuid.UUID, t EntityType) bool {
	want := id.String()
	for _, n := range g.Nodes {
		if n.ID == want && n.Type == t {
			return true
		}
	}
	return false
}

func hasEdge(g *Graph, from, to uuid.UUID, rel Relation) bool {
	f, t := from.String(), to.String()
	for _, e := range g.Edges {
		if e.From == f && e.To == t && e.Relation == rel {
			return true
		}
	}
	return false
}

// tests -----------------------------------------------------------------------

func TestValidEntityType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"cluster", "cluster", true},
		{"node", "node", true},
		{"persistentvolumeclaim", "persistentvolumeclaim", true},
		{"empty string", "", false},
		{"wrong case", "Cluster", false},
		{"unknown", "namespace_x", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidEntityType(tc.in); got != tc.want {
				t.Errorf("ValidEntityType(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestTraverse_UnknownEntityType(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	tr := NewTraverser(store)

	_, err := tr.Traverse(context.Background(), EntityType("not-a-thing"), uuid.New(), 2)
	if err == nil {
		t.Fatal("expected error for unknown entity type")
	}
	if !errors.Is(err, ErrUnknownType) {
		t.Errorf("err = %v; want wrapped ErrUnknownType", err)
	}
}

func TestTraverse_RootNotFound(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	tr := NewTraverser(store)

	_, err := tr.Traverse(context.Background(), TypeCluster, uuid.New(), 2)
	if err == nil {
		t.Fatal("expected error when root cluster missing")
	}
	if !errors.Is(err, api.ErrNotFound) {
		t.Errorf("err = %v; want wrapped api.ErrNotFound", err)
	}
}

func TestTraverse_ClusterDownstream_Depth1(t *testing.T) {
	// Depth 1 from a cluster reaches its direct children: nodes,
	// namespaces, PVs — but NOT grand-children (workloads, pods).
	t.Parallel()
	f := newFixture()
	tr := NewTraverser(f.store)

	g, err := tr.Traverse(context.Background(), TypeCluster, f.clusterID, 1)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	if g.Root.Type != TypeCluster || g.Root.Status != "1.29.5" {
		t.Errorf("root = %+v; want cluster with status=1.29.5", g.Root)
	}
	if !hasNode(g, f.nodeAID, TypeNode) {
		t.Error("missing node-a in graph")
	}
	if !hasNode(g, f.nsAID, TypeNamespace) {
		t.Error("missing ns-a in graph")
	}
	if !hasNode(g, f.pvID, TypePersistentVolume) {
		t.Error("missing pv-1 in graph")
	}
	if !hasEdge(g, f.clusterID, f.nodeAID, RelContains) {
		t.Error("missing edge cluster→node (contains)")
	}
	// Depth 1 stops before workloads/pods are explored from the
	// namespace, so they should NOT appear.
	if _, present := nodeIDs(g)[f.podID.String()]; present {
		t.Error("pod should not appear at depth 1 from cluster")
	}
}

func TestTraverse_ClusterDownstream_Depth2(t *testing.T) {
	// Depth 2 picks up the namespace's workloads/services/PVCs and
	// the node's pods.
	t.Parallel()
	f := newFixture()
	tr := NewTraverser(f.store)

	g, err := tr.Traverse(context.Background(), TypeCluster, f.clusterID, 2)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	want := map[uuid.UUID]EntityType{
		f.clusterID: TypeCluster,
		f.nodeAID:   TypeNode,
		f.nsAID:     TypeNamespace,
		f.wlID:      TypeWorkload,
		f.svcID:     TypeService,
		f.ingID:     TypeIngress,
		f.pvID:      TypePersistentVolume,
		f.pvcID:     TypePersistentVolumeClaim,
		f.podID:     TypePod,
	}
	for id, kind := range want {
		if !hasNode(g, id, kind) {
			t.Errorf("missing %s/%s at depth 2", kind, id)
		}
	}
}

func TestTraverse_PodWalksUpstream(t *testing.T) {
	// From a pod, depth 1 must reach namespace, workload, and node.
	t.Parallel()
	f := newFixture()
	tr := NewTraverser(f.store)

	g, err := tr.Traverse(context.Background(), TypePod, f.podID, 1)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if !hasNode(g, f.nsAID, TypeNamespace) {
		t.Error("pod traversal missed namespace")
	}
	if !hasNode(g, f.wlID, TypeWorkload) {
		t.Error("pod traversal missed workload")
	}
	if !hasNode(g, f.nodeAID, TypeNode) {
		t.Error("pod traversal missed node")
	}
	if !hasEdge(g, f.wlID, f.podID, RelOwns) {
		t.Error("missing workload→pod owns edge")
	}
	if !hasEdge(g, f.nodeAID, f.podID, RelHosts) {
		t.Error("missing node→pod hosts edge")
	}
}

func TestTraverse_PVCBoundToPV(t *testing.T) {
	t.Parallel()
	f := newFixture()
	tr := NewTraverser(f.store)

	g, err := tr.Traverse(context.Background(), TypePersistentVolumeClaim, f.pvcID, 1)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if !hasNode(g, f.pvID, TypePersistentVolume) {
		t.Error("pvc traversal missed bound pv")
	}
	if !hasEdge(g, f.pvID, f.pvcID, RelBinds) {
		t.Error("missing pv→pvc binds edge")
	}
}

func TestTraverse_PVBoundFromPVC(t *testing.T) {
	t.Parallel()
	f := newFixture()
	tr := NewTraverser(f.store)

	g, err := tr.Traverse(context.Background(), TypePersistentVolume, f.pvID, 2)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if !hasNode(g, f.pvcID, TypePersistentVolumeClaim) {
		t.Error("pv traversal missed bound pvc")
	}
	if !hasEdge(g, f.pvID, f.pvcID, RelBinds) {
		t.Error("missing pv→pvc binds edge from PV side")
	}
}

func TestTraverse_NoDuplicateNodesOrEdges(t *testing.T) {
	// The cluster ↔ namespace ↔ pod ↔ workload graph is reachable by
	// multiple paths (e.g., cluster → ns → workload, and pod → workload).
	// Depth 3 explores aggressively; we verify the graph stays a set.
	t.Parallel()
	f := newFixture()
	tr := NewTraverser(f.store)

	g, err := tr.Traverse(context.Background(), TypeCluster, f.clusterID, 3)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}

	seenNodes := map[string]bool{}
	for _, n := range g.Nodes {
		key := string(n.Type) + ":" + n.ID
		if seenNodes[key] {
			t.Errorf("duplicate node %s", key)
		}
		seenNodes[key] = true
	}
	seenEdges := map[string]bool{}
	for _, e := range g.Edges {
		key := e.From + "->" + e.To + ":" + string(e.Relation)
		if seenEdges[key] {
			t.Errorf("duplicate edge %s", key)
		}
		seenEdges[key] = true
	}
}

func TestTraverse_TruncatedAtMaxNodes(t *testing.T) {
	// Build a cluster with more nodes than the truncation cap allows.
	t.Parallel()
	store := newFakeStore()
	clusterID := uuid.New()
	store.clusters = []api.Cluster{{Id: ptrUUID(clusterID), Name: "big"}}
	for range 10 {
		store.nodes = append(store.nodes, api.Node{
			Id: ptrUUID(uuid.New()), ClusterId: clusterID, Name: "n",
		})
	}

	b := &builder{
		store:    store,
		seen:     map[string]bool{},
		nodeMap:  map[string]GraphNode{},
		edgeMap:  map[string]bool{},
		maxDepth: 2,
		maxNodes: 5, // small cap forces truncation
	}
	root, err := b.fetchNode(context.Background(), TypeCluster, clusterID)
	if err != nil {
		t.Fatalf("fetchNode: %v", err)
	}
	b.addNode(*root)
	if err := b.expand(context.Background(), *root, 0); err != nil {
		t.Fatalf("expand: %v", err)
	}
	if !b.truncated {
		t.Error("expected builder.truncated to be true once cap is hit")
	}
	if len(b.nodeMap) > b.maxNodes {
		t.Errorf("nodeMap = %d; want ≤ %d", len(b.nodeMap), b.maxNodes)
	}
}

func TestTraverse_StoreErrorPropagates(t *testing.T) {
	t.Parallel()
	f := newFixture()
	wantErr := errors.New("db down")
	f.store.errOn["ListNodes"] = wantErr
	tr := NewTraverser(f.store)

	_, err := tr.Traverse(context.Background(), TypeCluster, f.clusterID, 2)
	if err == nil {
		t.Fatal("expected error from store")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v; want wrapping %v", err, wantErr)
	}
}

func TestDisplayOrName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		display *string
		fallbk  string
		want    string
	}{
		{"display set", ptrStrLit("Production"), "prod", "Production"},
		{"display empty", ptrStrLit(""), "prod", "prod"},
		{"display nil", nil, "prod", "prod"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := displayOrName(tc.display, tc.fallbk); got != tc.want {
				t.Errorf("displayOrName(%v, %q) = %q; want %q", tc.display, tc.fallbk, got, tc.want)
			}
		})
	}
}

func TestReadyStatus(t *testing.T) {
	t.Parallel()
	if got := readyStatus(nil); got != "NotReady" {
		t.Errorf("nil = %q; want NotReady", got)
	}
	if got := readyStatus(ptrBoolLit(false)); got != "NotReady" {
		t.Errorf("false = %q; want NotReady", got)
	}
	if got := readyStatus(ptrBoolLit(true)); got != "Ready" {
		t.Errorf("true = %q; want Ready", got)
	}
}

// pagingFakeStore tests the collectAll generic by having ListNodes return
// two pages then stop. We embed the regular fakeStore to inherit the
// other methods.
type pagingFakeStore struct {
	*fakeStore
	pageCalls int
}

func (p *pagingFakeStore) ListNodes(_ context.Context, clusterID *uuid.UUID, _ int, cursor string) ([]api.Node, string, error) {
	p.pageCalls++
	if cursor == "" {
		return []api.Node{{Id: ptrUUID(uuid.New()), ClusterId: *clusterID, Name: "n1"}}, "page2", nil
	}
	return []api.Node{{Id: ptrUUID(uuid.New()), ClusterId: *clusterID, Name: "n2"}}, "", nil
}

func TestCollectAll_FollowsCursors(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	inner := newFakeStore()
	inner.clusters = []api.Cluster{{Id: ptrUUID(clusterID), Name: "x"}}
	p := &pagingFakeStore{fakeStore: inner}

	tr := NewTraverser(p)
	g, err := tr.Traverse(context.Background(), TypeCluster, clusterID, 1)
	if err != nil {
		t.Fatalf("Traverse: %v", err)
	}
	if p.pageCalls != 2 {
		t.Errorf("ListNodes called %d times; want 2 (one per cursor page)", p.pageCalls)
	}
	// 1 cluster + 2 paged nodes
	if len(g.Nodes) != 3 {
		t.Errorf("got %d nodes; want 3 (cluster + 2 paged nodes)", len(g.Nodes))
	}
}
