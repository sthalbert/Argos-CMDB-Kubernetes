package api

import (
	"strings"
	"testing"
	"time"
)

func TestSlugForFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"log4j:2.15", "log4j-2.15"},
		{"  NGINX:1.27  ", "nginx-1.27"},
		{"vault", "vault"},
		{"vault!!!", "vault"},
		{"", ""},
		{"---", ""},
		{"a/b/c", "a-b-c"},
		{strings.Repeat("x", 80), strings.Repeat("x", 40)},
	}
	for _, c := range cases {
		got := slugForFilename(c.in)
		if got != c.want {
			t.Errorf("slugForFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractTimestamp(t *testing.T) {
	tm := time.Date(2026, 5, 15, 14, 32, 7, 0, time.UTC)
	if got := extractTimestamp(tm); got != "2026-05-15T1432Z" {
		t.Fatalf("extractTimestamp = %q", got)
	}
}
