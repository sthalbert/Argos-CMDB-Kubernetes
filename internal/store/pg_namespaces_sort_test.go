package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// seedNamespacesForSort creates one cluster (with a unique name) and namespaces
// named like the given names, returning the cluster id.
func seedNamespacesForSort(t *testing.T, pg *PG, names []string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	clusterName := "ns-sort-fixture-" + uuid.New().String()[:8]
	c, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: clusterName})
	if err != nil {
		t.Fatalf("cluster: %v", err)
	}
	for _, n := range names {
		if _, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{ClusterId: *c.Id, Name: n}); err != nil {
			t.Fatalf("namespace %s: %v", n, err)
		}
	}
	return *c.Id
}

func TestListNamespacesSortByName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	clusterID := seedNamespacesForSort(t, pg, []string{"beta", "alpha", "delta", "gamma", "epsilon"})

	var got []string
	page := api.ListPage{Limit: 2, Sort: "name"}
	for {
		items, next, err := pg.ListNamespaces(ctx, api.NamespaceListFilter{ClusterID: &clusterID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, n := range items {
			got = append(got, n.Name)
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	want := []string{"alpha", "beta", "delta", "epsilon", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("asc order: got %v, want %v", got, want)
		}
	}

	// Descending flips the order.
	items, _, err := pg.ListNamespaces(ctx, api.NamespaceListFilter{ClusterID: &clusterID}, api.ListPage{Limit: 10, Sort: "name", Order: "desc"})
	if err != nil {
		t.Fatalf("desc: %v", err)
	}
	if items[0].Name != "gamma" {
		t.Errorf("desc first = %s, want gamma", items[0].Name)
	}
}

//nolint:gocyclo // test-only paging loop with dedup and order checks; mirrors TestListNodesSortTieBreakAcrossPages
func TestListNamespacesSortTieBreakAcrossPages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedNamespacesForSort(t, pg, []string{"ns1", "ns2", "ns3", "ns4", "ns5"})
	// Two phase groups force the id tiebreaker across page boundaries.
	if _, err := pg.pool.Exec(ctx,
		`UPDATE namespaces SET phase = CASE WHEN name IN ('ns1','ns2') THEN 'Active' ELSE 'Terminating' END`); err != nil {
		t.Fatalf("set phases: %v", err)
	}

	seen := map[string]bool{}
	var phases []string
	page := api.ListPage{Limit: 2, Sort: "phase"}
	for {
		items, next, err := pg.ListNamespaces(ctx, api.NamespaceListFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range items {
			n := &items[i]
			if seen[n.Id.String()] {
				t.Fatalf("namespace %s duplicated across pages (tiebreaker broken)", n.Id)
			}
			seen[n.Id.String()] = true
			if n.Phase != nil {
				phases = append(phases, *n.Phase)
			}
		}
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if len(phases) != 5 {
		t.Fatalf("total=%d want 5 (row skipped at tied page boundary)", len(phases))
	}
	want := []string{"Active", "Active", "Terminating", "Terminating", "Terminating"}
	for i := range want {
		if phases[i] != want[i] {
			t.Fatalf("phase order = %v, want %v", phases, want)
		}
	}
}

func TestListNamespacesNameFilterGlob(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	clusterID := seedNamespacesForSort(t, pg, []string{"prod-web", "prod-db", "dev-web", "my_ns"})

	cases := []struct {
		term string
		want int
	}{
		{"web", 2},    // substring
		{"prod-*", 2}, // prefix glob
		{"*-web", 2},  // suffix glob
		{"my_ns", 1},  // literal underscore (must not match e.g. "myxns")
		{"WEB", 2},    // case-insensitive
	}
	for _, tc := range cases {
		name := tc.term
		items, _, err := pg.ListNamespaces(ctx, api.NamespaceListFilter{ClusterID: &clusterID, Name: &name}, api.ListPage{Limit: 50})
		if err != nil {
			t.Fatalf("%q: %v", tc.term, err)
		}
		if len(items) != tc.want {
			t.Errorf("name=%q: got %d items, want %d", tc.term, len(items), tc.want)
		}
	}
}

func TestListNamespacesRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	seedNamespacesForSort(t, pg, []string{"a", "b", "c"})

	if _, _, err := pg.ListNamespaces(ctx, api.NamespaceListFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	_, next, err := pg.ListNamespaces(ctx, api.NamespaceListFilter{}, api.ListPage{Limit: 1, Sort: "name"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	// Replay the sort=name cursor under created_at → invalid.
	if _, _, err := pg.ListNamespaces(ctx, api.NamespaceListFilter{}, api.ListPage{Limit: 1, Cursor: next}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", err)
	}
	// Legacy pipe cursor → invalid.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListNamespaces(ctx, api.NamespaceListFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
}

func TestListNamespacesDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	clusterID := seedNamespacesForSort(t, pg, []string{"ns1", "ns2", "ns3", "ns4", "ns5"})

	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListNamespaces(ctx, api.NamespaceListFilter{ClusterID: &clusterID}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, n := range items {
			if seen[n.Id.String()] {
				t.Fatalf("duplicate %s across pages", n.Id)
			}
			seen[n.Id.String()] = true
		}
		total += len(items)
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if total != 5 {
		t.Fatalf("total=%d want 5", total)
	}
}
