package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// memStore is an in-memory api.Store implementation used to exercise the
// HTTP handlers without a PostgreSQL dependency.
type memStore struct {
	mu            sync.Mutex
	byID          map[uuid.UUID]Cluster
	byName        map[string]uuid.UUID
	nodesByID     map[uuid.UUID]Node
	nodesByNatKey map[string]uuid.UUID // "<cluster_id>/<name>" -> node id
	nsByID        map[uuid.UUID]Namespace
	nsByNatKey    map[string]uuid.UUID // "<cluster_id>/<name>" -> namespace id
	pingErr       error
	createdN      int
}

func newMemStore() *memStore {
	return &memStore{
		byID:          make(map[uuid.UUID]Cluster),
		byName:        make(map[string]uuid.UUID),
		nodesByID:     make(map[uuid.UUID]Node),
		nodesByNatKey: make(map[string]uuid.UUID),
		nsByID:        make(map[uuid.UUID]Namespace),
		nsByNatKey:    make(map[string]uuid.UUID),
	}
}

func nodeNatKey(clusterID uuid.UUID, name string) string {
	return clusterID.String() + "/" + name
}

func nsNatKey(clusterID uuid.UUID, name string) string {
	return clusterID.String() + "/" + name
}

func (m *memStore) Ping(_ context.Context) error { return m.pingErr }

func (m *memStore) CreateCluster(_ context.Context, in ClusterCreate) (Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.byName[in.Name]; exists {
		return Cluster{}, fmt.Errorf("duplicate: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	c := Cluster{
		Id:                &id,
		Name:              in.Name,
		DisplayName:       in.DisplayName,
		Environment:       in.Environment,
		Provider:          in.Provider,
		Region:            in.Region,
		KubernetesVersion: in.KubernetesVersion,
		ApiEndpoint:       in.ApiEndpoint,
		Labels:            in.Labels,
		CreatedAt:         &now,
		UpdatedAt:         &now,
	}
	m.byID[id] = c
	m.byName[in.Name] = id
	return c, nil
}

func (m *memStore) GetCluster(_ context.Context, id uuid.UUID) (Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.byID[id]
	if !ok {
		return Cluster{}, ErrNotFound
	}
	return c, nil
}

func (m *memStore) GetClusterByName(_ context.Context, name string) (Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byName[name]
	if !ok {
		return Cluster{}, ErrNotFound
	}
	return m.byID[id], nil
}

func (m *memStore) ListClusters(_ context.Context, limit int, _ string) ([]Cluster, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]Cluster, 0, len(m.byID))
	for _, c := range m.byID {
		out = append(out, c)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) UpdateCluster(_ context.Context, id uuid.UUID, in ClusterUpdate) (Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.byID[id]
	if !ok {
		return Cluster{}, ErrNotFound
	}
	if in.DisplayName != nil {
		c.DisplayName = in.DisplayName
	}
	if in.Environment != nil {
		c.Environment = in.Environment
	}
	if in.Provider != nil {
		c.Provider = in.Provider
	}
	if in.Region != nil {
		c.Region = in.Region
	}
	if in.KubernetesVersion != nil {
		c.KubernetesVersion = in.KubernetesVersion
	}
	if in.ApiEndpoint != nil {
		c.ApiEndpoint = in.ApiEndpoint
	}
	if in.Labels != nil {
		c.Labels = in.Labels
	}
	now := time.Now().UTC()
	c.UpdatedAt = &now
	m.byID[id] = c
	return c, nil
}

func (m *memStore) DeleteCluster(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.byID[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.byID, id)
	delete(m.byName, c.Name)
	return nil
}

func (m *memStore) CreateNode(_ context.Context, in NodeCreate) (Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[in.ClusterId]; !ok {
		return Node{}, ErrNotFound
	}
	key := nodeNatKey(in.ClusterId, in.Name)
	if _, dup := m.nodesByNatKey[key]; dup {
		return Node{}, fmt.Errorf("duplicate node: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	n := Node{
		Id:             &id,
		ClusterId:      in.ClusterId,
		Name:           in.Name,
		DisplayName:    in.DisplayName,
		KubeletVersion: in.KubeletVersion,
		OsImage:        in.OsImage,
		Architecture:   in.Architecture,
		Labels:         in.Labels,
		CreatedAt:      &now,
		UpdatedAt:      &now,
	}
	m.nodesByID[id] = n
	m.nodesByNatKey[key] = id
	return n, nil
}

func (m *memStore) GetNode(_ context.Context, id uuid.UUID) (Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodesByID[id]
	if !ok {
		return Node{}, ErrNotFound
	}
	return n, nil
}

func (m *memStore) ListNodes(_ context.Context, clusterID *uuid.UUID, limit int, _ string) ([]Node, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]Node, 0, len(m.nodesByID))
	for _, n := range m.nodesByID {
		if clusterID != nil && n.ClusterId != *clusterID {
			continue
		}
		out = append(out, n)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) UpdateNode(_ context.Context, id uuid.UUID, in NodeUpdate) (Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodesByID[id]
	if !ok {
		return Node{}, ErrNotFound
	}
	if in.DisplayName != nil {
		n.DisplayName = in.DisplayName
	}
	if in.KubeletVersion != nil {
		n.KubeletVersion = in.KubeletVersion
	}
	if in.OsImage != nil {
		n.OsImage = in.OsImage
	}
	if in.Architecture != nil {
		n.Architecture = in.Architecture
	}
	if in.Labels != nil {
		n.Labels = in.Labels
	}
	now := time.Now().UTC()
	n.UpdatedAt = &now
	m.nodesByID[id] = n
	return n, nil
}

func (m *memStore) DeleteNode(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodesByID[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.nodesByID, id)
	delete(m.nodesByNatKey, nodeNatKey(n.ClusterId, n.Name))
	return nil
}

func (m *memStore) CreateNamespace(_ context.Context, in NamespaceCreate) (Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[in.ClusterId]; !ok {
		return Namespace{}, ErrNotFound
	}
	key := nsNatKey(in.ClusterId, in.Name)
	if _, dup := m.nsByNatKey[key]; dup {
		return Namespace{}, fmt.Errorf("duplicate namespace: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	n := Namespace{
		Id:          &id,
		ClusterId:   in.ClusterId,
		Name:        in.Name,
		DisplayName: in.DisplayName,
		Phase:       in.Phase,
		Labels:      in.Labels,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	m.nsByID[id] = n
	m.nsByNatKey[key] = id
	return n, nil
}

func (m *memStore) GetNamespace(_ context.Context, id uuid.UUID) (Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nsByID[id]
	if !ok {
		return Namespace{}, ErrNotFound
	}
	return n, nil
}

func (m *memStore) ListNamespaces(_ context.Context, clusterID *uuid.UUID, limit int, _ string) ([]Namespace, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]Namespace, 0, len(m.nsByID))
	for _, n := range m.nsByID {
		if clusterID != nil && n.ClusterId != *clusterID {
			continue
		}
		out = append(out, n)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) UpdateNamespace(_ context.Context, id uuid.UUID, in NamespaceUpdate) (Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nsByID[id]
	if !ok {
		return Namespace{}, ErrNotFound
	}
	if in.DisplayName != nil {
		n.DisplayName = in.DisplayName
	}
	if in.Phase != nil {
		n.Phase = in.Phase
	}
	if in.Labels != nil {
		n.Labels = in.Labels
	}
	now := time.Now().UTC()
	n.UpdatedAt = &now
	m.nsByID[id] = n
	return n, nil
}

func (m *memStore) DeleteNamespace(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nsByID[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.nsByID, id)
	delete(m.nsByNatKey, nsNatKey(n.ClusterId, n.Name))
	return nil
}

func (m *memStore) UpsertNamespace(_ context.Context, in NamespaceCreate) (Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[in.ClusterId]; !ok {
		return Namespace{}, ErrNotFound
	}
	key := nsNatKey(in.ClusterId, in.Name)
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++

	if existingID, exists := m.nsByNatKey[key]; exists {
		n := m.nsByID[existingID]
		n.DisplayName = in.DisplayName
		n.Phase = in.Phase
		n.Labels = in.Labels
		n.UpdatedAt = &now
		m.nsByID[existingID] = n
		return n, nil
	}

	id := uuid.New()
	n := Namespace{
		Id:          &id,
		ClusterId:   in.ClusterId,
		Name:        in.Name,
		DisplayName: in.DisplayName,
		Phase:       in.Phase,
		Labels:      in.Labels,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	m.nsByID[id] = n
	m.nsByNatKey[key] = id
	return n, nil
}

func (m *memStore) DeleteNodesNotIn(_ context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[string]struct{}, len(keepNames))
	for _, name := range keepNames {
		keep[name] = struct{}{}
	}
	var deleted int64
	for id, n := range m.nodesByID {
		if n.ClusterId != clusterID {
			continue
		}
		if _, ok := keep[n.Name]; ok {
			continue
		}
		delete(m.nodesByID, id)
		delete(m.nodesByNatKey, nodeNatKey(n.ClusterId, n.Name))
		deleted++
	}
	return deleted, nil
}

func (m *memStore) DeleteNamespacesNotIn(_ context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[string]struct{}, len(keepNames))
	for _, name := range keepNames {
		keep[name] = struct{}{}
	}
	var deleted int64
	for id, n := range m.nsByID {
		if n.ClusterId != clusterID {
			continue
		}
		if _, ok := keep[n.Name]; ok {
			continue
		}
		delete(m.nsByID, id)
		delete(m.nsByNatKey, nsNatKey(n.ClusterId, n.Name))
		deleted++
	}
	return deleted, nil
}

func (m *memStore) UpsertNode(_ context.Context, in NodeCreate) (Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[in.ClusterId]; !ok {
		return Node{}, ErrNotFound
	}
	key := nodeNatKey(in.ClusterId, in.Name)
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++

	if existingID, exists := m.nodesByNatKey[key]; exists {
		n := m.nodesByID[existingID]
		n.DisplayName = in.DisplayName
		n.KubeletVersion = in.KubeletVersion
		n.OsImage = in.OsImage
		n.Architecture = in.Architecture
		n.Labels = in.Labels
		n.UpdatedAt = &now
		m.nodesByID[existingID] = n
		return n, nil
	}

	id := uuid.New()
	n := Node{
		Id:             &id,
		ClusterId:      in.ClusterId,
		Name:           in.Name,
		DisplayName:    in.DisplayName,
		KubeletVersion: in.KubeletVersion,
		OsImage:        in.OsImage,
		Architecture:   in.Architecture,
		Labels:         in.Labels,
		CreatedAt:      &now,
		UpdatedAt:      &now,
	}
	m.nodesByID[id] = n
	m.nodesByNatKey[key] = id
	return n, nil
}

func newTestHandler(t *testing.T, store Store) http.Handler {
	t.Helper()
	return Handler(NewServer("test", store))
}

func TestHealthAndReadiness(t *testing.T) {
	t.Parallel()

	t.Run("healthz ok", func(t *testing.T) {
		t.Parallel()
		h := newTestHandler(t, newMemStore())
		rr := do(h, http.MethodGet, "/healthz", "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		var got Health
		if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Status != Ok {
			t.Errorf("status = %q", got.Status)
		}
	})

	t.Run("readyz ok when store pings", func(t *testing.T) {
		t.Parallel()
		h := newTestHandler(t, newMemStore())
		rr := do(h, http.MethodGet, "/readyz", "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
		}
	})

	t.Run("readyz 503 when store ping fails", func(t *testing.T) {
		t.Parallel()
		m := newMemStore()
		m.pingErr = errors.New("db down")
		h := newTestHandler(t, m)
		rr := do(h, http.MethodGet, "/readyz", "")
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("Content-Type=%q", ct)
		}
	})
}

func TestClusterCRUD(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, newMemStore())

	// Create
	create := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-eu-west-1","environment":"prod"}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%q", create.Code, create.Body.String())
	}
	if loc := create.Header().Get("Location"); !strings.HasPrefix(loc, "/v1/clusters/") {
		t.Errorf("Location=%q", loc)
	}
	var created Cluster
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Id == nil {
		t.Fatal("created.Id is nil")
	}

	// Duplicate create → 409
	dup := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-eu-west-1"}`)
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate create status=%d", dup.Code)
	}

	// Get
	getURL := "/v1/clusters/" + created.Id.String()
	get := do(h, http.MethodGet, getURL, "")
	if get.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%q", get.Code, get.Body.String())
	}

	// Get missing → 404
	miss := do(h, http.MethodGet, "/v1/clusters/"+uuid.Nil.String(), "")
	if miss.Code != http.StatusNotFound {
		t.Errorf("get missing status=%d", miss.Code)
	}

	// Patch
	patch := do(h, http.MethodPatch, getURL, `{"provider":"gke"}`)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%q", patch.Code, patch.Body.String())
	}
	var patched Cluster
	if err := json.Unmarshal(patch.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Provider == nil || *patched.Provider != "gke" {
		t.Errorf("provider=%v", patched.Provider)
	}

	// List
	list := do(h, http.MethodGet, "/v1/clusters", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	var page ClusterList
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("list len=%d", len(page.Items))
	}

	// Delete
	del := do(h, http.MethodDelete, getURL, "")
	if del.Code != http.StatusNoContent {
		t.Errorf("delete status=%d", del.Code)
	}

	// Delete again → 404
	del2 := do(h, http.MethodDelete, getURL, "")
	if del2.Code != http.StatusNotFound {
		t.Errorf("second delete status=%d", del2.Code)
	}
}

func TestNodeCRUD(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	h := newTestHandler(t, store)

	// Seed a cluster so node creates have a valid FK.
	clusterResp := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-eu-west-1"}`)
	if clusterResp.Code != http.StatusCreated {
		t.Fatalf("seed cluster: status=%d body=%q", clusterResp.Code, clusterResp.Body.String())
	}
	var cluster Cluster
	if err := json.Unmarshal(clusterResp.Body.Bytes(), &cluster); err != nil {
		t.Fatalf("decode cluster: %v", err)
	}
	clusterIDStr := cluster.Id.String()

	// Create node
	createBody := fmt.Sprintf(`{"cluster_id":%q,"name":"node-1","kubelet_version":"v1.29.5"}`, clusterIDStr)
	create := do(h, http.MethodPost, "/v1/nodes", createBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("create node: status=%d body=%q", create.Code, create.Body.String())
	}
	if loc := create.Header().Get("Location"); !strings.HasPrefix(loc, "/v1/nodes/") {
		t.Errorf("Location=%q", loc)
	}
	var node Node
	if err := json.Unmarshal(create.Body.Bytes(), &node); err != nil {
		t.Fatalf("decode node: %v", err)
	}
	if node.Id == nil {
		t.Fatal("node.Id is nil")
	}
	if node.ClusterId != *cluster.Id {
		t.Errorf("node.ClusterId=%v, want %v", node.ClusterId, *cluster.Id)
	}

	// Duplicate (cluster_id, name) → 409
	dup := do(h, http.MethodPost, "/v1/nodes", createBody)
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate node status=%d", dup.Code)
	}

	// Create with unknown cluster_id → 404
	missing := do(h, http.MethodPost, "/v1/nodes", fmt.Sprintf(`{"cluster_id":%q,"name":"x"}`, uuid.New().String()))
	if missing.Code != http.StatusNotFound {
		t.Errorf("missing cluster create status=%d", missing.Code)
	}

	// Get
	nodeURL := "/v1/nodes/" + node.Id.String()
	get := do(h, http.MethodGet, nodeURL, "")
	if get.Code != http.StatusOK {
		t.Fatalf("get node status=%d body=%q", get.Code, get.Body.String())
	}

	// Patch
	patch := do(h, http.MethodPatch, nodeURL, `{"architecture":"arm64"}`)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%q", patch.Code, patch.Body.String())
	}
	var patched Node
	if err := json.Unmarshal(patch.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Architecture == nil || *patched.Architecture != "arm64" {
		t.Errorf("architecture=%v", patched.Architecture)
	}

	// List all
	list := do(h, http.MethodGet, "/v1/nodes", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	var page NodeList
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("list len=%d", len(page.Items))
	}

	// List filtered by cluster_id
	filtered := do(h, http.MethodGet, "/v1/nodes?cluster_id="+clusterIDStr, "")
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered list status=%d", filtered.Code)
	}
	if err := json.Unmarshal(filtered.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode filtered list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("filtered list len=%d", len(page.Items))
	}

	// List filtered by a different cluster id → empty
	empty := do(h, http.MethodGet, "/v1/nodes?cluster_id="+uuid.New().String(), "")
	if empty.Code != http.StatusOK {
		t.Fatalf("empty-filter list status=%d", empty.Code)
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode empty list: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("empty-filter list len=%d", len(page.Items))
	}

	// Delete
	del := do(h, http.MethodDelete, nodeURL, "")
	if del.Code != http.StatusNoContent {
		t.Errorf("delete status=%d", del.Code)
	}

	// Delete again → 404
	del2 := do(h, http.MethodDelete, nodeURL, "")
	if del2.Code != http.StatusNotFound {
		t.Errorf("second delete status=%d", del2.Code)
	}
}

func TestNamespaceCRUD(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	h := newTestHandler(t, store)

	clusterResp := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-ns"}`)
	if clusterResp.Code != http.StatusCreated {
		t.Fatalf("seed cluster: status=%d body=%q", clusterResp.Code, clusterResp.Body.String())
	}
	var cluster Cluster
	if err := json.Unmarshal(clusterResp.Body.Bytes(), &cluster); err != nil {
		t.Fatalf("decode cluster: %v", err)
	}
	clusterIDStr := cluster.Id.String()

	createBody := fmt.Sprintf(`{"cluster_id":%q,"name":"kube-system","phase":"Active"}`, clusterIDStr)
	create := do(h, http.MethodPost, "/v1/namespaces", createBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("create ns: status=%d body=%q", create.Code, create.Body.String())
	}
	if loc := create.Header().Get("Location"); !strings.HasPrefix(loc, "/v1/namespaces/") {
		t.Errorf("Location=%q", loc)
	}
	var ns Namespace
	if err := json.Unmarshal(create.Body.Bytes(), &ns); err != nil {
		t.Fatalf("decode ns: %v", err)
	}
	if ns.Id == nil {
		t.Fatal("ns.Id nil")
	}

	dup := do(h, http.MethodPost, "/v1/namespaces", createBody)
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate namespace status=%d", dup.Code)
	}

	missing := do(h, http.MethodPost, "/v1/namespaces", fmt.Sprintf(`{"cluster_id":%q,"name":"x"}`, uuid.New().String()))
	if missing.Code != http.StatusNotFound {
		t.Errorf("missing cluster create status=%d", missing.Code)
	}

	nsURL := "/v1/namespaces/" + ns.Id.String()

	get := do(h, http.MethodGet, nsURL, "")
	if get.Code != http.StatusOK {
		t.Fatalf("get ns status=%d body=%q", get.Code, get.Body.String())
	}

	patch := do(h, http.MethodPatch, nsURL, `{"phase":"Terminating"}`)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%q", patch.Code, patch.Body.String())
	}
	var patched Namespace
	if err := json.Unmarshal(patch.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Phase == nil || *patched.Phase != "Terminating" {
		t.Errorf("phase=%v", patched.Phase)
	}

	list := do(h, http.MethodGet, "/v1/namespaces", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	var page NamespaceList
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("list len=%d", len(page.Items))
	}

	filtered := do(h, http.MethodGet, "/v1/namespaces?cluster_id="+clusterIDStr, "")
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered list status=%d", filtered.Code)
	}
	if err := json.Unmarshal(filtered.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode filtered list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("filtered list len=%d", len(page.Items))
	}

	del := do(h, http.MethodDelete, nsURL, "")
	if del.Code != http.StatusNoContent {
		t.Errorf("delete status=%d", del.Code)
	}

	del2 := do(h, http.MethodDelete, nsURL, "")
	if del2.Code != http.StatusNotFound {
		t.Errorf("second delete status=%d", del2.Code)
	}
}

func TestResponsesCarryAnssiLayer(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	h := newTestHandler(t, store)

	// Cluster.
	clusterResp := do(h, http.MethodPost, "/v1/clusters", `{"name":"layer-check"}`)
	if clusterResp.Code != http.StatusCreated {
		t.Fatalf("create cluster: status=%d body=%q", clusterResp.Code, clusterResp.Body.String())
	}
	var cluster Cluster
	if err := json.Unmarshal(clusterResp.Body.Bytes(), &cluster); err != nil {
		t.Fatalf("decode cluster: %v", err)
	}
	if cluster.Layer == nil || *cluster.Layer != LayerCluster {
		t.Errorf("cluster layer=%v, want %q", cluster.Layer, LayerCluster)
	}

	// Node.
	nodeBody := fmt.Sprintf(`{"cluster_id":%q,"name":"node-a"}`, cluster.Id.String())
	nodeResp := do(h, http.MethodPost, "/v1/nodes", nodeBody)
	if nodeResp.Code != http.StatusCreated {
		t.Fatalf("create node: status=%d body=%q", nodeResp.Code, nodeResp.Body.String())
	}
	var node Node
	if err := json.Unmarshal(nodeResp.Body.Bytes(), &node); err != nil {
		t.Fatalf("decode node: %v", err)
	}
	if node.Layer == nil || *node.Layer != LayerNode {
		t.Errorf("node layer=%v, want %q", node.Layer, LayerNode)
	}

	// Namespace.
	nsBody := fmt.Sprintf(`{"cluster_id":%q,"name":"default"}`, cluster.Id.String())
	nsResp := do(h, http.MethodPost, "/v1/namespaces", nsBody)
	if nsResp.Code != http.StatusCreated {
		t.Fatalf("create namespace: status=%d body=%q", nsResp.Code, nsResp.Body.String())
	}
	var ns Namespace
	if err := json.Unmarshal(nsResp.Body.Bytes(), &ns); err != nil {
		t.Fatalf("decode namespace: %v", err)
	}
	if ns.Layer == nil || *ns.Layer != LayerNamespace {
		t.Errorf("namespace layer=%v, want %q", ns.Layer, LayerNamespace)
	}

	// Layer must also be set on GET and on list items.
	getResp := do(h, http.MethodGet, "/v1/clusters/"+cluster.Id.String(), "")
	if err := json.Unmarshal(getResp.Body.Bytes(), &cluster); err != nil {
		t.Fatalf("decode get cluster: %v", err)
	}
	if cluster.Layer == nil || *cluster.Layer != LayerCluster {
		t.Errorf("get cluster layer=%v, want %q", cluster.Layer, LayerCluster)
	}

	listResp := do(h, http.MethodGet, "/v1/nodes", "")
	var page NodeList
	if err := json.Unmarshal(listResp.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode node list: %v", err)
	}
	if len(page.Items) == 0 || page.Items[0].Layer == nil || *page.Items[0].Layer != LayerNode {
		t.Errorf("list node layer=%v, want %q", page.Items, LayerNode)
	}
}

func TestCreateClusterValidation(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, newMemStore())

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"empty body", "", http.StatusBadRequest},
		{"missing name", `{"environment":"dev"}`, http.StatusBadRequest},
		{"unknown field", `{"name":"x","bogus":true}`, http.StatusBadRequest},
		{"malformed json", `{`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rr := do(h, http.MethodPost, "/v1/clusters", tt.body)
			if rr.Code != tt.wantStatus {
				t.Errorf("status=%d want=%d body=%q", rr.Code, tt.wantStatus, rr.Body.String())
			}
		})
	}
}

func TestUnknownRoute404(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, newMemStore())
	rr := do(h, http.MethodGet, "/no-such-path", "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status=%d", rr.Code)
	}
}

func do(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
