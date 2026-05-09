package registry

import "testing"

func TestMatchHostname(t *testing.T) {
	cases := []struct {
		pattern string
		host    string
		want    bool
	}{
		{"docker.io", "docker.io", true},
		{"docker.io", "ghcr.io", false},
		{"*-docker.pkg.dev", "europe-west1-docker.pkg.dev", true},
		{"*-docker.pkg.dev", "us-central1-docker.pkg.dev", true},
		{"*-docker.pkg.dev", "docker.pkg.dev", false}, // suffix only, requires non-empty leading
		{"*-docker.pkg.dev", "docker.io", false},
		{"*", "anything", true}, // wildcard-only matches anything non-empty
		{"*", "", false},
		{"", "x", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"|"+tc.host, func(t *testing.T) {
			got := Match(tc.pattern, tc.host)
			if got != tc.want {
				t.Errorf("Match(%q, %q): want %v, got %v", tc.pattern, tc.host, tc.want, got)
			}
		})
	}
}
