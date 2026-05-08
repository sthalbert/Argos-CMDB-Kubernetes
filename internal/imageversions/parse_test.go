package imageversions

import (
	"errors"
	"testing"
)

func TestParseImageRef(t *testing.T) {
	// Standard valid digest: 64 lowercase hex characters after sha256:.
	const validDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	// Deliberately broken digest (62 hex chars). Real Kubernetes image refs
	// occasionally carry truncated/non-standard digests; the parser must
	// fall back to the tag.
	const brokenDigest = "sha256:abcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcabcab"

	tests := []struct {
		in       string
		wantRepo string
		wantTag  string
		wantSkip bool
	}{
		{"nginx", "docker.io/library/nginx", "", true},                                     // implicit :latest -> skip
		{"nginx:latest", "docker.io/library/nginx", "", true},                              // explicit :latest -> skip
		{"nginx:1.25.3", "docker.io/library/nginx", "1.25.3", false},                       // basic semver
		{"library/nginx:1.25.3-alpine", "docker.io/library/nginx", "1.25.3-alpine", false}, // variant
		{"docker.io/library/nginx:1.27.0", "docker.io/library/nginx", "1.27.0", false},     // fully qualified
		{"quay.io/prometheus/prometheus:v2.45.0", "quay.io/prometheus/prometheus", "v2.45.0", false},
		{"ghcr.io/foo/bar:1.0.0", "ghcr.io/foo/bar", "1.0.0", false},
		{"nginx@" + validDigest, "", "", true},                                   // digest-only (valid digest) -> skip
		{"nginx:1.25@" + validDigest, "docker.io/library/nginx", "1.25", false},  // tag+digest valid -> primary path
		{"nginx:1.25@" + brokenDigest, "docker.io/library/nginx", "1.25", false}, // tag+digest broken -> fallback path strips digest
		{"", "", "", true},            // empty
		{"@@invalid@@", "", "", true}, // garbage
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			ref, err := ParseImageRef(tc.in)
			if tc.wantSkip {
				if err == nil {
					t.Fatalf("expected skip/error, got %+v", ref)
				}
				if !errors.Is(err, ErrSkip) {
					t.Errorf("error not wrapping ErrSkip: %v", err)
				}
				if ref != (Ref{}) {
					t.Errorf("expected zero Ref on skip, got %+v", ref)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ref.ImageRepo != tc.wantRepo {
				t.Errorf("repo: want %q, got %q", tc.wantRepo, ref.ImageRepo)
			}
			if ref.Tag != tc.wantTag {
				t.Errorf("tag: want %q, got %q", tc.wantTag, ref.Tag)
			}
		})
	}
}

func TestParseTag(t *testing.T) {
	tests := []struct {
		in          string
		wantVersion string // semver-ish prefix as captured (without v prefix)
		wantVariant string
		wantPre     bool
		wantSkip    bool
	}{
		{"1.25.3", "1.25.3", "", false, false},
		{"v1.25.3", "1.25.3", "", false, false},
		{"1.25.3-alpine", "1.25.3", "alpine", false, false},
		{"1.25.3-alpine3.18", "1.25.3", "alpine3.18", false, false},
		{"1.25.3-debian-12", "1.25.3", "debian-12", false, false},
		{"1.25.3-rc1", "1.25.3", "", true, false},
		{"1.25.3-beta", "1.25.3", "", true, false},
		{"1.25.3-alpha.2", "1.25.3", "", true, false},
		{"1.25.3-rc1-alpine", "", "", false, true}, // ambiguous -> skip
		{"latest", "", "", false, true},
		{"master", "", "", false, true},
		{"main", "", "", false, true},
		{"sha-abc123", "", "", false, true},
		{"2024.01.15", "", "", false, true},
		{"", "", "", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			p, err := ParseTag(tc.in)
			if tc.wantSkip {
				if err == nil {
					t.Fatalf("expected skip, got %+v", p)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Version.String() != tc.wantVersion {
				t.Errorf("version: want %q, got %q", tc.wantVersion, p.Version.String())
			}
			if p.Variant != tc.wantVariant {
				t.Errorf("variant: want %q, got %q", tc.wantVariant, p.Variant)
			}
			if p.IsPrerelease != tc.wantPre {
				t.Errorf("prerelease: want %v, got %v", tc.wantPre, p.IsPrerelease)
			}
		})
	}
}
