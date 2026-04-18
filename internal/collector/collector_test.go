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

type fakeFetcher struct {
	version string
	err     error
}

func (f *fakeFetcher) ServerVersion(_ context.Context) (string, error) {
	return f.version, f.err
}

type recordedUpdate struct {
	id    uuid.UUID
	patch api.ClusterUpdate
}

type fakeStore struct {
	mu        sync.Mutex
	clusters  []api.Cluster
	updates   []recordedUpdate
	listErr   error
	updateErr error
}

func (s *fakeStore) ListClusters(_ context.Context, _ int, _ string) ([]api.Cluster, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, "", s.listErr
	}
	out := make([]api.Cluster, len(s.clusters))
	copy(out, s.clusters)
	return out, "", nil
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

func TestPollUpdatesVersionWhenChanged(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	old := "v1.28.0"
	store := &fakeStore{
		clusters: []api.Cluster{
			{Id: &id, Name: "prod", KubernetesVersion: &old},
		},
	}
	c := New(store, &fakeFetcher{version: "v1.29.5"}, "prod", time.Minute, time.Second)

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
	store := &fakeStore{
		clusters: []api.Cluster{
			{Id: &id, Name: "prod", KubernetesVersion: &current},
		},
	}
	c := New(store, &fakeFetcher{version: current}, "prod", time.Minute, time.Second)

	c.poll(context.Background())

	if len(store.updates) != 0 {
		t.Errorf("expected no updates when version unchanged, got %d", len(store.updates))
	}
}

func TestPollSkipsOnFetcherError(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := &fakeStore{
		clusters: []api.Cluster{{Id: &id, Name: "prod"}},
	}
	c := New(store, &fakeFetcher{err: errors.New("boom")}, "prod", time.Minute, time.Second)

	c.poll(context.Background())

	if len(store.updates) != 0 {
		t.Errorf("expected no updates on fetcher error, got %d", len(store.updates))
	}
}

func TestPollSkipsOnListError(t *testing.T) {
	t.Parallel()
	store := &fakeStore{listErr: errors.New("db down")}
	c := New(store, &fakeFetcher{version: "v1.29.5"}, "prod", time.Minute, time.Second)

	c.poll(context.Background())

	if len(store.updates) != 0 {
		t.Errorf("expected no updates on list error, got %d", len(store.updates))
	}
}

func TestPollSkipsWhenClusterNotRegistered(t *testing.T) {
	t.Parallel()
	store := &fakeStore{clusters: []api.Cluster{}}
	c := New(store, &fakeFetcher{version: "v1.29.5"}, "missing", time.Minute, time.Second)

	c.poll(context.Background())

	if len(store.updates) != 0 {
		t.Errorf("expected no updates when cluster missing, got %d", len(store.updates))
	}
}

type signalingFetcher struct {
	calls atomic.Int64
	ch    chan struct{}
}

func (c *signalingFetcher) ServerVersion(_ context.Context) (string, error) {
	c.calls.Add(1)
	select {
	case c.ch <- struct{}{}:
	default:
	}
	return "v1.29.5", nil
}

func TestRunStopsOnContextCancel(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	store := &fakeStore{
		clusters: []api.Cluster{{Id: &id, Name: "prod"}},
	}
	fetcher := &signalingFetcher{ch: make(chan struct{}, 4)}
	c := New(store, fetcher, "prod", 20*time.Millisecond, time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	// Wait deterministically for two ticks (startup + one ticker fire).
	waitForSignal(t, fetcher.ch, 2*time.Second)
	waitForSignal(t, fetcher.ch, 2*time.Second)

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if got := fetcher.calls.Load(); got < 2 {
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
