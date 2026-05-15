package swagger

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

// openapiETag is the strong validator for the embedded spec. Computed once
// at init from a SHA-256 prefix; quoted per RFC 7232.
var openapiETag = func() string {
	sum := sha256.Sum256(openapiYAML)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}()

// OpenAPISpecHandler serves the embedded OpenAPI 3.1 spec. Intended to be
// mounted under `requireReadScope` + auth middleware so anonymous callers
// get a 401, not the spec.
func OpenAPISpecHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.Header().Set("ETag", openapiETag)
		w.Header().Set("Cache-Control", "no-cache")
		if r.Header.Get("If-None-Match") == openapiETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(openapiYAML)
	})
}
