package api

import (
	"context"
	"testing"
	"time"

	"github.com/sthalbert/longue-vue/internal/auth"
)

func newStaleTestServer(days int) (*Server, *memStore) {
	ms := newMemStore()
	ms.settings = Settings{ClusterStaleAfterDays: days}
	s := NewServer("test", ms, auth.SecureNever, nil, NewLoginRateLimiter(), NewVerifyRateLimiter())
	return s, ms
}

// ageMemCluster rewinds the in-memory heartbeat.
func ageMemCluster(ms *memStore, c Cluster, days int) {
	past := time.Now().UTC().AddDate(0, 0, -days)
	stored := ms.byID[*c.Id]
	stored.LastSeenAt = &past
	ms.byID[*c.Id] = stored
}

func TestListClusters_StaleDecorationAndFilter(t *testing.T) {
	s, ms := newStaleTestServer(7)
	ctx := context.Background()

	fresh, _, err := ms.EnsureCluster(ctx, ClusterCreate{Name: "fresh"})
	if err != nil {
		t.Fatalf("ensure fresh: %v", err)
	}
	if fresh.LastSeenAt == nil {
		t.Fatal("memStore.EnsureCluster must set LastSeenAt")
	}
	old, _, err := ms.EnsureCluster(ctx, ClusterCreate{Name: "old"})
	if err != nil {
		t.Fatalf("ensure old: %v", err)
	}
	ageMemCluster(ms, old, 10)

	resp, err := s.ListClusters(ctx, ListClustersRequestObject{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	list := resp.(ListClusters200JSONResponse)
	if len(list.Items) != 2 {
		t.Fatalf("unfiltered list: got %d items, want 2", len(list.Items))
	}
	for _, c := range list.Items {
		wantStale := c.Name == "old"
		if c.Stale == nil || *c.Stale != wantStale {
			t.Fatalf("cluster %s: stale=%v, want %v", c.Name, c.Stale, wantStale)
		}
	}

	staleTrue := true
	resp, err = s.ListClusters(ctx, ListClustersRequestObject{
		Params: ListClustersParams{Stale: &staleTrue},
	})
	if err != nil {
		t.Fatalf("list stale=true: %v", err)
	}
	list = resp.(ListClusters200JSONResponse)
	if len(list.Items) != 1 || list.Items[0].Name != "old" {
		t.Fatalf("stale=true: got %+v, want only 'old'", list.Items)
	}
}

func TestListClusters_FeatureDisabled(t *testing.T) {
	s, ms := newStaleTestServer(0)
	ctx := context.Background()

	c, _, err := ms.EnsureCluster(ctx, ClusterCreate{Name: "silent"})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	ageMemCluster(ms, c, 100)

	resp, err := s.ListClusters(ctx, ListClustersRequestObject{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	list := resp.(ListClusters200JSONResponse)
	if len(list.Items) != 1 || list.Items[0].Stale == nil || *list.Items[0].Stale {
		t.Fatalf("disabled: stale must be false, got %+v", list.Items)
	}

	staleTrue := true
	resp, err = s.ListClusters(ctx, ListClustersRequestObject{
		Params: ListClustersParams{Stale: &staleTrue},
	})
	if err != nil {
		t.Fatalf("list stale=true disabled: %v", err)
	}
	list = resp.(ListClusters200JSONResponse)
	if len(list.Items) != 0 {
		t.Fatalf("disabled + stale=true: want empty page, got %d items", len(list.Items))
	}
}

func TestGetCluster_StaleDecoration(t *testing.T) {
	s, ms := newStaleTestServer(7)
	ctx := context.Background()

	c, _, err := ms.EnsureCluster(ctx, ClusterCreate{Name: "solo"})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	ageMemCluster(ms, c, 10)

	resp, err := s.GetCluster(ctx, GetClusterRequestObject{Id: *c.Id})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got := Cluster(resp.(GetCluster200JSONResponse))
	if got.Stale == nil || !*got.Stale {
		t.Fatalf("detail: stale=%v, want true", got.Stale)
	}
}

func TestCreateCluster_NoOpTickSkipsAudit(t *testing.T) {
	s, _ := newStaleTestServer(7)
	body := ClusterCreate{Name: "tick"}

	ctx1, bag1 := WithAuditBagForTest(context.Background())
	if _, err := s.CreateCluster(ctx1, CreateClusterRequestObject{Body: &body}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if bag1.SkipForTest() {
		t.Fatal("first create must be audited")
	}

	ctx2, bag2 := WithAuditBagForTest(context.Background())
	if _, err := s.CreateCluster(ctx2, CreateClusterRequestObject{Body: &body}); err != nil {
		t.Fatalf("no-op ensure: %v", err)
	}
	if !bag2.SkipForTest() {
		t.Fatal("no-op ensure tick must set the audit skip flag")
	}
}
