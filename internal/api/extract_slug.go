package api

import (
	"strings"
	"time"
)

const slugMaxLen = 40

// slugForFilename turns a user-supplied search query into a
// filesystem-safe token: trimmed, lowercased, every character outside
// [a-z0-9.-] replaced with a hyphen, runs of hyphens collapsed, leading
// and trailing hyphens stripped, length capped at slugMaxLen.
// Empty / all-special-character inputs return "".
//
//nolint:gocyclo // character-class switch + run-collapse + trim is the only clean implementation; each case is one line
func slugForFilename(q string) string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return ""
	}
	var b strings.Builder
	prevHyphen := false
	for _, r := range q {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.':
			b.WriteRune(r)
			prevHyphen = false
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > slugMaxLen {
		out = strings.TrimRight(out[:slugMaxLen], "-")
	}
	return out
}

// extractTimestamp formats t for embedding in a download filename:
// YYYY-MM-DDTHHMMZ. Colons are avoided (Windows-hostile) and seconds
// are dropped (irrelevant at extract granularity).
func extractTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T1504Z")
}
