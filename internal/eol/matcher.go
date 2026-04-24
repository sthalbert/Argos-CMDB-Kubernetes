package eol

import (
	"regexp"
	"strings"
)

// Matcher extracts a product identifier and cycle version from a raw
// version string. Each matchable field has its own Matcher.
type Matcher interface {
	Match(raw string) (MatchResult, bool)
}

// --- Kubernetes version matcher ------------------------------------------

// KubernetesMatcher matches Kubernetes version strings like "v1.28.5" or
// "1.28.5" and extracts product "kubernetes", cycle "1.28".
type KubernetesMatcher struct{}

var kubeVersionRe = regexp.MustCompile(`^v?(\d+\.\d+)`)

// Match implements Matcher for Kubernetes versions.
func (KubernetesMatcher) Match(raw string) (MatchResult, bool) {
	m := kubeVersionRe.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return MatchResult{}, false
	}
	return MatchResult{Product: "kubernetes", Cycle: m[1]}, true
}

// --- Container runtime matcher -------------------------------------------

// ContainerRuntimeMatcher parses the node's container_runtime_version
// field, which has the form "containerd://1.7.2" or "cri-o://1.28.0".
type ContainerRuntimeMatcher struct{}

var runtimeRe = regexp.MustCompile(`^([\w-]+)://v?(\d+\.\d+)`)

// Match implements Matcher for container runtime version strings.
func (ContainerRuntimeMatcher) Match(raw string) (MatchResult, bool) {
	m := runtimeRe.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return MatchResult{}, false
	}
	product := strings.ToLower(m[1])
	cycle := m[2]
	return MatchResult{Product: product, Cycle: cycle}, true
}

// --- OS image matcher ----------------------------------------------------

// OSImageMatcher extracts a distro product and version from a free-text
// os_image string such as "Ubuntu 22.04.3 LTS" or "Debian GNU/Linux 12
// (bookworm)".
type OSImageMatcher struct{}

// Ordered list of patterns tried in sequence. First match wins.
var osPatterns = []struct {
	product string
	re      *regexp.Regexp
}{
	{"ubuntu", regexp.MustCompile(`(?i)ubuntu\s+(\d+\.\d+)`)},
	{"debian", regexp.MustCompile(`(?i)debian.*?(\d+)`)},
	{"alpine", regexp.MustCompile(`(?i)alpine.*?(\d+\.\d+)`)},
	{"rhel", regexp.MustCompile(`(?i)red\s*hat.*?(\d+)`)},
	{"rocky-linux", regexp.MustCompile(`(?i)rocky.*?(\d+)`)},
	{"alma-linux", regexp.MustCompile(`(?i)alma.*?(\d+)`)},
	{"amazon-linux", regexp.MustCompile(`(?i)amazon\s+linux\s+(\d+)`)},
	{"centos", regexp.MustCompile(`(?i)centos.*?(\d+)`)},
	{"fedora", regexp.MustCompile(`(?i)fedora.*?(\d+)`)},
	{"oracle-linux", regexp.MustCompile(`(?i)oracle.*?(\d+)`)},
	{"opensuse", regexp.MustCompile(`(?i)opensuse.*?(\d+\.\d+)`)},
	{"sles", regexp.MustCompile(`(?i)suse.*?enterprise.*?(\d+)`)},
	{"flatcar", regexp.MustCompile(`(?i)flatcar.*?(\d+)`)},
	{"cos", regexp.MustCompile(`(?i)container.optimized\s+os.*?(\d+)`)},
}

// Match implements Matcher for OS image strings.
func (OSImageMatcher) Match(raw string) (MatchResult, bool) {
	trimmed := strings.TrimSpace(raw)
	for _, p := range osPatterns {
		m := p.re.FindStringSubmatch(trimmed)
		if m != nil {
			return MatchResult{Product: p.product, Cycle: m[1]}, true
		}
	}
	return MatchResult{}, false
}

// --- Kernel version matcher ----------------------------------------------

// KernelMatcher extracts the major.minor from a Linux kernel version
// string such as "5.15.0-91-generic" → product "linux", cycle "5.15".
type KernelMatcher struct{}

var kernelRe = regexp.MustCompile(`^(\d+\.\d+)`)

// Match implements Matcher for Linux kernel version strings.
func (KernelMatcher) Match(raw string) (MatchResult, bool) {
	m := kernelRe.FindStringSubmatch(strings.TrimSpace(raw))
	if m == nil {
		return MatchResult{}, false
	}
	return MatchResult{Product: "linux", Cycle: m[1]}, true
}
