//go:build integration

package imageversions_test

import (
	"testing"
)

// TestEnricher_Integration_FullCycle is a placeholder for a hermetic
// full-cycle integration test against a real Postgres instance and a
// fake registry server. The full integration is non-trivial because the
// OCI client constructs URLs via registry.EffectiveHost from a fixed
// hostname → URL mapping (Docker Hub maps to registry-1.docker.io). To
// make a hermetic test, EffectiveHost would need to be parametrizable,
// or the workload's image string would need a host that DNS-resolves
// to the test server.
//
// For V1, this gap is acceptable: the unit tests in enricher_test.go
// cover the loop's branching and concurrency; the live smoke tests in
// internal/imageversions/registry/live_test.go (Task 23) cover the
// real-registry path. Promote this scaffold to a proper test in V2 if
// the gap proves insufficient.
func TestEnricher_Integration_FullCycle(t *testing.T) {
	t.Skip("Hermetic full-cycle integration test is deferred. " +
		"Unit tests in enricher_test.go cover loop logic; live smoke in " +
		"registry/live_test.go (Task 23) covers real-registry behavior.")
}
