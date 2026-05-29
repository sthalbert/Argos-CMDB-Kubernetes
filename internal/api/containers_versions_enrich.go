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

// minorDistanceStatus maps the minor-version distance between the deployed
// tag (cur) and the latest registry tag (latest) onto the traffic-light EOL
// status (ADR-0032). Patch differences are ignored; any major gap is "eol".
func minorDistanceStatus(cur, latest containerVersion) ContainerVersionInfoEolStatus {
	if !latest.gt(cur) {
		return ContainerVersionInfoEolStatusSupported
	}
	if semver.Major(latest.raw) != semver.Major(cur.raw) {
		return ContainerVersionInfoEolStatusEol
	}
	switch latest.minor() - cur.minor() {
	case 0:
		return ContainerVersionInfoEolStatusSupported
	case 1:
		return ContainerVersionInfoEolStatusApproachingEol
	default:
		return ContainerVersionInfoEolStatusEol
	}
}

// minor returns the numeric minor component of the canonical "vX.Y.Z" form.
func (v containerVersion) minor() int {
	mm := semver.MajorMinor(v.raw) // "vX.Y"
	dot := strings.LastIndex(mm, ".")
	if dot < 0 {
		return 0
	}
	n, _ := strconv.Atoi(mm[dot+1:])
	return n
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

// containerVersionLookup is the minimal store surface the container-version
// enrichment needs (mirror-origin resolution + the image_versions rows).
type containerVersionLookup interface {
	GetImageOriginResolution(ctx context.Context, mirrorImageRepo, variant string) (ImageOriginResolution, error)
	GetImageVersionsByRepo(ctx context.Context, imageRepo string) ([]ImageVersionRow, error)
}

// EnrichContainersVersions joins each container's image string against the
// image_versions store.
func EnrichContainersVersions(ctx context.Context, s containerVersionLookup, containers []map[string]any) ContainersVersions {
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

// lookupContainerVersion looks up a container image's version info using
// the three-branch logic:
//
//  1. The image is known to be a mirror with a successful resolution →
//     return version info computed against the resolved origin row,
//     plus origin_image_repo and origin_status="resolved".
//  2. The image is known to be a mirror with a failed resolution →
//     return origin_status="unresolved" with origin_error set, no badge.
//  3. The image isn't in the resolutions table → fall through to direct
//     lookup against image_versions (today's passthrough behavior).
func lookupContainerVersion(ctx context.Context, s containerVersionLookup, img string) (ContainerVersionInfo, bool) {
	ref, err := parseContainerRef(img)
	if err != nil {
		return ContainerVersionInfo{}, false
	}
	cur, err := parseContainerTag(ref.tag)
	if err != nil {
		return ContainerVersionInfo{}, false
	}

	resolution, err := s.GetImageOriginResolution(ctx, ref.imageRepo, cur.variant)
	switch {
	case errors.Is(err, ErrNotFound):
		// Passthrough — fall through to direct image_versions lookup.
	case err != nil:
		return ContainerVersionInfo{}, false
	case resolution.OriginImageRepo == nil:
		// Failure row — surface the unresolved branch.
		unresolved := Unresolved
		errMsg := ""
		if resolution.LastError != nil {
			errMsg = *resolution.LastError
		}
		return ContainerVersionInfo{
			OriginStatus: &unresolved,
			OriginError:  &errMsg,
		}, true
	default:
		// Resolved — look up the origin row in image_versions.
		info, ok := lookupVersionRow(ctx, s, *resolution.OriginImageRepo, cur)
		resolved := Resolved
		info.OriginStatus = &resolved
		info.OriginImageRepo = resolution.OriginImageRepo
		// ok==false here means we have a successful resolution but no
		// origin row yet (next enricher tick will fill it). Surface the
		// origin info anyway so the UI can show it, just without a badge.
		_ = ok
		return info, true
	}

	// Passthrough lookup.
	return lookupVersionRow(ctx, s, ref.imageRepo, cur)
}

// lookupVersionRow finds the matching variant row in image_versions and
// returns a populated ContainerVersionInfo (without origin fields).
// Returns (zero, false) when no row matches or the row has no usable
// latest_tag.
func lookupVersionRow(ctx context.Context, s containerVersionLookup, imageRepo string, cur containerParsedTag) (ContainerVersionInfo, bool) {
	rows, err := s.GetImageVersionsByRepo(ctx, imageRepo)
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
		status := minorDistanceStatus(cur.version, latest.version)
		return ContainerVersionInfo{
			LatestTag:     row.LatestTag,
			IsBehind:      &isBehind,
			LastCheckedAt: &row.LastCheckedAt,
			EolStatus:     &status,
		}, true
	}
	return ContainerVersionInfo{}, false
}
