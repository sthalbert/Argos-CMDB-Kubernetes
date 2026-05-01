package ingestgw

import (
	"net/http"
	"regexp"
	"strings"
)

// route is one entry in the gateway's hardcoded write allowlist. Method
// and Pattern together form an exact-match key against the incoming
// request's (Method, URL.Path) tuple.
//
// Pattern uses one wildcard segment: "{uuid}". Anything else in the
// pattern is matched literally. There is no globbing, no nested
// wildcards, no trailing-slash flexibility — a request line either
// matches an entry exactly or returns 404.
type route struct {
	Method  string
	Pattern string
}

// Routes is the hardcoded ingest allowlist (ADR-0016 §2). Eighteen write
// paths the K8s push collector touches every tick — no read endpoints, no
// admin endpoints, no auth endpoints other than verify (which the gateway
// doesn't expose to collectors anyway; verify is a server-side concern,
// the gateway calls longue-vue's verify endpoint internally).
//
// Keep this list synchronised with internal/api.IngestRoutes (ADR-0016
// §3): a route longue-vue serves but the gateway blocks is fine (defence in
// depth); a route the gateway forwards but longue-vue does not register is
// a configuration error and produces a 404 at the listener.
var Routes = []route{ //nolint:gochecknoglobals // hardcoded allowlist; reads better as a literal table
	{http.MethodPost, "/v1/clusters"},
	{http.MethodPatch, "/v1/clusters/{uuid}"},
	{http.MethodPost, "/v1/nodes"},
	{http.MethodPost, "/v1/nodes/reconcile"},
	{http.MethodPost, "/v1/namespaces"},
	{http.MethodPost, "/v1/namespaces/reconcile"},
	{http.MethodPost, "/v1/pods"},
	{http.MethodPost, "/v1/pods/reconcile"},
	{http.MethodPost, "/v1/workloads"},
	{http.MethodPost, "/v1/workloads/reconcile"},
	{http.MethodPost, "/v1/services"},
	{http.MethodPost, "/v1/services/reconcile"},
	{http.MethodPost, "/v1/ingresses"},
	{http.MethodPost, "/v1/ingresses/reconcile"},
	{http.MethodPost, "/v1/persistentvolumes"},
	{http.MethodPost, "/v1/persistentvolumes/reconcile"},
	{http.MethodPost, "/v1/persistentvolumeclaims"},
	{http.MethodPost, "/v1/persistentvolumeclaims/reconcile"},
}

// uuidPattern matches a canonical 8-4-4-4-12 hex UUID. Wider than
// strictly required (no version / variant nibble check) on purpose —
// longue-vue is the authority on whether the UUID resolves to a row; the
// gateway only needs to confirm "it looks like a UUID, not a path
// traversal payload" before forwarding.
var uuidPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
) //nolint:gochecknoglobals // compile-once

// matchAllowlist resolves an incoming (method, path) against Routes.
// Returns the matched pattern (used as the Prometheus route label) and
// true when allowed, or "" / false when denied.
//
// Pattern matching is split-and-compare on '/' segments: literal
// segments must match exactly, "{uuid}" segments must look like a UUID.
// No URL decoding, no path normalisation — the caller's request line is
// taken literally. ServeMux has already done its own normalisation; the
// gateway just verifies (method, path) against the table.
func matchAllowlist(method, path string) (string, bool) {
	pathParts := strings.Split(path, "/")
	for _, r := range Routes {
		if r.Method != method {
			continue
		}
		if matchPattern(r.Pattern, pathParts) {
			return r.Pattern, true
		}
	}
	return "", false
}

// matchPattern compares a route pattern against a pre-split path. Returns
// true when every segment matches by the rule above.
func matchPattern(pattern string, pathParts []string) bool {
	patternParts := strings.Split(pattern, "/")
	if len(patternParts) != len(pathParts) {
		return false
	}
	for i, want := range patternParts {
		got := pathParts[i]
		switch want {
		case "{uuid}":
			if !uuidPattern.MatchString(got) {
				return false
			}
		default:
			if want != got {
				return false
			}
		}
	}
	return true
}
