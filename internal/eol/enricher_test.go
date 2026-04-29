package eol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/api"
)

// fakeStore implements EnricherStore for tests.
type fakeStore struct {
	mu         sync.Mutex
	clusters   []api.Cluster
	nodes      []api.Node
	eolEnabled bool
}

func (s *fakeStore) GetSettings(_ context.Context) (api.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return api.Settings{EOLEnabled: s.eolEnabled}, nil
}

func (s *fakeStore) ListClusters(_ context.Context, _ int, _ string) ([]api.Cluster, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]api.Cluster, len(s.clusters))
	copy(cp, s.clusters)
	return cp, "", nil
}

func (s *fakeStore) GetCluster(_ context.Context, id uuid.UUID) (api.Cluster, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.clusters {
		if s.clusters[i].Id != nil && *s.clusters[i].Id == id {
			return s.clusters[i], nil
		}
	}
	return api.Cluster{}, api.ErrNotFound
}

//nolint:gocritic // hugeParam: must match the EnricherStore interface signature.
func (s *fakeStore) UpdateCluster(_ context.Context, id uuid.UUID, in api.ClusterUpdate) (api.Cluster, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.clusters {
		if s.clusters[i].Id != nil && *s.clusters[i].Id == id {
			if in.Annotations != nil {
				s.clusters[i].Annotations = in.Annotations
			}
			return s.clusters[i], nil
		}
	}
	return api.Cluster{}, api.ErrNotFound
}

func (s *fakeStore) ListNodes(_ context.Context, clusterID *uuid.UUID, _ int, _ string) ([]api.Node, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []api.Node
	for i := range s.nodes {
		if clusterID == nil || s.nodes[i].ClusterId == *clusterID {
			result = append(result, s.nodes[i])
		}
	}
	return result, "", nil
}

func (s *fakeStore) GetNode(_ context.Context, id uuid.UUID) (api.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.nodes {
		if s.nodes[i].Id != nil && *s.nodes[i].Id == id {
			return s.nodes[i], nil
		}
	}
	return api.Node{}, api.ErrNotFound
}

//nolint:gocritic // hugeParam: must match the EnricherStore interface signature.
func (s *fakeStore) UpdateNode(_ context.Context, id uuid.UUID, in api.NodeUpdate) (api.Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.nodes {
		if s.nodes[i].Id != nil && *s.nodes[i].Id == id {
			if in.Annotations != nil {
				s.nodes[i].Annotations = in.Annotations
			}
			return s.nodes[i], nil
		}
	}
	return api.Node{}, api.ErrNotFound
}

func ptr[T any](v T) *T { return &v }

func TestEnricherEnrichesClusterKubernetesVersion(t *testing.T) {
	t.Parallel()

	// Set up a fake endoflife.date server with a past EOL date.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/kubernetes.json" {
			// endoflife.date returns newest cycle first.
			serveCycles(w, []Cycle{
				{Cycle: "1.30", EOL: "2028-06-28", Support: "2028-04-28", Latest: "1.30.14"},
				{Cycle: "1.28", EOL: "2024-11-28", Support: "2024-09-28", Latest: "1.28.15"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	clusterID := uuid.New()
	store := &fakeStore{
		eolEnabled: true,
		clusters: []api.Cluster{
			{Id: &clusterID, Name: "test", KubernetesVersion: ptr("v1.28.5")},
		},
	}

	client := NewClient(srv.URL, 1*time.Hour, srv.Client())
	enricher := NewEnricher(store, client, 1*time.Hour, 90)

	enricher.enrich(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()

	ann := store.clusters[0].Annotations
	if ann == nil {
		t.Fatal("expected annotations to be set")
		return
	}
	raw, ok := (*ann)["argos.io/eol.kubernetes"]
	if !ok {
		t.Fatal("expected argos.io/eol.kubernetes annotation")
	}

	var parsed Annotation
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("unmarshal annotation: %v", err)
	}
	if parsed.EOLStatus != StatusEOL {
		t.Errorf("eol_status = %q, want %q", parsed.EOLStatus, StatusEOL)
	}
	if parsed.Cycle != "1.28" {
		t.Errorf("cycle = %q, want 1.28", parsed.Cycle)
	}
	if parsed.Latest != "1.28.15" {
		t.Errorf("latest = %q, want 1.28.15", parsed.Latest)
	}
	// endoflife.date returns newest cycle first (1.30), so
	// latest_available should be "1.30.14" even though the entity is on 1.28.
	if parsed.LatestAvailable != "1.30.14" {
		t.Errorf("latest_available = %q, want 1.30.14", parsed.LatestAvailable)
	}
}

func TestEnricherEnrichesNodeFields(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/kubernetes.json":
			serveCycles(w, []Cycle{{Cycle: "1.30", EOL: "2028-06-28", Latest: "1.30.14"}})
		case "/api/containerd.json":
			serveCycles(w, []Cycle{{Cycle: "1.7", EOL: "2028-12-31", Latest: "1.7.25"}})
		case "/api/ubuntu.json":
			serveCycles(w, []Cycle{{Cycle: "22.04", EOL: "2029-04-01", Latest: "22.04.4"}})
		case "/api/linux.json":
			serveCycles(w, []Cycle{{Cycle: "5.15", EOL: "2028-10-01", Latest: "5.15.150"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	clusterID := uuid.New()
	nodeID := uuid.New()
	store := &fakeStore{
		eolEnabled: true,
		clusters: []api.Cluster{
			{Id: &clusterID, Name: "test", KubernetesVersion: ptr("v1.30.2")},
		},
		nodes: []api.Node{
			{
				Id:                      &nodeID,
				ClusterId:               clusterID,
				Name:                    "node-1",
				KubeletVersion:          ptr("v1.30.2"),
				ContainerRuntimeVersion: ptr("containerd://1.7.15"),
				OsImage:                 ptr("Ubuntu 22.04.3 LTS"),
				KernelVersion:           ptr("5.15.0-91-generic"),
			},
		},
	}

	client := NewClient(srv.URL, 1*time.Hour, srv.Client())
	enricher := NewEnricher(store, client, 1*time.Hour, 90)

	enricher.enrich(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()

	ann := store.nodes[0].Annotations
	if ann == nil {
		t.Fatal("expected node annotations to be set")
	}

	expected := []string{
		"argos.io/eol.kubernetes",
		"argos.io/eol.containerd",
		"argos.io/eol.ubuntu",
		"argos.io/eol.linux",
	}
	for _, key := range expected {
		raw, ok := (*ann)[key]
		if !ok {
			t.Errorf("missing annotation %q", key)
			continue
		}
		var parsed Annotation
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			t.Errorf("unmarshal %s: %v", key, err)
			continue
		}
		if parsed.EOLStatus != StatusSupported {
			t.Errorf("%s: eol_status = %q, want %q", key, parsed.EOLStatus, StatusSupported)
		}
	}
}

func TestEnricherPreservesExistingAnnotations(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		serveCycles(w, []Cycle{{Cycle: "1.30", EOL: "2028-06-28", Latest: "1.30.14"}})
	}))
	defer srv.Close()

	clusterID := uuid.New()
	existing := map[string]string{"team": "platform", "tier": "production"}
	store := &fakeStore{
		eolEnabled: true,
		clusters: []api.Cluster{
			{Id: &clusterID, Name: "test", KubernetesVersion: ptr("v1.30.2"), Annotations: &existing},
		},
	}

	client := NewClient(srv.URL, 1*time.Hour, srv.Client())
	enricher := NewEnricher(store, client, 1*time.Hour, 90)

	enricher.enrich(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()

	ann := store.clusters[0].Annotations
	if ann == nil {
		t.Fatal("expected annotations")
		return
	}
	if (*ann)["team"] != "platform" {
		t.Error("existing 'team' annotation was overwritten")
	}
	if (*ann)["tier"] != "production" {
		t.Error("existing 'tier' annotation was overwritten")
	}
	if _, ok := (*ann)["argos.io/eol.kubernetes"]; !ok {
		t.Error("eol annotation was not added")
	}
}

func TestMergeAnnotation(t *testing.T) {
	t.Parallel()

	ann := &Annotation{Product: "kubernetes", Cycle: "1.28", EOLStatus: StatusEOL}

	t.Run("nil existing", func(t *testing.T) {
		t.Parallel()
		merged := mergeAnnotation(nil, "kubernetes", ann)
		if _, ok := merged["argos.io/eol.kubernetes"]; !ok {
			t.Error("expected eol key")
		}
	})

	t.Run("preserves existing keys", func(t *testing.T) {
		t.Parallel()
		existing := map[string]string{"custom": "value"}
		merged := mergeAnnotation(&existing, "kubernetes", ann)
		if merged["custom"] != "value" {
			t.Error("custom key was lost")
		}
		if _, ok := merged["argos.io/eol.kubernetes"]; !ok {
			t.Error("expected eol key")
		}
	})

	t.Run("overwrites previous eol key", func(t *testing.T) {
		t.Parallel()
		existing := map[string]string{"argos.io/eol.kubernetes": `{"old":"data"}`}
		merged := mergeAnnotation(&existing, "kubernetes", ann)
		var parsed Annotation
		if err := json.Unmarshal([]byte(merged["argos.io/eol.kubernetes"]), &parsed); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if parsed.Cycle != "1.28" {
			t.Errorf("cycle = %q, want 1.28", parsed.Cycle)
		}
	})
}
