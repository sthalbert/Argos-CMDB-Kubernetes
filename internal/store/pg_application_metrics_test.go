package store

import (
	"context"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

// TestPGApplicationMetricCounts seeds applications across the (has_block,
// has_dict) matrix plus a standalone block and asserts ApplicationMetricCounts
// returns the expected per-combination buckets and block total (ADR-0029 §9).
//
//nolint:gocyclo // flat table assertions over a small fixture; complexity is inherent.
func TestPGApplicationMetricCounts(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	blk, err := pg.CreateApplicationBlock(ctx, api.ApplicationBlockCreate{Name: "platform-block"})
	if err != nil {
		t.Fatalf("create block: %v", err)
	}
	// A second, unreferenced block — exercises the standalone block count.
	if _, err := pg.CreateApplicationBlock(ctx, api.ApplicationBlockCreate{Name: "ops-block"}); err != nil {
		t.Fatalf("create second block: %v", err)
	}

	d := 3
	// 1) block + dict.
	if _, err := pg.CreateApplication(ctx, api.ApplicationCreate{
		Name:               "billing",
		ApplicationBlockID: &blk.ID,
		SecDisponibilite:   &d,
	}); err != nil {
		t.Fatalf("create block+dict app: %v", err)
	}
	// 2) block, no dict.
	if _, err := pg.CreateApplication(ctx, api.ApplicationCreate{
		Name:               "checkout",
		ApplicationBlockID: &blk.ID,
	}); err != nil {
		t.Fatalf("create block-only app: %v", err)
	}
	// 3) no block, dict.
	if _, err := pg.CreateApplication(ctx, api.ApplicationCreate{
		Name:           "vault",
		SecTracabilite: &d,
	}); err != nil {
		t.Fatalf("create dict-only app: %v", err)
	}
	// 4) neither.
	if _, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: "scratch"}); err != nil {
		t.Fatalf("create neither app: %v", err)
	}

	buckets, blocks, err := pg.ApplicationMetricCounts(ctx)
	if err != nil {
		t.Fatalf("ApplicationMetricCounts: %v", err)
	}
	if blocks != 2 {
		t.Errorf("blocks = %d, want 2", blocks)
	}

	// Fold the returned slice into a (has_block, has_dict) → count map so the
	// assertions don't depend on row order.
	got := map[[2]bool]int{}
	for _, b := range buckets {
		got[[2]bool{b.HasBlock, b.HasDict}] += b.Count
	}
	want := map[[2]bool]int{
		{true, true}:   1, // billing
		{true, false}:  1, // checkout
		{false, true}:  1, // vault
		{false, false}: 1, // scratch
	}
	for key, n := range want {
		if got[key] != n {
			t.Errorf("bucket has_block=%v has_dict=%v = %d, want %d", key[0], key[1], got[key], n)
		}
	}
	if len(buckets) != 4 {
		t.Errorf("got %d buckets, want 4 distinct combinations", len(buckets))
	}

	// Sanity: the setter accepts the buckets without panicking on the label
	// permutations (exercises the metrics wiring end-to-end).
	metrics.SetApplicationCounts(buckets)
	metrics.SetApplicationBlocksTotal(blocks)
}
