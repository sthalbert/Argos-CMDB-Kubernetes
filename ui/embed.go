//go:build !noui

// Package ui owns the embedded SPA bundle served by argosd under /ui/.
//
// Two build modes:
//   - default: //go:embed pulls ui/dist into the binary; Handler() serves it.
//     `npm run build` (see Makefile: make ui-build) must have produced dist/
//     before `go build` runs. CI does this automatically.
//   - -tags noui: compiles embed_noui.go instead, returning a stub handler
//     that replies 404. Lets backend-only workflows skip the Node toolchain.
//
// index.html is rewritten with a small shim that rewrites asset URLs so the
// bundle works at /ui/. See vite.config.ts `base: '/ui/'`.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded SPA bundle.
// Intended to be mounted at /ui/ by the main mux:
//
//	mux.Handle("/ui/", http.StripPrefix("/ui", ui.Handler()))
//
// Unknown paths under /ui/ fall back to index.html so client-side routing
// works on page reload (e.g. /ui/clusters → served index.html → React Router
// renders the Clusters page).
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Only possible if the //go:embed directive above changes shape,
		// which is a build-time issue surfaced here as a runtime panic on
		// first request so the misconfiguration is loud.
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "ui: embedded fs subtree missing: "+err.Error(), http.StatusInternalServerError)
		})
	}

	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve the requested path if it exists; otherwise fall through to
		// index.html so a hard reload of /ui/clusters still gets the SPA.
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(sub, path); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
