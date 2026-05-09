// Package registry contains the OCI-distribution registry client and the
// hostname allowlist matcher used by the imageversions enricher.
package registry

import "strings"

// Match reports whether host satisfies the given pattern.
// Patterns are either exact hostnames ("docker.io") or "*<suffix>" where
// the leading "*" matches any non-empty string. "*" alone matches anything
// non-empty.
func Match(pattern, host string) bool {
	if pattern == "" || host == "" {
		return false
	}
	if !strings.HasPrefix(pattern, "*") {
		return pattern == host
	}
	suffix := pattern[1:]
	if suffix == "" {
		return host != ""
	}
	if !strings.HasSuffix(host, suffix) {
		return false
	}
	return len(host) > len(suffix)
}
