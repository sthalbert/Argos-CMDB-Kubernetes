package imageversions

import "sort"

// ComputeLatest returns the highest non-prerelease tag whose variant matches.
// The variant argument must already be the parsed variant string (use
// ParseTag on a current tag to derive it). Pass "" for pure semver tags.
//
// Returns "" with a nil error when no tag in allTags matches the variant
// family. Parse errors on individual tags are ignored — they are silently
// skipped and do not cause the whole call to fail.
func ComputeLatest(variant string, allTags []string) (string, error) {
	var candidates []ParsedTag
	for _, t := range allTags {
		p, err := ParseTag(t)
		if err != nil || p.IsPrerelease || p.Variant != variant {
			continue
		}
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		return "", nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Version.GT(candidates[j].Version)
	})
	return candidates[0].Original, nil
}
