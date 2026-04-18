package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// BearerAuth returns a middleware that enforces bearer-token auth on every
// request except the liveness and readiness probes, matching the security
// declared in api/openapi/openapi.yaml.
//
// A constant-time compare prevents token-probing timing attacks. Callers
// must not pass an empty token; argosd refuses to start without one.
func BearerAuth(token string) func(http.Handler) http.Handler {
	expected := []byte(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isHealthProbe(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			presented, ok := extractBearer(r.Header.Get("Authorization"))
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="argos"`)
				writeProblem(w, http.StatusUnauthorized, "Unauthorized", "missing bearer token")
				return
			}
			if subtle.ConstantTimeCompare([]byte(presented), expected) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="argos"`)
				writeProblem(w, http.StatusUnauthorized, "Unauthorized", "invalid bearer token")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func isHealthProbe(path string) bool {
	return path == "/healthz" || path == "/readyz"
}

func extractBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimPrefix(header, prefix)
	if token == "" {
		return "", false
	}
	return token, true
}
