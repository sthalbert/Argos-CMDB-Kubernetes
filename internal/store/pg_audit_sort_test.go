package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// seedAuditEvent inserts one audit_events row with minimal required fields.
// action must be non-empty; OccurredAt defaults to now if zero.
func seedAuditEvent(t *testing.T, pg *PG, action string, occurredAt time.Time) {
	t.Helper()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC().Truncate(time.Microsecond)
	}
	in := api.AuditEventInsert{
		ID:         uuid.New(),
		OccurredAt: occurredAt,
		ActorKind:  "user",
		Action:     action,
		HTTPMethod: "POST",
		HTTPPath:   "/v1/test",
		HTTPStatus: 200,
		Source:     api.SourceAPI,
	}
	if err := pg.InsertAuditEvent(context.Background(), in); err != nil {
		t.Fatalf("seedAuditEvent(%q): %v", action, err)
	}
}

func TestListAuditEventsSortByAction(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Microsecond)

	// Seed 3 events with distinct actions in non-alphabetical order.
	seedAuditEvent(t, pg, "user.update", base.Add(-3*time.Second))
	seedAuditEvent(t, pg, "cluster.create", base.Add(-2*time.Second))
	seedAuditEvent(t, pg, "auth.login.success", base.Add(-1*time.Second))

	// Sort by action asc: expect auth.login.success < cluster.create < user.update
	items, _, err := pg.ListAuditEvents(ctx, api.AuditEventFilter{}, api.ListPage{Limit: 50, Sort: "action"})
	if err != nil {
		t.Fatalf("list sort=action: %v", err)
	}
	if len(items) < 3 {
		t.Fatalf("got %d items, want at least 3", len(items))
	}
	// Find our seeded events (there may be others from parallel tests).
	var actions []string
	for _, ev := range items {
		if ev.Action == "auth.login.success" || ev.Action == "cluster.create" || ev.Action == "user.update" {
			actions = append(actions, ev.Action)
		}
	}
	if len(actions) < 3 {
		t.Fatalf("could not find our 3 seeded actions in results: %v", actions)
	}
	// Verify ascending order within our seeded events.
	for i := 1; i < len(actions); i++ {
		if actions[i] < actions[i-1] {
			t.Fatalf("not sorted asc at index %d: %q > %q", i, actions[i-1], actions[i])
		}
	}
}

func TestListAuditEventsRejectsBadSortAndMismatchedCursor(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Microsecond)

	seedAuditEvent(t, pg, "ev.a", base.Add(-3*time.Second))
	seedAuditEvent(t, pg, "ev.b", base.Add(-2*time.Second))
	seedAuditEvent(t, pg, "ev.c", base.Add(-1*time.Second))

	// Unknown sort key → ErrInvalidSort.
	if _, _, err := pg.ListAuditEvents(ctx, api.AuditEventFilter{}, api.ListPage{Sort: "bogus"}); !errors.Is(err, api.ErrInvalidSort) {
		t.Errorf("bogus sort: %v, want ErrInvalidSort", err)
	}

	// Mint a cursor under sort=action, then replay it under default (occurred_at) → ErrInvalidCursor.
	_, next, err := pg.ListAuditEvents(ctx, api.AuditEventFilter{}, api.ListPage{Limit: 1, Sort: "action"})
	if err != nil || next == "" {
		t.Fatalf("seed cursor: next=%q err=%v", next, err)
	}
	if _, _, err := pg.ListAuditEvents(ctx, api.AuditEventFilter{}, api.ListPage{Limit: 1, Cursor: next}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("mismatched cursor: %v, want ErrInvalidCursor", err)
	}

	// Old-style pipe cursor → ErrInvalidCursor.
	legacy := encodeCursor(timeNowFixed(t), uuid.New())
	if _, _, err := pg.ListAuditEvents(ctx, api.AuditEventFilter{}, api.ListPage{Cursor: legacy}); !errors.Is(err, api.ErrInvalidCursor) {
		t.Errorf("legacy cursor: %v, want ErrInvalidCursor", err)
	}
}

func TestListAuditEventsDefaultOrderUnchanged(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Microsecond)

	// Insert 5 events with distinct occurred_at values.
	for i := range 5 {
		seedAuditEvent(t, pg, "do.ev", base.Add(time.Duration(i)*time.Second))
	}

	// Default order (occurred_at DESC) should yield no duplicates across pages.
	seen := map[string]bool{}
	page := api.ListPage{Limit: 2}
	total := 0
	for {
		items, next, err := pg.ListAuditEvents(ctx, api.AuditEventFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, ev := range items {
			id := ev.Id.String()
			if seen[id] {
				t.Fatalf("event %s duplicated across pages", id)
			}
			seen[id] = true
		}
		total += len(items)
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if total < 5 {
		t.Fatalf("total=%d want at least 5", total)
	}
}

func TestListAuditEventsSortTieBreakAcrossPages(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Microsecond)
	suffix := uuid.New().String()[:6]

	// Seed 4 events: 2 with action "alpha.*" and 2 with action "zeta.*".
	seedAuditEvent(t, pg, "alpha.one-"+suffix, base.Add(-4*time.Second))
	seedAuditEvent(t, pg, "alpha.two-"+suffix, base.Add(-3*time.Second))
	seedAuditEvent(t, pg, "zeta.one-"+suffix, base.Add(-2*time.Second))
	seedAuditEvent(t, pg, "zeta.two-"+suffix, base.Add(-1*time.Second))

	// Walk with limit=2 sorted by action asc; must see all 4 with no duplicates.
	seen := map[string]bool{}
	page := api.ListPage{Limit: 2, Sort: "action"}
	total := 0
	for {
		items, next, err := pg.ListAuditEvents(ctx, api.AuditEventFilter{}, page)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for i := range items {
			ev := &items[i]
			id := ev.Id.String()
			if seen[id] {
				t.Fatalf("event %s duplicated across pages (tiebreaker broken)", id)
			}
			seen[id] = true
		}
		total += len(items)
		if next == "" {
			break
		}
		page.Cursor = next
	}
	if total < 4 {
		t.Fatalf("total=%d want at least 4 (row skipped at tied page boundary)", total)
	}
}
