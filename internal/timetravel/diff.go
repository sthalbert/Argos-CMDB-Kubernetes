package timetravel

import "reflect"

// PatchOp is a JSON-Patch-shaped operation describing a single watched-field
// change between two snapshots of an entity row.
type PatchOp struct {
	// Op is "replace", "add", or "remove".
	Op string `json:"op"`
	// Path is "/<field>".
	Path string `json:"path"`
	// From is the previous value (nil for op="add").
	From any `json:"from,omitempty"`
	// To is the next value (nil for op="remove").
	To any `json:"to,omitempty"`
}

// Diff computes a JSON-Patch-shaped slice describing changes to the watched
// fields between prev and next. JSONB columns are treated as opaque values and
// compared with reflect.DeepEqual. A noop diff returns an empty (non-nil)
// slice.
func Diff(prev, next map[string]any, watched []string) []PatchOp {
	ops := make([]PatchOp, 0)
	for _, field := range watched {
		prevVal, prevOK := prev[field]
		nextVal, nextOK := next[field]

		switch {
		case !prevOK && !nextOK:
			// field absent from both — treat as no change.
		case !prevOK || prevVal == nil:
			if nextOK && nextVal != nil {
				ops = append(ops, PatchOp{Op: "add", Path: "/" + field, To: nextVal})
			}
		case !nextOK || nextVal == nil:
			ops = append(ops, PatchOp{Op: "remove", Path: "/" + field, From: prevVal})
		default:
			if !reflect.DeepEqual(prevVal, nextVal) {
				ops = append(ops, PatchOp{Op: "replace", Path: "/" + field, From: prevVal, To: nextVal})
			}
		}
	}
	return ops
}
