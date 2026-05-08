package imageversions

import "testing"

func TestParseImageRef(t *testing.T) {
	tests := []struct {
		in       string
		wantRepo string
		wantTag  string
		wantSkip bool
	}{
		{"nginx", "docker.io/library/nginx", "", true},                    // implicit :latest -> skip
		{"nginx:latest", "docker.io/library/nginx", "", true},             // explicit :latest -> skip
		{"nginx:1.25.3", "docker.io/library/nginx", "1.25.3", false},
		{"library/nginx:1.25.3-alpine", "docker.io/library/nginx", "1.25.3-alpine", false},
		{"docker.io/library/nginx:1.27.0", "docker.io/library/nginx", "1.27.0", false},
		{"quay.io/prometheus/prometheus:v2.45.0", "quay.io/prometheus/prometheus", "v2.45.0", false},
		{"ghcr.io/foo/bar:1.0.0", "ghcr.io/foo/bar", "1.0.0", false},
		{"nginx@sha256:abc123def4567890abc123def4567890abc123def4567890abc123def4567890ab", "", "", true}, // digest only -> skip
		{"nginx:1.25@sha256:abc123def4567890abc123def4567890abc123def4567890abc123def4567890ab", "docker.io/library/nginx", "1.25", false},
		{"", "", "", true},
		{"@@invalid@@", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			ref, err := ParseImageRef(tc.in)
			if tc.wantSkip {
				if err == nil {
					t.Fatalf("expected skip/error, got %+v", ref)
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
		in           string
		wantVersion  string // semver-ish prefix as captured (without v prefix)
		wantVariant  string
		wantPre      bool
		wantSkip     bool
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
