package store

import (
	"context"
	"testing"
	"time"

	"github.com/sthalbert/longue-vue/internal/api"
)

// ageHeartbeat rewinds a cluster's last_seen_at, simulating a collector
// that stopped reporting `days` ago.
func ageHeartbeat(t *testing.T, pg *PG, name string, days int) {
	t.Helper()
	tag, err := pg.pool.Exec(context.Background(),
		`UPDATE clusters SET last_seen_at = now() - make_interval(days => $1) WHERE name = $2`,
		days, name)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("age heartbeat %q: err=%v rows=%d", name, err, tag.RowsAffected())
	}
}

func TestEnsureClusterRefreshesHeartbeat(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	created, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "hb-cluster"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.LastSeenAt == nil {
		t.Fatal("create: LastSeenAt not set")
	}

	ageHeartbeat(t, pg, "hb-cluster", 10)
	aged, err := pg.GetClusterByName(ctx, "hb-cluster")
	if err != nil {
		t.Fatalf("get aged: %v", err)
	}
	prevUpdated := *aged.UpdatedAt

	// NO-OP ensure must refresh the heartbeat without touching updated_at.
	refreshed, wasCreated, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "hb-cluster"})
	if err != nil || wasCreated {
		t.Fatalf("no-op ensure: err=%v created=%v", err, wasCreated)
	}
	if refreshed.LastSeenAt == nil || time.Since(*refreshed.LastSeenAt) > time.Minute {
		t.Fatalf("no-op ensure did not refresh heartbeat: %v", refreshed.LastSeenAt)
	}
	if !refreshed.UpdatedAt.Equal(prevUpdated) {
		t.Fatalf("no-op ensure moved updated_at: %v -> %v", prevUpdated, *refreshed.UpdatedAt)
	}

	// RESTORE branch refreshes the heartbeat too.
	if err := pg.SoftDeleteCluster(ctx, *refreshed.Id); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	ageHeartbeat(t, pg, "hb-cluster", 10)
	restored, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "hb-cluster"})
	if err != nil {
		t.Fatalf("restore ensure: %v", err)
	}
	if restored.LastSeenAt == nil || time.Since(*restored.LastSeenAt) > time.Minute {
		t.Fatalf("restore ensure did not refresh heartbeat: %v", restored.LastSeenAt)
	}
}

func TestListClustersStaleFilterAndSort(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	for _, name := range []string{"stale-a", "stale-b", "fresh-c"} {
		if _, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	ageHeartbeat(t, pg, "stale-a", 30)
	ageHeartbeat(t, pg, "stale-b", 9)

	cutoff := time.Now().UTC().AddDate(0, 0, -7)
	wantStale := true
	items, _, err := pg.ListClusters(ctx,
		api.ClusterListFilter{Stale: &wantStale, StaleCutoff: cutoff}, api.ListPage{Limit: 10})
	if err != nil {
		t.Fatalf("list stale: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("stale=true: got %d clusters, want 2", len(items))
	}

	wantFresh := false
	items, _, err = pg.ListClusters(ctx,
		api.ClusterListFilter{Stale: &wantFresh, StaleCutoff: cutoff}, api.ListPage{Limit: 10})
	if err != nil {
		t.Fatalf("list fresh: %v", err)
	}
	if len(items) != 1 || items[0].Name != "fresh-c" {
		t.Fatalf("stale=false: got %v, want [fresh-c]", items)
	}

	// Sort by last_seen_at ascending: oldest heartbeat first.
	items, _, err = pg.ListClusters(ctx, api.ClusterListFilter{},
		api.ListPage{Limit: 10, Sort: "last_seen_at", Order: "asc"})
	if err != nil {
		t.Fatalf("sort: %v", err)
	}
	if len(items) != 3 || items[0].Name != "stale-a" || items[2].Name != "fresh-c" {
		names := make([]string, len(items))
		for i := range items {
			names[i] = items[i].Name
		}
		t.Fatalf("sort last_seen_at asc: got %v, want [stale-a stale-b fresh-c]", names)
	}
}
