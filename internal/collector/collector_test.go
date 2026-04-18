package collector

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/api"
)

// fakeSource implements KubeSource with fixed results.
type fakeSource struct {
	version          string
	versionErr       error
	nodes            []NodeInfo
	listNodeErr      error
	namespaces       []NamespaceInfo
	listNamespaceErr error
}

func (f *fakeSource) ServerVersion(_ context.Context) (string, error) {
	return f.version, f.versionErr
}

func (f *fakeSource) ListNodes(_ context.Context) ([]NodeInfo, error) {
	return f.nodes, f.listNodeErr
}

func (f *fakeSource) ListNamespaces(_ context.Context) ([]NamespaceInfo, error) {
	return f.namespaces, f.listNamespaceErr
}

type recordedUpdate struct {
	id    uuid.UUID
	patch api.ClusterUpdate
}

type fakeStore struct {
	mu               sync.Mutex
	clusters         []api.Cluster
	updates          []recordedUpdate
	upsertedNode     []api.NodeCreate
	upsertedNS       []api.NamespaceCreate
	// Existing rows; reconciliation operates against these.
	existingNodes []api.Node
	existingNS    []api.Namespace
	listErr       error
	updateErr     error
	upsertErr     error
	upsertNSErr   error
	// nodeState mirrors per-(cluster_id, name) upsert history so tests can
	// assert idempotent behaviour.
	nodeState map[string]int // key: cluster_id/name, value: upsert count
	nsState   map[string]int
	// Reconciliation call log: each entry is the keepNames slice from a call.
	reconcileNodesCalls []reconcileCall
	reconcileNSCalls    []reconcileCall
}

type reconcileCall struct {
	clusterID uuid.UUID
	keep      []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		nodeState: make(map[string]int),
		nsState:   make(map[string]int),
	}
}

func (s *fakeStore) GetClusterByName(_ context.Context, name string) (api.Cluster, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return api.Cluster{}, s.listErr
	}
	for _, c := range s.clusters {
		if c.Name == name {
			return c, nil
		}
	}
	return api.Cluster{}, api.ErrNotFound
}

func (s *fakeStore) UpdateCluster(_ context.Context, id uuid.UUID, in api.ClusterUpdate) (api.Cluster, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updateErr != nil {
		return api.Cluster{}, s.updateErr
	}
	s.updates = append(s.updates, recordedUpdate{id: id, patch: in})
	for i, c := range s.clusters {
		if c.Id != nil && *c.Id == id {
			if in.KubernetesVersion != nil {
				s.clusters[i].KubernetesVersion = in.KubernetesVersion
			}
			return s.clusters[i], nil
		}
	}
	return api.Cluster{}, api.ErrNotFound
}

func (s *fakeStore) UpsertNode(_ context.Context, in api.NodeCreate) (api.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upsertErr != nil {
		return api.Node{}, s.upsertErr
	}
	s.upsertedNode = append(s.upsertedNode, in)
	key := in.ClusterId.String() + "/" + in.Name
	s.nodeState[key]++
	id := uuid.New()
	return api.Node{
		Id:             &id,
		ClusterId:      in.ClusterId,
		Name:           in.Name,
		KubeletVersion: in.KubeletVersion,
	}, nil
}

func (s *fakeStore) DeleteNodesNotIn(_ context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileNodesCalls = append(s.reconcileNodesCalls, reconcileCall{clusterID: clusterID, keep: append([]string(nil), keepNames...)})
	keep := make(map[string]struct{}, len(keepNames))
	for _, n := range keepNames {
		keep[n] = struct{}{}
	}
	kept := s.existingNodes[:0]
	var deleted int64
	for _, n := range s.existingNodes {
		if n.ClusterId != clusterID {
			kept = append(kept, n)
			continue
		}
		if _, ok := keep[n.Name]; ok {
			kept = append(kept, n)
			continue
		}
		deleted++
	}
	s.existingNodes = kept
	return deleted, nil
}

func (s *fakeStore) DeleteNamespacesNotIn(_ context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileNSCalls = append(s.reconcileNSCalls, reconcileCall{clusterID: clusterID, keep: append([]string(nil), keepNames...)})
	keep := make(map[string]struct{}, len(keepNames))
	for _, n := range keepNames {
		keep[n] = struct{}{}
	}
	kept := s.existingNS[:0]
	var deleted int64
	for _, n := range s.existingNS {
		if n.ClusterId != clusterID {
			kept = append(kept, n)
			continue
		}
		if _, ok := keep[n.Name]; ok {
			kept = append(kept, n)
			continue
		}
		deleted++
	}
	s.existingNS = kept
	return deleted, nil
}

func (s *fakeStore) UpsertNamespace(_ context.Context, in api.NamespaceCreate) (api.Namespace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upsertNSErr != nil {
		return api.Namespace{}, s.upsertNSErr
	}
	s.upsertedNS = append(s.upsertedNS, in)
	key := in.ClusterId.String() + "/" + in.Name
	s.nsState[key]++
	id := uuid.New()
	return api.Namespace{
		Id:        &id,
		ClusterId: in.ClusterId,
		Name:      in.Name,
		Phase:     in.Phase,
	}, nil
}

func TestPollUpdatesVersionWhenChanged(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	old := "v1.28.0"
	store := newFakeStore()
	store.clusters = []api.Cluster{
		{Id: &id, Name: "prod", KubernetesVersion: &old},
	}
	c := New(store, &fakeSource{version: "v1.29.5"}, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.updates) != 1 {
		t.Fatalf("updates=%d, want 1", len(store.updates))
	}
	up := store.updates[0]
	if up.id != id {
		t.Errorf("id=%v, want %v", up.id, id)
	}
	if up.patch.KubernetesVersion == nil || *up.patch.KubernetesVersion != "v1.29.5" {
		t.Errorf("version patch=%v", up.patch.KubernetesVersion)
	}
}

func TestPollSkipsWhenVersionUnchanged(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	current := "v1.29.5"
	store := newFakeStore()
	store.clusters = []api.Cluster{
		{Id: &id, Name: "prod", KubernetesVersion: &current},
	}
	c := New(store, &fakeSource{version: current}, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	if len(store.updates) != 0 {
		t.Errorf("expected no updates when version unchanged, got %d", len(store.updates))
	}
}

func TestPollSkipsOnVersionError(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &id, Name: "prod"}}
	c := New(store, &fakeSource{versionErr: errors.New("boom")}, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	if len(store.updates) != 0 || len(store.upsertedNode) != 0 {
		t.Errorf("expected no store writes on version error; updates=%d upserts=%d", len(store.updates), len(store.upsertedNode))
	}
}

func TestPollSkipsOnGetClusterByNameError(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.listErr = errors.New("db down")
	c := New(store, &fakeSource{version: "v1.29.5"}, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	if len(store.updates) != 0 || len(store.upsertedNode) != 0 {
		t.Errorf("expected no store writes on lookup error")
	}
}

func TestPollSkipsWhenClusterNotRegistered(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	c := New(store, &fakeSource{version: "v1.29.5"}, "missing", time.Minute, time.Second, true)

	c.poll(context.Background())

	if len(store.updates) != 0 || len(store.upsertedNode) != 0 {
		t.Errorf("expected no store writes when cluster missing")
	}
}

func TestPollIngestsNodes(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &id, Name: "prod"}}

	source := &fakeSource{
		version: "v1.29.5",
		nodes: []NodeInfo{
			{Name: "node-a", KubeletVersion: "v1.29.5", OsImage: "Ubuntu 22.04", Architecture: "amd64"},
			{Name: "node-b", KubeletVersion: "v1.29.5", Labels: map[string]string{"role": "worker"}},
		},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.upsertedNode) != 2 {
		t.Fatalf("upserted=%d, want 2", len(store.upsertedNode))
	}
	// Every upsert must carry the correct cluster id.
	for _, up := range store.upsertedNode {
		if up.ClusterId != id {
			t.Errorf("upsert cluster_id=%v, want %v", up.ClusterId, id)
		}
	}
	// Empty label map must NOT be written as a non-nil pointer.
	if store.upsertedNode[0].Labels != nil {
		t.Errorf("node-a labels = %v, want nil", store.upsertedNode[0].Labels)
	}
	// Non-empty label map must round-trip through a pointer.
	if store.upsertedNode[1].Labels == nil || (*store.upsertedNode[1].Labels)["role"] != "worker" {
		t.Errorf("node-b labels = %v, want {role:worker}", store.upsertedNode[1].Labels)
	}
}

func TestPollIngestsNodesIsIdempotent(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &id, Name: "prod"}}

	source := &fakeSource{
		version: "v1.29.5",
		nodes:   []NodeInfo{{Name: "node-a"}},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())
	c.poll(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if got := store.nodeState[id.String()+"/node-a"]; got != 2 {
		t.Errorf("expected node-a upsert count 2 (one per poll), got %d", got)
	}
}

func TestPollContinuesOnPerNodeUpsertError(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &id, Name: "prod"}}
	store.upsertErr = errors.New("boom")

	source := &fakeSource{
		version: "v1.29.5",
		nodes: []NodeInfo{
			{Name: "node-a"}, {Name: "node-b"}, {Name: "node-c"},
		},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	// poll must not panic or return early on upsert error.
	c.poll(context.Background())

	// The version update already happened before UpsertNode errors.
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.updates) != 1 {
		t.Errorf("expected cluster version update despite node upsert failures, got %d updates", len(store.updates))
	}
}

func TestPollSkipsNodeIngestionOnListNodesError(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &id, Name: "prod"}}

	source := &fakeSource{
		version:     "v1.29.5",
		listNodeErr: errors.New("kube down"),
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	if len(store.upsertedNode) != 0 {
		t.Errorf("expected no node upserts when ListNodes errors; got %d", len(store.upsertedNode))
	}
}

func TestPollIngestsNamespaces(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &id, Name: "prod"}}

	source := &fakeSource{
		version: "v1.29.5",
		namespaces: []NamespaceInfo{
			{Name: "default", Phase: "Active"},
			{Name: "kube-system", Phase: "Active", Labels: map[string]string{"role": "system"}},
		},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.upsertedNS) != 2 {
		t.Fatalf("upserted=%d, want 2", len(store.upsertedNS))
	}
	if store.upsertedNS[1].Labels == nil || (*store.upsertedNS[1].Labels)["role"] != "system" {
		t.Errorf("kube-system labels not round-tripped: %v", store.upsertedNS[1].Labels)
	}
}

func TestPollReconcilesNodesAndNamespaces(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	nA := uuid.New()
	nB := uuid.New()
	nsA := uuid.New()
	nsB := uuid.New()

	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &id, Name: "prod"}}
	store.existingNodes = []api.Node{
		{Id: &nA, ClusterId: id, Name: "node-a"},
		{Id: &nB, ClusterId: id, Name: "node-b"},
	}
	store.existingNS = []api.Namespace{
		{Id: &nsA, ClusterId: id, Name: "ns-a"},
		{Id: &nsB, ClusterId: id, Name: "ns-b"},
	}

	// Kubernetes now reports only the -a variants; -b rows must be reconciled away.
	source := &fakeSource{
		version:    "v1.29.5",
		nodes:      []NodeInfo{{Name: "node-a"}},
		namespaces: []NamespaceInfo{{Name: "ns-a"}},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()

	if len(store.reconcileNodesCalls) != 1 {
		t.Fatalf("node reconcile calls=%d, want 1", len(store.reconcileNodesCalls))
	}
	if got := store.reconcileNodesCalls[0].keep; len(got) != 1 || got[0] != "node-a" {
		t.Errorf("node keep=%v, want [node-a]", got)
	}
	if len(store.existingNodes) != 1 || store.existingNodes[0].Name != "node-a" {
		t.Errorf("existingNodes=%v, want only node-a", store.existingNodes)
	}

	if len(store.reconcileNSCalls) != 1 {
		t.Fatalf("namespace reconcile calls=%d, want 1", len(store.reconcileNSCalls))
	}
	if got := store.reconcileNSCalls[0].keep; len(got) != 1 || got[0] != "ns-a" {
		t.Errorf("namespace keep=%v, want [ns-a]", got)
	}
	if len(store.existingNS) != 1 || store.existingNS[0].Name != "ns-a" {
		t.Errorf("existingNS=%v, want only ns-a", store.existingNS)
	}
}

func TestPollSkipsReconcileWhenDisabled(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &id, Name: "prod"}}
	source := &fakeSource{
		version:    "v1.29.5",
		nodes:      []NodeInfo{{Name: "node-a"}},
		namespaces: []NamespaceInfo{{Name: "ns-a"}},
	}
	c := New(store, source, "prod", time.Minute, time.Second, false)

	c.poll(context.Background())

	if len(store.reconcileNodesCalls) != 0 {
		t.Errorf("node reconcile called with reconcile=false: %d calls", len(store.reconcileNodesCalls))
	}
	if len(store.reconcileNSCalls) != 0 {
		t.Errorf("namespace reconcile called with reconcile=false: %d calls", len(store.reconcileNSCalls))
	}
}

func TestPollDoesNotReconcileOnListError(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &id, Name: "prod"}}
	// Both list calls fail; reconciliation MUST NOT run (otherwise a transient
	// Kubernetes error would wipe the CMDB).
	source := &fakeSource{
		version:          "v1.29.5",
		listNodeErr:      errors.New("nodes down"),
		listNamespaceErr: errors.New("ns down"),
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	if len(store.reconcileNodesCalls) != 0 {
		t.Errorf("node reconcile must not run on ListNodes error; got %d calls", len(store.reconcileNodesCalls))
	}
	if len(store.reconcileNSCalls) != 0 {
		t.Errorf("namespace reconcile must not run on ListNamespaces error; got %d calls", len(store.reconcileNSCalls))
	}
}

func TestPollSkipsNamespaceIngestionOnListError(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &id, Name: "prod"}}

	source := &fakeSource{
		version:          "v1.29.5",
		listNamespaceErr: errors.New("kube down"),
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	if len(store.upsertedNS) != 0 {
		t.Errorf("expected no namespace upserts on list error; got %d", len(store.upsertedNS))
	}
}

type signalingSource struct {
	fakeSource
	calls atomic.Int64
	ch    chan struct{}
}

func (s *signalingSource) ServerVersion(ctx context.Context) (string, error) {
	s.calls.Add(1)
	select {
	case s.ch <- struct{}{}:
	default:
	}
	return s.fakeSource.ServerVersion(ctx)
}

func TestRunStopsOnContextCancel(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &id, Name: "prod"}}

	source := &signalingSource{
		fakeSource: fakeSource{version: "v1.29.5"},
		ch:         make(chan struct{}, 4),
	}
	c := New(store, source, "prod", 20*time.Millisecond, time.Second, true)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	waitForSignal(t, source.ch, 2*time.Second)
	waitForSignal(t, source.ch, 2*time.Second)

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if got := source.calls.Load(); got < 2 {
		t.Errorf("expected at least 2 fetches, got %d", got)
	}
}

func waitForSignal(t *testing.T, ch <-chan struct{}, timeout time.Duration) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for collector tick after %v", timeout)
	}
}
