package api

// EnrichContainersVersions joins each container's image string against the
// image_versions store and returns the populated map. Containers whose image
// is not enriched (non-parseable, registry outside allowlist, not yet
// processed) are absent from the map. Returns nil when no containers are
// enriched so that the json:"omitempty" tag on callers suppresses the field.
//
// Parsing helpers are inlined here to avoid an import cycle:
// internal/imageversions imports internal/api (via types.go), so
// internal/api cannot import internal/imageversions.
// The same external libraries (github.com/distribution/reference,
// golang.org/x/mod/semver) used by imageversions/parse.go are used directly.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/distribution/reference"
	"golang.org/x/mod/semver"
)

// containerRef is the canonical form of a parsed image reference.
type containerRef struct {
	imageRepo string // e.g. "docker.io/library/nginx"
	tag       string // e.g. "1.25.3-alpine"
}

// containerParsedTag captures the semver version and optional variant from a tag.
type containerParsedTag struct {
	version containerVersion
	variant string
}

// containerVersion wraps the semver canonical form.
type containerVersion struct {
	raw string // "vX.Y.Z"
}

// gt reports whether v is strictly greater than other.
func (v containerVersion) gt(other containerVersion) bool {
	return semver.Compare(v.raw, other.raw) > 0
}

var (
	semverPrefixRe = regexp.MustCompile(`^v?(\d+(?:\.\d+){0,2})`)

	prereleaseStarts = []string{"alpha", "beta", "rc", "pre", "dev", "snapshot", "nightly"}
)

const maxSemverMajor = 1000

// errSkip signals that a ref or tag cannot be enriched.
var errSkip = errors.New("skip")

func parseContainerRef(s string) (containerRef, error) {
	if s == "" {
		return containerRef{}, fmt.Errorf("%w: empty image string", errSkip)
	}
	named, err := reference.ParseNormalizedNamed(s)
	if err != nil {
		if at := strings.Index(s, "@"); at != -1 {
			named, err = reference.ParseNormalizedNamed(s[:at])
			if err != nil {
				return containerRef{}, fmt.Errorf("%w: %v", errSkip, err)
			}
		} else {
			return containerRef{}, fmt.Errorf("%w: %v", errSkip, err)
		}
	}
	tag := ""
	if t, ok := named.(reference.Tagged); ok {
		tag = t.Tag()
	}
	if tag == "" || tag == "latest" {
		return containerRef{}, fmt.Errorf("%w: no usable tag (%q)", errSkip, tag)
	}
	return containerRef{imageRepo: named.Name(), tag: tag}, nil
}

func parseContainerTag(s string) (containerParsedTag, error) {
	canonical, rest, err := extractCanonicalSemver(s)
	if err != nil {
		return containerParsedTag{}, err
	}
	pt := containerParsedTag{version: containerVersion{raw: canonical}}
	if rest == "" {
		return pt, nil
	}
	variant, err := classifyTagSuffix(s, rest)
	if err != nil {
		return containerParsedTag{}, err
	}
	pt.variant = variant
	return pt, nil
}

// extractCanonicalSemver parses the leading semver-shaped numeric prefix of s
// and returns its canonical "vX.Y.Z" form along with the unparsed remainder.
// Returns errSkip on empty input, no-prefix, implausible major, or invalid semver.
func extractCanonicalSemver(s string) (canonical, rest string, err error) {
	if s == "" {
		return "", "", fmt.Errorf("%w: empty tag", errSkip)
	}
	m := semverPrefixRe.FindStringSubmatchIndex(s)
	if m == nil {
		return "", "", fmt.Errorf("%w: no semver prefix in %q", errSkip, s)
	}
	versionStr := s[m[2]:m[3]]
	rest = s[m[1]:]
	parts := strings.Split(versionStr, ".")
	major, atoiErr := strconv.Atoi(parts[0])
	if atoiErr != nil || major >= maxSemverMajor {
		return "", "", fmt.Errorf("%w: implausible major in %q", errSkip, s)
	}
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	canonical = "v" + strings.Join(parts, ".")
	if !semver.IsValid(canonical) {
		return "", "", fmt.Errorf("%w: invalid semver in %q", errSkip, s)
	}
	return canonical, rest, nil
}

// classifyTagSuffix interprets the post-semver suffix. A leading "-" is
// stripped; anything else (e.g., "1.2.3foo") is rejected. Returns the
// variant string or errSkip if the suffix indicates a prerelease.
func classifyTagSuffix(original, rest string) (variant string, err error) {
	suffix := strings.TrimPrefix(rest, "-")
	if suffix == rest {
		return "", fmt.Errorf("%w: unexpected suffix shape %q", errSkip, rest)
	}
	lower := strings.ToLower(suffix)
	for _, p := range prereleaseStarts {
		if strings.HasPrefix(lower, p) {
			return "", fmt.Errorf("%w: prerelease tag %q", errSkip, original)
		}
	}
	return suffix, nil
}

// EnrichContainersVersions joins each container's image string against the
// image_versions store.
func EnrichContainersVersions(ctx context.Context, s Store, containers []map[string]any) ContainersVersions {
	out := ContainersVersions{}
	for _, c := range containers {
		name, _ := c["name"].(string)
		img, _ := c["image"].(string)
		if name == "" || img == "" {
			continue
		}
		if info, ok := lookupContainerVersion(ctx, s, img); ok {
			out[name] = info
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// lookupContainerVersion parses one image string and looks up the matching
// (image_repo, variant) row in image_versions. Returns the populated
// ContainerVersionInfo on hit, or (zero, false) when the image cannot be
// enriched (non-parseable, store error, no matching variant, or no LatestTag).
func lookupContainerVersion(ctx context.Context, s Store, img string) (ContainerVersionInfo, bool) {
	ref, err := parseContainerRef(img)
	if err != nil {
		return ContainerVersionInfo{}, false
	}
	cur, err := parseContainerTag(ref.tag)
	if err != nil {
		return ContainerVersionInfo{}, false
	}
	rows, err := s.GetImageVersionsByRepo(ctx, ref.imageRepo)
	if err != nil {
		return ContainerVersionInfo{}, false
	}
	for i := range rows {
		row := &rows[i]
		if row.Variant != cur.variant || row.LatestTag == nil {
			continue
		}
		latest, err := parseContainerTag(*row.LatestTag)
		if err != nil {
			continue
		}
		isBehind := latest.version.gt(cur.version)
		return ContainerVersionInfo{
			LatestTag:     row.LatestTag,
			IsBehind:      &isBehind,
			LastCheckedAt: &row.LastCheckedAt,
		}, true
	}
	return ContainerVersionInfo{}, false
}
