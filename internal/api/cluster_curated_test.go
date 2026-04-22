package api

// Handler-level coverage for the curated-metadata fields on clusters
// (owner / criticality / notes / runbook_url / annotations). The PG
// integration test in internal/store asserts the "collector doesn't
// clobber curated fields" invariant against real SQL; this file runs
// the same patch shapes through the HTTP surface so the OpenAPI
// binding + JSON marshalling + merge-patch handler wiring all agree.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestClusterCuratedRoundTrip is the canonical "can I set and read
// back every curated field" check. Lives in its own test so a
// regression in the JSON tags or OpenAPI schema surfaces with a clear
// message instead of being hidden in the big lifecycle test.
func TestClusterCuratedRoundTrip(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, newMemStore())

	createBody := `{
		"name": "prod-cluster",
		"owner": "team-platform",
		"criticality": "critical",
		"notes": "page on any outage",
		"runbook_url": "https://runbooks.example.com/prod",
		"annotations": {"compliance": "snc", "dc": "paris-a"}
	}`
	rr := do(h, http.MethodPost, "/v1/clusters", createBody)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%q", rr.Code, rr.Body.String())
	}
	var created Cluster
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Id == nil {
		t.Fatal("created.Id is nil")
	}

	assertCuratedFields(t, created, expectedCurated{
		owner:       "team-platform",
		criticality: "critical",
		notes:       "page on any outage",
		runbookURL:  "https://runbooks.example.com/prod",
		annotations: map[string]string{"compliance": "snc", "dc": "paris-a"},
	})

	// GET returns what we inserted.
	get := do(h, http.MethodGet, "/v1/clusters/"+created.Id.String(), "")
	if get.Code != http.StatusOK {
		t.Fatalf("get status=%d", get.Code)
	}
	var fetched Cluster
	if err := json.Unmarshal(get.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	assertCuratedFields(t, fetched, expectedCurated{
		owner:       "team-platform",
		criticality: "critical",
		notes:       "page on any outage",
		runbookURL:  "https://runbooks.example.com/prod",
		annotations: map[string]string{"compliance": "snc", "dc": "paris-a"},
	})
}

// TestClusterPatchPreservesCuratedFields runs every patch shape we
// expect to see in production and asserts which curated fields the
// patch should and should not touch. Table-driven so new patch shapes
// (e.g. new collector fields) are a one-line addition.
func TestClusterPatchPreservesCuratedFields(t *testing.T) { //nolint:gocyclo // table-driven test verifying many patch combinations
	t.Parallel()

	type want struct {
		owner       string
		criticality string
		notes       string
		runbookURL  string
		annotations map[string]string
		// For fields the patch should have updated.
		kubernetesVersion string
		provider          string
	}

	// Baseline curated payload applied before every patch variant.
	const seed = `{
		"name": "seed-cluster",
		"owner": "team-platform",
		"criticality": "critical",
		"notes": "prose",
		"runbook_url": "https://runbooks.example.com/x",
		"annotations": {"k": "v"}
	}`
	baseline := want{
		owner:       "team-platform",
		criticality: "critical",
		notes:       "prose",
		runbookURL:  "https://runbooks.example.com/x",
		annotations: map[string]string{"k": "v"},
	}

	tests := []struct {
		name  string
		patch string
		want  want
	}{
		{
			name:  "collector-style version-only patch leaves curated fields alone",
			patch: `{"kubernetes_version":"1.30.4"}`,
			want: func() want {
				w := baseline
				w.kubernetesVersion = "1.30.4"
				return w
			}(),
		},
		{
			name:  "editor updates owner only; criticality unchanged",
			patch: `{"owner":"team-sre"}`,
			want: func() want {
				w := baseline
				w.owner = "team-sre"
				return w
			}(),
		},
		{
			name:  "editor replaces annotations outright (merge-patch for JSONB)",
			patch: `{"annotations":{"compliance":"snc2"}}`,
			want: func() want {
				w := baseline
				w.annotations = map[string]string{"compliance": "snc2"}
				return w
			}(),
		},
		{
			name:  "clear owner by sending empty string",
			patch: `{"owner":""}`,
			want: func() want {
				w := baseline
				w.owner = ""
				return w
			}(),
		},
		{
			name:  "patch unrelated field does not drop curated data",
			patch: `{"provider":"gke"}`,
			want: func() want {
				w := baseline
				w.provider = "gke"
				return w
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newTestHandler(t, newMemStore())

			// Seed with the baseline curated payload.
			seeded := do(h, http.MethodPost, "/v1/clusters", seed)
			if seeded.Code != http.StatusCreated {
				t.Fatalf("seed create status=%d body=%q", seeded.Code, seeded.Body.String())
			}
			var created Cluster
			if err := json.Unmarshal(seeded.Body.Bytes(), &created); err != nil {
				t.Fatalf("decode seed: %v", err)
			}

			// Apply the patch.
			patchURL := "/v1/clusters/" + created.Id.String()
			patch := do(h, http.MethodPatch, patchURL, tt.patch)
			if patch.Code != http.StatusOK {
				t.Fatalf("patch status=%d body=%q", patch.Code, patch.Body.String())
			}
			var after Cluster
			if err := json.Unmarshal(patch.Body.Bytes(), &after); err != nil {
				t.Fatalf("decode patch: %v", err)
			}

			assertCuratedFields(t, after, expectedCurated{
				owner:       tt.want.owner,
				criticality: tt.want.criticality,
				notes:       tt.want.notes,
				runbookURL:  tt.want.runbookURL,
				annotations: tt.want.annotations,
			})
			if tt.want.kubernetesVersion != "" {
				if after.KubernetesVersion == nil || *after.KubernetesVersion != tt.want.kubernetesVersion {
					t.Errorf("kubernetes_version = %v, want %q", after.KubernetesVersion, tt.want.kubernetesVersion)
				}
			}
			if tt.want.provider != "" {
				if after.Provider == nil || *after.Provider != tt.want.provider {
					t.Errorf("provider = %v, want %q", after.Provider, tt.want.provider)
				}
			}
		})
	}
}

// --- helpers -----------------------------------------------------------

type expectedCurated struct {
	owner       string
	criticality string
	notes       string
	runbookURL  string
	annotations map[string]string
}

// assertCuratedFields diffs the curated-metadata payload on a Cluster
// against the expected snapshot. Empty-string wants mean "must be nil
// or empty" (the UI uses empty string to clear a field; the store
// translates that to SQL NULL on the way down but JSON round-trip
// may keep it as an empty-string pointer on the way up — either is
// acceptable here).
func assertCuratedFields(t *testing.T, c Cluster, w expectedCurated) { //nolint:gocritic // test helper, copy is fine
	t.Helper()
	if got := strVal(c.Owner); got != w.owner {
		t.Errorf("owner = %q, want %q", got, w.owner)
	}
	if got := strVal(c.Criticality); got != w.criticality {
		t.Errorf("criticality = %q, want %q", got, w.criticality)
	}
	if got := strVal(c.Notes); got != w.notes {
		t.Errorf("notes = %q, want %q", got, w.notes)
	}
	if got := strVal(c.RunbookUrl); got != w.runbookURL {
		t.Errorf("runbook_url = %q, want %q", got, w.runbookURL)
	}
	got := map[string]string(nil)
	if c.Annotations != nil {
		got = *c.Annotations
	}
	if !annotationsEqual(got, w.annotations) {
		t.Errorf("annotations = %v, want %v", got, w.annotations)
	}
}

func strVal(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func annotationsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Sanity check on the helper itself — exercising the assertion path
// keeps a regression in strVal/annotationsEqual from hiding behind
// the main test assertions.
func TestAnnotationsEqual(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a, b map[string]string
		want bool
	}{
		{"both nil", nil, nil, true},
		{"empty vs nil", map[string]string{}, nil, true},
		{"same content", map[string]string{"k": "v"}, map[string]string{"k": "v"}, true},
		{"diff length", map[string]string{"k": "v"}, map[string]string{"k": "v", "x": "y"}, false},
		{"diff value", map[string]string{"k": "v"}, map[string]string{"k": "w"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := annotationsEqual(c.a, c.b); got != c.want {
				t.Errorf("annotationsEqual(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

// Ensure the new create-with-curated payload surfaces a validation-free
// path (no 4xx for optional fields being present). Guard against a
// future OpenAPI tightening that accidentally rejects curated input.
func TestCreateClusterAcceptsCuratedFields(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, newMemStore())
	body := fmt.Sprintf(`{"name":"c-%d","owner":"o","criticality":"high","annotations":{"a":"b"}}`, 1)
	rr := do(h, http.MethodPost, "/v1/clusters", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"owner":"o"`) {
		t.Errorf("create response missing owner: %s", rr.Body.String())
	}
}
