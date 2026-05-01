package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/collector"
)

// Compile-time check: Store satisfies collector.CmdbStore.
var _ collector.CmdbStore = (*Store)(nil)

// newTestStore creates a Store backed by the given httptest.Server.
func newTestStore(t *testing.T, srv *httptest.Server, extraHeaders map[string]string) *Store {
	t.Helper()
	s, err := NewStore(Config{
		ServerURL:    srv.URL,
		Token:        "test-token",
		ExtraHeaders: extraHeaders,
	})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestBearerTokenInjected(t *testing.T) {
	var gotAuth string
	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.Cluster{Id: &id, Name: "test"})
	}))
	defer srv.Close()

	s := newTestStore(t, srv, nil)
	_, _, _ = s.EnsureCluster(context.Background(), api.ClusterCreate{Name: "test"})

	if gotAuth != "Bearer test-token" {
		t.Errorf("expected 'Bearer test-token', got %q", gotAuth)
	}
}

func TestExtraHeadersInjected(t *testing.T) {
	var gotTenant, gotRoute string
	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTenant = r.Header.Get("X-Tenant-Id")
		gotRoute = r.Header.Get("X-Route-Key")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.Cluster{Id: &id, Name: "test"})
	}))
	defer srv.Close()

	s := newTestStore(t, srv, map[string]string{
		"X-Tenant-Id": "zad-prod",
		"X-Route-Key": "longue-vue",
	})
	_, _, _ = s.EnsureCluster(context.Background(), api.ClusterCreate{Name: "test"})

	if gotTenant != "zad-prod" {
		t.Errorf("X-Tenant-Id: want 'zad-prod', got %q", gotTenant)
	}
	if gotRoute != "longue-vue" {
		t.Errorf("X-Route-Key: want 'longue-vue', got %q", gotRoute)
	}
}

func TestBasePathPrepended(t *testing.T) {
	var gotPath string
	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.Cluster{Id: &id, Name: "prod"})
	}))
	defer srv.Close()

	// Simulate gateway path prefix: ServerURL = srv.URL + "/argos"
	s, err := NewStore(Config{
		ServerURL: srv.URL + "/argos",
		Token:     "t",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, _, _ = s.EnsureCluster(context.Background(), api.ClusterCreate{Name: "prod"})

	want := "/argos/v1/clusters"
	if gotPath != want {
		t.Errorf("path: want %q, got %q", want, gotPath)
	}
}

func TestEnsureClusterCreated(t *testing.T) {
	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: want POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/clusters" {
			t.Errorf("path: want /v1/clusters, got %s", r.URL.Path)
		}
		var body api.ClusterCreate
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body.Name != "new-cluster" {
			t.Errorf("body.Name: want 'new-cluster', got %q", body.Name)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.Cluster{Id: &id, Name: "new-cluster"})
	}))
	defer srv.Close()

	s := newTestStore(t, srv, nil)
	cluster, created, err := s.EnsureCluster(context.Background(), api.ClusterCreate{Name: "new-cluster"})
	if err != nil {
		t.Fatalf("EnsureCluster: %v", err)
	}
	if !created {
		t.Error("created: want true on 201, got false")
	}
	if cluster.Name != "new-cluster" {
		t.Errorf("cluster.Name: want 'new-cluster', got %q", cluster.Name)
	}
}

func TestEnsureClusterExisting(t *testing.T) {
	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(api.Cluster{Id: &id, Name: "existing"})
	}))
	defer srv.Close()

	s := newTestStore(t, srv, nil)
	cluster, created, err := s.EnsureCluster(context.Background(), api.ClusterCreate{Name: "existing"})
	if err != nil {
		t.Fatalf("EnsureCluster: %v", err)
	}
	if created {
		t.Error("created: want false on 200, got true")
	}
	if cluster.Name != "existing" {
		t.Errorf("cluster.Name: want 'existing', got %q", cluster.Name)
	}
}

func TestEnsureClusterErrorOn4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer srv.Close()

	s := newTestStore(t, srv, nil)
	_, _, err := s.EnsureCluster(context.Background(), api.ClusterCreate{Name: "x"})
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}

func TestUpsertNodeMethod(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody api.NodeCreate
	nodeID := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.Node{Id: &nodeID, Name: "node-1"})
	}))
	defer srv.Close()

	s := newTestStore(t, srv, nil)
	clusterID := uuid.New()
	node, err := s.UpsertNode(context.Background(), api.NodeCreate{
		ClusterId: clusterID,
		Name:      "node-1",
	})
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: want POST, got %s", gotMethod)
	}
	if gotPath != "/v1/nodes" {
		t.Errorf("path: want /v1/nodes, got %s", gotPath)
	}
	if gotBody.Name != "node-1" {
		t.Errorf("body.Name: want 'node-1', got %q", gotBody.Name)
	}
	if node.Name != "node-1" {
		t.Errorf("response.Name: want 'node-1', got %q", node.Name)
	}
}

func TestReconcileNodesMethod(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody reconcileClusterScopedBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(reconcileResultBody{Deleted: 3})
	}))
	defer srv.Close()

	s := newTestStore(t, srv, nil)
	clusterID := uuid.New()
	n, err := s.DeleteNodesNotIn(context.Background(), clusterID, []string{"node-1", "node-2"})
	if err != nil {
		t.Fatalf("DeleteNodesNotIn: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: want POST, got %s", gotMethod)
	}
	if gotPath != "/v1/nodes/reconcile" {
		t.Errorf("path: want /v1/nodes/reconcile, got %s", gotPath)
	}
	if gotBody.ClusterID != clusterID {
		t.Errorf("body.ClusterID: want %s, got %s", clusterID, gotBody.ClusterID)
	}
	if n != 3 {
		t.Errorf("deleted: want 3, got %d", n)
	}
}

func TestReconcileWorkloadsMethod(t *testing.T) {
	var gotBody reconcileWorkloadsBody

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(reconcileResultBody{Deleted: 1})
	}))
	defer srv.Close()

	s := newTestStore(t, srv, nil)
	nsID := uuid.New()
	n, err := s.DeleteWorkloadsNotIn(context.Background(), nsID,
		[]string{"Deployment", "StatefulSet"},
		[]string{"web", "db"},
	)
	if err != nil {
		t.Fatalf("DeleteWorkloadsNotIn: %v", err)
	}
	if gotBody.NamespaceID != nsID {
		t.Errorf("body.NamespaceID: want %s, got %s", nsID, gotBody.NamespaceID)
	}
	if len(gotBody.KeepKinds) != 2 || gotBody.KeepKinds[0] != "Deployment" {
		t.Errorf("body.KeepKinds: want [Deployment, StatefulSet], got %v", gotBody.KeepKinds)
	}
	if n != 1 {
		t.Errorf("deleted: want 1, got %d", n)
	}
}

func TestRetryOn5xx(t *testing.T) {
	var calls atomic.Int32
	id := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := calls.Add(1)
		if c < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"gateway timeout"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(api.Cluster{Id: &id, Name: "retry-test"})
	}))
	defer srv.Close()

	s := newTestStore(t, srv, nil)
	_, _, err := s.EnsureCluster(context.Background(), api.ClusterCreate{Name: "retry-test"})
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("expected 3 calls (2 retries + 1 success), got %d", calls.Load())
	}
}

func TestNoRetryOn401(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer srv.Close()

	s := newTestStore(t, srv, nil)
	_, _, err := s.EnsureCluster(context.Background(), api.ClusterCreate{Name: "no-retry"})
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if calls.Load() != 1 {
		t.Errorf("expected exactly 1 call (no retry on 401), got %d", calls.Load())
	}
}

func TestNoRetryOn403(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer srv.Close()

	s := newTestStore(t, srv, nil)
	_, err := s.UpsertNode(context.Background(), api.NodeCreate{Name: "x"})
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if calls.Load() != 1 {
		t.Errorf("expected exactly 1 call (no retry on 403), got %d", calls.Load())
	}
}

func TestUpdateClusterMethod(t *testing.T) {
	var gotMethod, gotPath string
	id := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(api.Cluster{Id: &id})
	}))
	defer srv.Close()

	s := newTestStore(t, srv, nil)
	v := "1.28"
	_, err := s.UpdateCluster(context.Background(), id, api.ClusterUpdate{KubernetesVersion: &v})
	if err != nil {
		t.Fatalf("UpdateCluster: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method: want PATCH, got %s", gotMethod)
	}
	if gotPath != "/v1/clusters/"+id.String() {
		t.Errorf("path: want /v1/clusters/%s, got %s", id, gotPath)
	}
}
