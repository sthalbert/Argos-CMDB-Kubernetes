package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRunbookURLValidation verifies that the server rejects runbook_url
// values with non-HTTP schemes (e.g. javascript:, data:) and accepts
// valid http/https URLs across all create and update endpoints for
// clusters, namespaces, and nodes.
func TestRunbookURLValidation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		url    string
		reject bool
	}{
		{"https", "https://runbooks.example.com/prod", false},
		{"http", "http://runbooks.internal/page", false},
		{"empty string", "", false},
		{"javascript scheme", "javascript:alert(document.cookie)", true},
		{"data scheme", "data:text/html,<script>alert(1)</script>", true},
		{"ftp scheme", "ftp://files.example.com/runbook.pdf", true},
		{"no scheme", "runbooks.example.com/prod", true},
	}

	t.Run("CreateCluster", func(t *testing.T) {
		t.Parallel()
		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				h := newTestHandler(t, newMemStore())
				body := fmt.Sprintf(`{"name":"c-%d","runbook_url":%q}`, i, tc.url)
				if tc.url == "" {
					body = fmt.Sprintf(`{"name":"c-%d"}`, i)
				}
				rr := do(h, http.MethodPost, "/v1/clusters", body)
				assertRunbookStatus(t, rr, tc.reject, http.StatusCreated)
			})
		}
	})

	t.Run("UpdateCluster", func(t *testing.T) {
		t.Parallel()
		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				h := newTestHandler(t, newMemStore())

				id := seedCluster(t, h, fmt.Sprintf("uc-%d", i))
				patch := fmt.Sprintf(`{"runbook_url":%q}`, tc.url)
				rr := do(h, http.MethodPatch, "/v1/clusters/"+id, patch)
				assertRunbookStatus(t, rr, tc.reject, http.StatusOK)
			})
		}
	})

	t.Run("CreateNamespace", func(t *testing.T) {
		t.Parallel()
		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				h := newTestHandler(t, newMemStore())

				clusterID := seedCluster(t, h, fmt.Sprintf("cn-cl-%d", i))
				body := fmt.Sprintf(`{"name":"ns-%d","cluster_id":%q,"runbook_url":%q}`, i, clusterID, tc.url)
				if tc.url == "" {
					body = fmt.Sprintf(`{"name":"ns-%d","cluster_id":%q}`, i, clusterID)
				}
				rr := do(h, http.MethodPost, "/v1/namespaces", body)
				assertRunbookStatus(t, rr, tc.reject, http.StatusCreated)
			})
		}
	})

	t.Run("UpdateNamespace", func(t *testing.T) {
		t.Parallel()
		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				h := newTestHandler(t, newMemStore())

				clusterID := seedCluster(t, h, fmt.Sprintf("un-cl-%d", i))
				nsID := seedNamespace(t, h, clusterID, fmt.Sprintf("uns-%d", i))
				patch := fmt.Sprintf(`{"runbook_url":%q}`, tc.url)
				rr := do(h, http.MethodPatch, "/v1/namespaces/"+nsID, patch)
				assertRunbookStatus(t, rr, tc.reject, http.StatusOK)
			})
		}
	})

	t.Run("CreateNode", func(t *testing.T) {
		t.Parallel()
		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				h := newTestHandler(t, newMemStore())

				clusterID := seedCluster(t, h, fmt.Sprintf("cno-cl-%d", i))
				body := fmt.Sprintf(`{"name":"node-%d","cluster_id":%q,"runbook_url":%q}`, i, clusterID, tc.url)
				if tc.url == "" {
					body = fmt.Sprintf(`{"name":"node-%d","cluster_id":%q}`, i, clusterID)
				}
				rr := do(h, http.MethodPost, "/v1/nodes", body)
				assertRunbookStatus(t, rr, tc.reject, http.StatusCreated)
			})
		}
	})

	t.Run("UpdateNode", func(t *testing.T) {
		t.Parallel()
		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				h := newTestHandler(t, newMemStore())

				clusterID := seedCluster(t, h, fmt.Sprintf("uno-cl-%d", i))
				nodeID := seedNode(t, h, clusterID, fmt.Sprintf("unode-%d", i))
				patch := fmt.Sprintf(`{"runbook_url":%q}`, tc.url)
				rr := do(h, http.MethodPatch, "/v1/nodes/"+nodeID, patch)
				assertRunbookStatus(t, rr, tc.reject, http.StatusOK)
			})
		}
	})
}

// TestValidateRunbookURL is a unit test for the validation function itself.
func TestValidateRunbookURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		input   *string
		wantErr bool
	}{
		{"nil pointer", nil, false},
		{"empty string", ptr(""), false},
		{"https url", ptr("https://example.com/runbook"), false},
		{"http url", ptr("http://internal.corp/page"), false},
		{"javascript", ptr("javascript:alert(1)"), true},
		{"data uri", ptr("data:text/html,<h1>hi</h1>"), true},
		{"ftp", ptr("ftp://example.com/file"), true},
		{"mailto", ptr("mailto:admin@example.com"), true},
		{"bare path", ptr("runbooks.example.com/prod"), true},
		{"HTTPS uppercase", ptr("HTTPS://EXAMPLE.COM"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateRunbookURL(tc.input)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for %v, got nil", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error for %v: %v", tc.input, err)
			}
		})
	}
}

// --- helpers ---------------------------------------------------------------

func ptr(s string) *string { return &s }

func seedCluster(t *testing.T, h http.Handler, name string) string {
	t.Helper()
	rr := do(h, http.MethodPost, "/v1/clusters", fmt.Sprintf(`{"name":%q}`, name))
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed cluster %q: status=%d body=%q", name, rr.Code, rr.Body.String())
	}
	var c Cluster
	if err := json.Unmarshal(rr.Body.Bytes(), &c); err != nil {
		t.Fatalf("decode cluster: %v", err)
	}
	return c.Id.String()
}

func seedNamespace(t *testing.T, h http.Handler, clusterID, name string) string {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"cluster_id":%q}`, name, clusterID)
	rr := do(h, http.MethodPost, "/v1/namespaces", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed namespace %q: status=%d body=%q", name, rr.Code, rr.Body.String())
	}
	var ns Namespace
	if err := json.Unmarshal(rr.Body.Bytes(), &ns); err != nil {
		t.Fatalf("decode namespace: %v", err)
	}
	return ns.Id.String()
}

func seedNode(t *testing.T, h http.Handler, clusterID, name string) string {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"cluster_id":%q}`, name, clusterID)
	rr := do(h, http.MethodPost, "/v1/nodes", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed node %q: status=%d body=%q", name, rr.Code, rr.Body.String())
	}
	var n Node
	if err := json.Unmarshal(rr.Body.Bytes(), &n); err != nil {
		t.Fatalf("decode node: %v", err)
	}
	return n.Id.String()
}

func assertRunbookStatus(t *testing.T, rr *httptest.ResponseRecorder, reject bool, successCode int) {
	t.Helper()
	if reject {
		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d body=%q", rr.Code, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), "runbook_url") {
			t.Errorf("error body should mention runbook_url: %s", rr.Body.String())
		}
	} else if rr.Code != successCode {
		t.Errorf("expected %d, got %d body=%q", successCode, rr.Code, rr.Body.String())
	}
}
