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
	pods             []PodInfo
	listPodErr       error
	workloads        []WorkloadInfo
	listWorkloadErr  error
	services         []ServiceInfo
	listServiceErr   error
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

func (f *fakeSource) ListPods(_ context.Context) ([]PodInfo, error) {
	return f.pods, f.listPodErr
}

func (f *fakeSource) ListWorkloads(_ context.Context) ([]WorkloadInfo, error) {
	return f.workloads, f.listWorkloadErr
}

func (f *fakeSource) ListServices(_ context.Context) ([]ServiceInfo, error) {
	return f.services, f.listServiceErr
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
	upsertedPod      []api.PodCreate
	upsertedWorkload []api.WorkloadCreate
	upsertedService  []api.ServiceCreate
	// Existing rows; reconciliation operates against these.
	existingNodes     []api.Node
	existingNS        []api.Namespace
	existingPods      []api.Pod
	existingWorkloads []api.Workload
	existingServices  []api.Service
	// Preset namespace-id assignments. The fake picks an id for each
	// UpsertNamespace call from here (keyed by cluster_id + name), falling
	// back to a fresh uuid.New() if absent. Lets tests pin the name -> id
	// map that flows into ingestPods.
	nsIDPreset        map[string]uuid.UUID
	listErr           error
	updateErr         error
	upsertErr         error
	upsertNSErr       error
	upsertPodErr      error
	upsertWorkloadErr error
	upsertServiceErr  error
	// nodeState mirrors per-(cluster_id, name) upsert history so tests can
	// assert idempotent behaviour.
	nodeState     map[string]int // key: cluster_id/name, value: upsert count
	nsState       map[string]int
	podState      map[string]int // key: namespace_id/name
	workloadState map[string]int // key: namespace_id/kind/name
	serviceState  map[string]int // key: namespace_id/name
	// Reconciliation call log: each entry is the keepNames slice from a call.
	reconcileNodesCalls     []reconcileCall
	reconcileNSCalls        []reconcileCall
	reconcilePodsCalls      []reconcileCall
	reconcileWorkloadsCalls []reconcileWorkloadCall
	reconcileServicesCalls  []reconcileCall
}

type reconcileWorkloadCall struct {
	namespaceID uuid.UUID
	keepKinds   []string
	keepNames   []string
}

type reconcileCall struct {
	clusterID uuid.UUID
	keep      []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		nodeState:     make(map[string]int),
		nsState:       make(map[string]int),
		podState:      make(map[string]int),
		workloadState: make(map[string]int),
		serviceState:  make(map[string]int),
		nsIDPreset:    make(map[string]uuid.UUID),
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
	id, preset := s.nsIDPreset[key]
	if !preset {
		id = uuid.New()
	}
	return api.Namespace{
		Id:        &id,
		ClusterId: in.ClusterId,
		Name:      in.Name,
		Phase:     in.Phase,
	}, nil
}

func (s *fakeStore) UpsertPod(_ context.Context, in api.PodCreate) (api.Pod, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upsertPodErr != nil {
		return api.Pod{}, s.upsertPodErr
	}
	s.upsertedPod = append(s.upsertedPod, in)
	key := in.NamespaceId.String() + "/" + in.Name
	s.podState[key]++
	id := uuid.New()
	return api.Pod{
		Id:          &id,
		NamespaceId: in.NamespaceId,
		Name:        in.Name,
		Phase:       in.Phase,
	}, nil
}

func (s *fakeStore) DeletePodsNotIn(_ context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcilePodsCalls = append(s.reconcilePodsCalls, reconcileCall{clusterID: namespaceID, keep: append([]string(nil), keepNames...)})
	keep := make(map[string]struct{}, len(keepNames))
	for _, n := range keepNames {
		keep[n] = struct{}{}
	}
	kept := s.existingPods[:0]
	var deleted int64
	for _, p := range s.existingPods {
		if p.NamespaceId != namespaceID {
			kept = append(kept, p)
			continue
		}
		if _, ok := keep[p.Name]; ok {
			kept = append(kept, p)
			continue
		}
		deleted++
	}
	s.existingPods = kept
	return deleted, nil
}

func (s *fakeStore) UpsertService(_ context.Context, in api.ServiceCreate) (api.Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upsertServiceErr != nil {
		return api.Service{}, s.upsertServiceErr
	}
	s.upsertedService = append(s.upsertedService, in)
	key := in.NamespaceId.String() + "/" + in.Name
	s.serviceState[key]++
	id := uuid.New()
	return api.Service{
		Id:          &id,
		NamespaceId: in.NamespaceId,
		Name:        in.Name,
		Type:        in.Type,
	}, nil
}

func (s *fakeStore) DeleteServicesNotIn(_ context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileServicesCalls = append(s.reconcileServicesCalls, reconcileCall{clusterID: namespaceID, keep: append([]string(nil), keepNames...)})
	keep := make(map[string]struct{}, len(keepNames))
	for _, n := range keepNames {
		keep[n] = struct{}{}
	}
	kept := s.existingServices[:0]
	var deleted int64
	for _, svc := range s.existingServices {
		if svc.NamespaceId != namespaceID {
			kept = append(kept, svc)
			continue
		}
		if _, ok := keep[svc.Name]; ok {
			kept = append(kept, svc)
			continue
		}
		deleted++
	}
	s.existingServices = kept
	return deleted, nil
}

func (s *fakeStore) UpsertWorkload(_ context.Context, in api.WorkloadCreate) (api.Workload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upsertWorkloadErr != nil {
		return api.Workload{}, s.upsertWorkloadErr
	}
	s.upsertedWorkload = append(s.upsertedWorkload, in)
	key := in.NamespaceId.String() + "/" + string(in.Kind) + "/" + in.Name
	s.workloadState[key]++
	id := uuid.New()
	return api.Workload{
		Id:          &id,
		NamespaceId: in.NamespaceId,
		Kind:        in.Kind,
		Name:        in.Name,
	}, nil
}

func (s *fakeStore) DeleteWorkloadsNotIn(_ context.Context, namespaceID uuid.UUID, keepKinds, keepNames []string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileWorkloadsCalls = append(s.reconcileWorkloadsCalls, reconcileWorkloadCall{
		namespaceID: namespaceID,
		keepKinds:   append([]string(nil), keepKinds...),
		keepNames:   append([]string(nil), keepNames...),
	})
	keep := make(map[string]struct{}, len(keepKinds))
	for i := range keepKinds {
		keep[keepKinds[i]+"/"+keepNames[i]] = struct{}{}
	}
	kept := s.existingWorkloads[:0]
	var deleted int64
	for _, w := range s.existingWorkloads {
		if w.NamespaceId != namespaceID {
			kept = append(kept, w)
			continue
		}
		if _, ok := keep[string(w.Kind)+"/"+w.Name]; ok {
			kept = append(kept, w)
			continue
		}
		deleted++
	}
	s.existingWorkloads = kept
	return deleted, nil
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

func TestPollIngestsPodsWithNamespaceResolution(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	defaultNSID := uuid.New()
	kubeSystemNSID := uuid.New()

	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &clusterID, Name: "prod"}}
	store.nsIDPreset[clusterID.String()+"/default"] = defaultNSID
	store.nsIDPreset[clusterID.String()+"/kube-system"] = kubeSystemNSID

	source := &fakeSource{
		version: "v1.29.5",
		namespaces: []NamespaceInfo{
			{Name: "default"},
			{Name: "kube-system"},
		},
		pods: []PodInfo{
			{Name: "app-1", Namespace: "default", Phase: "Running"},
			{Name: "coredns-abc", Namespace: "kube-system", Phase: "Running"},
			{Name: "orphan", Namespace: "deleted-ns"}, // no matching namespace -> skipped
		},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()

	if len(store.upsertedPod) != 2 {
		t.Fatalf("upserted pods=%d, want 2 (orphan must be skipped)", len(store.upsertedPod))
	}
	// Each upsert must carry the resolved namespace id, not the K8s name.
	seen := map[uuid.UUID]string{}
	for _, up := range store.upsertedPod {
		seen[up.NamespaceId] = up.Name
	}
	if seen[defaultNSID] != "app-1" {
		t.Errorf("default namespace pod=%q, want app-1", seen[defaultNSID])
	}
	if seen[kubeSystemNSID] != "coredns-abc" {
		t.Errorf("kube-system pod=%q, want coredns-abc", seen[kubeSystemNSID])
	}
}

func TestPollPodsReconcilePerNamespace(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	nsID := uuid.New()

	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &clusterID, Name: "prod"}}
	store.nsIDPreset[clusterID.String()+"/default"] = nsID

	// Pre-seed two stored pods; only one is still in the live listing.
	podA := uuid.New()
	podStale := uuid.New()
	store.existingPods = []api.Pod{
		{Id: &podA, NamespaceId: nsID, Name: "a"},
		{Id: &podStale, NamespaceId: nsID, Name: "stale"},
	}

	source := &fakeSource{
		version:    "v1.29.5",
		namespaces: []NamespaceInfo{{Name: "default"}},
		pods:       []PodInfo{{Name: "a", Namespace: "default"}},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.reconcilePodsCalls) == 0 {
		t.Fatal("expected at least one pod reconcile call")
	}
	// Stale pod must be gone; live one kept.
	if len(store.existingPods) != 1 || store.existingPods[0].Name != "a" {
		t.Errorf("existingPods=%v, want only 'a'", store.existingPods)
	}
}

func TestPollPodsReconcileEmptyNamespace(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	emptyNSID := uuid.New()

	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &clusterID, Name: "prod"}}
	store.nsIDPreset[clusterID.String()+"/default"] = emptyNSID
	leftover := uuid.New()
	store.existingPods = []api.Pod{{Id: &leftover, NamespaceId: emptyNSID, Name: "leftover"}}

	// Live namespace exists but reports zero pods — leftover must still be reconciled away.
	source := &fakeSource{
		version:    "v1.29.5",
		namespaces: []NamespaceInfo{{Name: "default"}},
		pods:       []PodInfo{},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.existingPods) != 0 {
		t.Errorf("existingPods=%v, want empty for a namespace with no live pods", store.existingPods)
	}
}

func TestPollSkipsPodIngestionOnListError(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &clusterID, Name: "prod"}}
	source := &fakeSource{
		version:    "v1.29.5",
		namespaces: []NamespaceInfo{{Name: "default"}},
		listPodErr: errors.New("kube down"),
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	if len(store.upsertedPod) != 0 {
		t.Errorf("expected no pod upserts on ListPods error; got %d", len(store.upsertedPod))
	}
	if len(store.reconcilePodsCalls) != 0 {
		t.Errorf("expected no pod reconcile on ListPods error; got %d", len(store.reconcilePodsCalls))
	}
}

func TestPollSkipsPodIngestionWhenNamespaceListFails(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &clusterID, Name: "prod"}}
	source := &fakeSource{
		version:          "v1.29.5",
		listNamespaceErr: errors.New("ns down"),
		pods:             []PodInfo{{Name: "x", Namespace: "default"}},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	if len(store.upsertedPod) != 0 {
		t.Errorf("expected no pod upserts when ListNamespaces fails; got %d", len(store.upsertedPod))
	}
}

func TestPollIngestsWorkloadsWithNamespaceResolution(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	defaultNSID := uuid.New()
	kubeSystemNSID := uuid.New()

	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &clusterID, Name: "prod"}}
	store.nsIDPreset[clusterID.String()+"/default"] = defaultNSID
	store.nsIDPreset[clusterID.String()+"/kube-system"] = kubeSystemNSID

	replicas := 3
	source := &fakeSource{
		version: "v1.29.5",
		namespaces: []NamespaceInfo{
			{Name: "default"},
			{Name: "kube-system"},
		},
		workloads: []WorkloadInfo{
			{Name: "web", Namespace: "default", Kind: api.Deployment, Replicas: &replicas},
			{Name: "coredns", Namespace: "kube-system", Kind: api.Deployment, Replicas: &replicas},
			{Name: "fluent-bit", Namespace: "kube-system", Kind: api.DaemonSet},
			{Name: "orphan", Namespace: "deleted-ns", Kind: api.Deployment}, // skipped
		},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.upsertedWorkload) != 3 {
		t.Fatalf("upserted workloads=%d, want 3 (orphan must be skipped)", len(store.upsertedWorkload))
	}
	seen := map[string]api.WorkloadKind{}
	for _, up := range store.upsertedWorkload {
		seen[up.NamespaceId.String()+"/"+up.Name] = up.Kind
	}
	if seen[defaultNSID.String()+"/web"] != api.Deployment {
		t.Errorf("web in default ns not a Deployment: %v", seen)
	}
	if seen[kubeSystemNSID.String()+"/fluent-bit"] != api.DaemonSet {
		t.Errorf("fluent-bit not a DaemonSet: %v", seen)
	}
}

func TestPollWorkloadsReconcileByKindName(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	nsID := uuid.New()
	depID := uuid.New()
	stsID := uuid.New()

	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &clusterID, Name: "prod"}}
	store.nsIDPreset[clusterID.String()+"/default"] = nsID
	// Pre-seed a Deployment and a StatefulSet both named 'web'; only the
	// Deployment is still live. The StatefulSet must be reconciled away
	// while the Deployment survives — that's the (kind, name) tuple check.
	store.existingWorkloads = []api.Workload{
		{Id: &depID, NamespaceId: nsID, Kind: api.Deployment, Name: "web"},
		{Id: &stsID, NamespaceId: nsID, Kind: api.StatefulSet, Name: "web"},
	}

	source := &fakeSource{
		version:    "v1.29.5",
		namespaces: []NamespaceInfo{{Name: "default"}},
		workloads:  []WorkloadInfo{{Name: "web", Namespace: "default", Kind: api.Deployment}},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.reconcileWorkloadsCalls) == 0 {
		t.Fatal("expected at least one workload reconcile call")
	}
	if len(store.existingWorkloads) != 1 || store.existingWorkloads[0].Kind != api.Deployment {
		t.Errorf("expected only (Deployment, web) to survive, got %+v", store.existingWorkloads)
	}
	// Verify the collector actually passed (kind, name) parallel slices.
	call := store.reconcileWorkloadsCalls[0]
	if len(call.keepKinds) != len(call.keepNames) {
		t.Errorf("keep arrays length mismatch: kinds=%d names=%d", len(call.keepKinds), len(call.keepNames))
	}
}

func TestPollSkipsWorkloadIngestionOnListError(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &clusterID, Name: "prod"}}
	source := &fakeSource{
		version:         "v1.29.5",
		namespaces:      []NamespaceInfo{{Name: "default"}},
		listWorkloadErr: errors.New("kube down"),
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	if len(store.upsertedWorkload) != 0 {
		t.Errorf("expected no workload upserts on ListWorkloads error; got %d", len(store.upsertedWorkload))
	}
	if len(store.reconcileWorkloadsCalls) != 0 {
		t.Errorf("expected no workload reconcile on ListWorkloads error; got %d", len(store.reconcileWorkloadsCalls))
	}
}

func TestPollIngestsServicesWithNamespaceResolution(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	defaultNSID := uuid.New()

	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &clusterID, Name: "prod"}}
	store.nsIDPreset[clusterID.String()+"/default"] = defaultNSID

	source := &fakeSource{
		version:    "v1.29.5",
		namespaces: []NamespaceInfo{{Name: "default"}},
		services: []ServiceInfo{
			{Name: "web", Namespace: "default", Type: "ClusterIP", ClusterIP: "10.0.0.1"},
			{Name: "orphan", Namespace: "deleted-ns", Type: "ClusterIP"}, // skipped
		},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.upsertedService) != 1 {
		t.Fatalf("upserted services=%d, want 1 (orphan must be skipped)", len(store.upsertedService))
	}
	up := store.upsertedService[0]
	if up.NamespaceId != defaultNSID {
		t.Errorf("upsert namespace_id=%v, want %v", up.NamespaceId, defaultNSID)
	}
	if up.Type == nil || *up.Type != api.ClusterIP {
		t.Errorf("type=%v, want ClusterIP", up.Type)
	}
}

func TestPollServicesReconcilePerNamespace(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	nsID := uuid.New()
	live := uuid.New()
	stale := uuid.New()

	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &clusterID, Name: "prod"}}
	store.nsIDPreset[clusterID.String()+"/default"] = nsID
	store.existingServices = []api.Service{
		{Id: &live, NamespaceId: nsID, Name: "web"},
		{Id: &stale, NamespaceId: nsID, Name: "stale"},
	}

	source := &fakeSource{
		version:    "v1.29.5",
		namespaces: []NamespaceInfo{{Name: "default"}},
		services:   []ServiceInfo{{Name: "web", Namespace: "default"}},
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.existingServices) != 1 || store.existingServices[0].Name != "web" {
		t.Errorf("existingServices=%v, want only 'web'", store.existingServices)
	}
}

func TestPollSkipsServiceIngestionOnListError(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	store := newFakeStore()
	store.clusters = []api.Cluster{{Id: &clusterID, Name: "prod"}}
	source := &fakeSource{
		version:        "v1.29.5",
		namespaces:     []NamespaceInfo{{Name: "default"}},
		listServiceErr: errors.New("kube down"),
	}
	c := New(store, source, "prod", time.Minute, time.Second, true)

	c.poll(context.Background())

	if len(store.upsertedService) != 0 {
		t.Errorf("expected no service upserts on ListServices error; got %d", len(store.upsertedService))
	}
	if len(store.reconcileServicesCalls) != 0 {
		t.Errorf("expected no service reconcile on ListServices error; got %d", len(store.reconcileServicesCalls))
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
