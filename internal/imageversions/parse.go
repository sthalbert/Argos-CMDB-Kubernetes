// Package imageversions enriches container images used in Kubernetes clusters
// with the latest available tag from their source registry.
package imageversions

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/distribution/reference"
	"golang.org/x/mod/semver"
)

// ErrSkip is returned when an image ref or tag should not produce an
// image_versions row (e.g., :latest, digest-only, non-semver tags).
var ErrSkip = errors.New("imageversions: skip")

// Ref is the canonical, parsed form of an image reference.
type Ref struct {
	ImageRepo string // e.g. "docker.io/library/nginx" (always fully qualified)
	Registry  string // e.g. "docker.io"
	Tag       string // e.g. "1.25.3-alpine"
}

// ParseImageRef parses a raw image string into a canonical Ref.
// Returns ErrSkip if the image cannot be enriched (no tag, digest-only,
// invalid format, etc.).
//
// When the input combines a tag and a digest (e.g. "nginx:1.25@sha256:..."),
// the tag is extracted even if the digest is syntactically malformed — the
// digest is used only for content-addressable pulls and carries no version
// information for enrichment purposes.
func ParseImageRef(s string) (Ref, error) {
	if s == "" {
		return Ref{}, fmt.Errorf("%w: empty image string", ErrSkip)
	}
	named, err := reference.ParseNormalizedNamed(s)
	if err != nil {
		// If parsing failed and there is both a tag and a digest in the raw
		// string, strip the digest and retry.  Real Kubernetes image fields
		// occasionally carry a truncated or otherwise non-standard digest
		// alongside the tag; the tag is the only thing we care about here.
		if at := strings.Index(s, "@"); at != -1 {
			noDigest := s[:at]
			named, err = reference.ParseNormalizedNamed(noDigest)
			if err != nil {
				return Ref{}, fmt.Errorf("%w: %v", ErrSkip, err)
			}
		} else {
			return Ref{}, fmt.Errorf("%w: %v", ErrSkip, err)
		}
	}
	full := named.Name() // canonical: "docker.io/library/nginx"
	registry := reference.Domain(named)
	tag := ""
	if t, ok := named.(reference.Tagged); ok {
		tag = t.Tag()
	}
	if tag == "" || tag == "latest" {
		return Ref{ImageRepo: full, Registry: registry}, fmt.Errorf("%w: no usable tag (%q)", ErrSkip, tag)
	}
	return Ref{ImageRepo: full, Registry: registry, Tag: tag}, nil
}

// ParsedTag captures the semver prefix, optional variant suffix, and
// whether the tag is a prerelease.
type ParsedTag struct {
	Original     string
	Version      Version
	Variant      string
	IsPrerelease bool
}

// Version is a thin wrapper around the golang.org/x/mod/semver canonical
// form (e.g., "v1.25.3"). Use GT to compare; String returns the dotted
// numeric form without a v prefix.
type Version struct {
	raw string // canonical "vX.Y.Z" form
}

// String returns the version without the leading "v".
func (v Version) String() string {
	return strings.TrimPrefix(v.raw, "v")
}

// GT reports whether v is strictly greater than other.
func (v Version) GT(other Version) bool {
	return semver.Compare(v.raw, other.raw) > 0
}

// Canonical returns the semver-canonical form ("vX.Y.Z") for use with
// the semver package.
func (v Version) Canonical() string {
	return v.raw
}

var (
	semverPrefixRe   = regexp.MustCompile(`^v?(\d+(?:\.\d+){0,2})`)
	prereleaseStarts = []string{"alpha", "beta", "rc", "pre", "dev", "snapshot", "nightly"}
)

// maxSemverMajor is a heuristic to reject date-shaped tags like "2024.01.15".
// No real container image has a major version anywhere near this; date-based
// tags consistently use a 4-digit year as the leading component.
const maxSemverMajor = 1000

// ParseTag classifies a tag into (version, variant, prerelease).
// Returns ErrSkip when the tag does not begin with a semver-shaped number,
// when it ambiguously combines a prerelease and a variant suffix, or when
// the tag looks like a date (major >= maxSemverMajor).
func ParseTag(s string) (ParsedTag, error) {
	if s == "" {
		return ParsedTag{}, fmt.Errorf("%w: empty tag", ErrSkip)
	}
	m := semverPrefixRe.FindStringSubmatchIndex(s)
	if m == nil {
		return ParsedTag{}, fmt.Errorf("%w: no semver prefix in %q", ErrSkip, s)
	}
	versionStr := s[m[2]:m[3]]
	rest := s[m[1]:]

	// Reject date-shaped tags (e.g. "2024.01.15") via a major-version sanity bound.
	parts := strings.Split(versionStr, ".")
	major, err := strconv.Atoi(parts[0])
	if err != nil || major >= maxSemverMajor {
		return ParsedTag{}, fmt.Errorf("%w: implausible major version in %q", ErrSkip, s)
	}

	// Pad to 3 components so semver lib accepts it.
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	canonical := "v" + strings.Join(parts, ".")
	if !semver.IsValid(canonical) {
		return ParsedTag{}, fmt.Errorf("%w: not a valid semver: %q", ErrSkip, canonical)
	}

	pt := ParsedTag{
		Original: s,
		Version:  Version{raw: canonical},
	}

	if rest == "" {
		return pt, nil
	}
	// Trim a leading separator dash. If there's no dash, it means the tag
	// has unexpected trailing content (e.g., "1.2.3foo") that we don't model.
	suffix := strings.TrimPrefix(rest, "-")
	if suffix == rest {
		return ParsedTag{}, fmt.Errorf("%w: unexpected suffix shape %q", ErrSkip, rest)
	}

	// Detect prerelease marker.
	isPre := false
	lower := strings.ToLower(suffix)
	for _, p := range prereleaseStarts {
		if strings.HasPrefix(lower, p) {
			isPre = true
			break
		}
	}

	if isPre {
		// Reject mixed "rc1-alpine" style as ambiguous.
		if strings.Contains(suffix, "-") {
			return ParsedTag{}, fmt.Errorf("%w: ambiguous prerelease+variant: %q", ErrSkip, s)
		}
		pt.IsPrerelease = true
		return pt, nil
	}

	pt.Variant = suffix
	return pt, nil
}
