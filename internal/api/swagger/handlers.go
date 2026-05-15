package swagger

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"strings"
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
		inm := r.Header.Get("If-None-Match")
		if inm == "*" || inm == openapiETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(openapiYAML)
	})
}

// UIHandler serves the Swagger UI shell. Designed to be mounted at
// /docs/ with http.StripPrefix("/docs", ...) so a request to /docs/ arrives
// here as "/" and is served the index; a request to /docs/<asset> arrives
// as /<asset> and is served from the embedded dist/ subtree.
//
// Misses return 404 (no fallback to index.html) so broken vendor copies
// surface loudly instead of silently rendering an empty Swagger UI shell.
func UIHandler() http.Handler {
	distSub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Compile-time guarantee: dist/ is embedded. A failure here is a
		// programming error, not a runtime condition.
		panic("swagger: fs.Sub(distFS, \"dist\"): " + err.Error())
	}
	distSrv := http.FileServer(http.FS(distSub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/") {
		case "":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(indexHTML)
			return
		case "init.js":
			// Separate file (rather than inline in index.html) so the strict
			// `script-src 'self'` CSP applied to /docs/* responses accepts
			// the bootstrap without needing `'unsafe-inline'`.
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(initJS)
			return
		}
		distSrv.ServeHTTP(w, r)
	})
}
