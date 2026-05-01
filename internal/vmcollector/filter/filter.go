// Package filter implements the cheap pre-filter that drops VMs the
// longue-vue-vm-collector should never push to longue-vue (ADR-0015 §8).
//
// Three drop conditions:
//   - any tag matching OscK8sClusterID/* (Outscale CCM cluster ownership)
//   - any tag matching OscK8sNodeName=*  (Outscale CCM node ownership)
//   - any tag longue-vue.io/ignore=true   (operator escape hatch)
//
// Server-side dedup against nodes.provider_id is the source of truth;
// this filter is a performance optimisation to avoid the HTTP round-trip
// per Kubernetes worker.
package filter

import (
	"strings"

	"github.com/sthalbert/longue-vue/internal/vmcollector/provider"
)

const (
	cccmTagPrefix   = "OscK8sClusterID/"
	cccmNodeNameKey = "OscK8sNodeName"
	lvIgnoreKey     = "longue-vue.io/ignore"
	lvIgnoreOnVal   = "true"
)

// Apply returns the subset of vms that should be pushed to longue-vue.
// The input slice is not modified; a fresh slice is allocated.
func Apply(vms []provider.VM) []provider.VM {
	out := make([]provider.VM, 0, len(vms))
	for i := range vms {
		if shouldDrop(vms[i].Tags) {
			continue
		}
		out = append(out, vms[i])
	}
	return out
}

// shouldDrop reports whether a tag map carries any of the three drop
// markers from ADR-0015 §8.
func shouldDrop(tags map[string]string) bool {
	if v, ok := tags[lvIgnoreKey]; ok && v == lvIgnoreOnVal {
		return true
	}
	if _, ok := tags[cccmNodeNameKey]; ok {
		return true
	}
	for k := range tags {
		if strings.HasPrefix(k, cccmTagPrefix) {
			return true
		}
	}
	return false
}
