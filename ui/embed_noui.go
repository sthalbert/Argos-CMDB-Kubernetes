//go:build noui

// Stub replacement compiled when -tags noui is passed to `go build`. Lets
// backend-only developers skip `npm install` / `npm run build` entirely at
// the cost of a 404 under /ui/. See embed.go for the default build.
package ui

import "net/http"

// Handler returns an http.Handler that always replies 404, with a short
// note pointing at the Makefile target that would bundle the real SPA.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "UI bundle not built; rebuild without -tags noui after `make ui-build`", http.StatusNotFound)
	})
}
