package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDetectWorkloadUnlinkMiddleware verifies the body-peek middleware sets the
// clear-flag only for an explicit `"application_id": null` on a workload PATCH,
// restores the body for the downstream decode, and leaves non-PATCH / non-null
// requests untouched (ADR-0029 §2.3).
func TestDetectWorkloadUnlinkMiddleware(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		path      string
		body      string
		wantClear bool
	}{
		{"explicit null PATCH", http.MethodPatch, "/v1/workloads/abc", `{"application_id":null}`, true},
		{"explicit null among fields", http.MethodPatch, "/v1/workloads/abc", `{"owner":"x","application_id":null}`, true},
		{"value not null", http.MethodPatch, "/v1/workloads/abc", `{"application_id":"11111111-1111-1111-1111-111111111111"}`, false},
		{"absent key", http.MethodPatch, "/v1/workloads/abc", `{"owner":"x"}`, false},
		{"empty object", http.MethodPatch, "/v1/workloads/abc", `{}`, false},
		{"wrong method", http.MethodPost, "/v1/workloads/abc", `{"application_id":null}`, false},
		{"collection not item", http.MethodPatch, "/v1/workloads/", `{"application_id":null}`, false},
		{"sub-resource", http.MethodPatch, "/v1/workloads/abc/history", `{"application_id":null}`, false},
		{"malformed json", http.MethodPatch, "/v1/workloads/abc", `{`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotClear bool
			var gotBody string
			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				gotClear = workloadClearApplicationFromCtx(r.Context())
				b, _ := io.ReadAll(r.Body)
				gotBody = string(b)
			})
			//nolint:noctx // test helper; context is not needed for in-process middleware tests
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/merge-patch+json")
			DetectWorkloadUnlinkMiddleware(next).ServeHTTP(httptest.NewRecorder(), req)

			if gotClear != tc.wantClear {
				t.Errorf("clear flag = %v, want %v", gotClear, tc.wantClear)
			}
			// The downstream handler must still see the full body.
			if gotBody != tc.body {
				t.Errorf("body not restored: got %q, want %q", gotBody, tc.body)
			}
		})
	}
}
