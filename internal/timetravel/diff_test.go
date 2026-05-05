package timetravel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDiff_NoChange(t *testing.T) {
	prev := map[string]any{"kubernetes_version": "1.29"}
	next := map[string]any{"kubernetes_version": "1.29"}
	ops := Diff(prev, next, []string{"kubernetes_version"})
	assert.Empty(t, ops)
}

func TestDiff_SingleStringChange(t *testing.T) {
	prev := map[string]any{"kubernetes_version": "1.28"}
	next := map[string]any{"kubernetes_version": "1.29"}
	ops := Diff(prev, next, []string{"kubernetes_version"})
	assert.Len(t, ops, 1)
	assert.Equal(t, "replace", ops[0].Op)
	assert.Equal(t, "/kubernetes_version", ops[0].Path)
	assert.Equal(t, "1.28", ops[0].From)
	assert.Equal(t, "1.29", ops[0].To)
}

func TestDiff_JSONBChange(t *testing.T) {
	prev := map[string]any{"annotations": map[string]any{"a": "1"}}
	next := map[string]any{"annotations": map[string]any{"a": "2"}}
	ops := Diff(prev, next, []string{"annotations"})
	assert.Len(t, ops, 1)
	assert.Equal(t, "replace", ops[0].Op)
}

func TestDiff_JSONBNoChange(t *testing.T) {
	m := map[string]any{"a": "1"}
	prev := map[string]any{"annotations": m}
	next := map[string]any{"annotations": m}
	ops := Diff(prev, next, []string{"annotations"})
	assert.Empty(t, ops)
}

func TestDiff_BothNilFields(t *testing.T) {
	prev := map[string]any{"terminated_at": nil}
	next := map[string]any{"terminated_at": nil}
	ops := Diff(prev, next, []string{"terminated_at"})
	assert.Empty(t, ops)
}

func TestDiff_PrevNilNextSet(t *testing.T) {
	prev := map[string]any{"terminated_at": nil}
	next := map[string]any{"terminated_at": "2026-01-01T00:00:00Z"}
	ops := Diff(prev, next, []string{"terminated_at"})
	assert.Len(t, ops, 1)
	assert.Equal(t, "add", ops[0].Op)
	assert.Equal(t, "/terminated_at", ops[0].Path)
	assert.Nil(t, ops[0].From)
	assert.Equal(t, "2026-01-01T00:00:00Z", ops[0].To)
}

func TestDiff_PrevSetNextNil(t *testing.T) {
	prev := map[string]any{"terminated_at": "2026-01-01T00:00:00Z"}
	next := map[string]any{"terminated_at": nil}
	ops := Diff(prev, next, []string{"terminated_at"})
	assert.Len(t, ops, 1)
	assert.Equal(t, "remove", ops[0].Op)
	assert.Equal(t, "2026-01-01T00:00:00Z", ops[0].From)
	assert.Nil(t, ops[0].To)
}

func TestDiff_WatchedSubset(t *testing.T) {
	prev := map[string]any{"kubernetes_version": "1.28", "owner": "alice"}
	next := map[string]any{"kubernetes_version": "1.29", "owner": "alice"}
	// Only watch kubernetes_version — owner change is irrelevant.
	ops := Diff(prev, next, []string{"kubernetes_version"})
	assert.Len(t, ops, 1)
	assert.Equal(t, "/kubernetes_version", ops[0].Path)
}
