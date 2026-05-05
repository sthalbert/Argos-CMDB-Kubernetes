package timetravel

import (
	"reflect"
	"strings"
)

// PatchOp is a JSON-Patch-shaped operation describing a single watched-field
// change between two snapshots of an entity row.
type PatchOp struct {
	Op   string `json:"op"`
	Path string `json:"path"`
	From any    `json:"from,omitempty"`
	To   any    `json:"to,omitempty"`
}

// systemAnnotationPrefixes are annotation key prefixes written by background
// enrichers (EOL, etc.) rather than by operators. Annotation diffs ignore
// these keys so enricher churn does not generate a history row per tick.
// Operator-set annotations still trigger history normally.
var systemAnnotationPrefixes = []string{"longue-vue.io/eol."}

func stripSystemAnnotations(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		skip := false
		for _, prefix := range systemAnnotationPrefixes {
			if strings.HasPrefix(k, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			out[k] = val
		}
	}
	return out
}

func sanitizeForDiff(field string, v any) any {
	if field == "annotations" {
		return stripSystemAnnotations(v)
	}
	return v
}

// Diff computes a JSON-Patch-shaped slice describing changes to the watched
// fields between prev and next. JSONB columns are treated as opaque values
// and compared with reflect.DeepEqual. A noop diff returns an empty
// (non-nil) slice.
func Diff(prev, next map[string]any, watched []string) []PatchOp { //nolint:gocyclo // small per-field switch; splitting hurts clarity
	ops := make([]PatchOp, 0)
	for _, field := range watched {
		prevVal, prevOK := prev[field]
		nextVal, nextOK := next[field]

		prevSan := sanitizeForDiff(field, prevVal)
		nextSan := sanitizeForDiff(field, nextVal)

		prevAbsent := !prevOK || prevSan == nil || isEmptyMap(prevSan)
		nextAbsent := !nextOK || nextSan == nil || isEmptyMap(nextSan)

		switch {
		case prevAbsent && nextAbsent:
		case prevAbsent:
			ops = append(ops, PatchOp{Op: "add", Path: "/" + field, To: nextSan})
		case nextAbsent:
			ops = append(ops, PatchOp{Op: "remove", Path: "/" + field, From: prevSan})
		default:
			if !reflect.DeepEqual(prevSan, nextSan) {
				ops = append(ops, PatchOp{Op: "replace", Path: "/" + field, From: prevSan, To: nextSan})
			}
		}
	}
	return ops
}

func isEmptyMap(v any) bool {
	m, ok := v.(map[string]any)
	return ok && len(m) == 0
}
