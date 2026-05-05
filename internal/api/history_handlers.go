package api

// history_handlers.go — hand-written HTTP handlers for the time-travel history
// and point-in-time API surface (ADR-0021 Phase 3).
//
// Routes (mounted in cmd/longue-vue/main.go alongside impact/settings):
//
//	GET /v1/clusters/{id}/history
//	GET /v1/namespaces/{id}/history
//	GET /v1/nodes/{id}/history
//	GET /v1/workloads/{id}/history
//
// Point-in-time:
//
//	GET /v1/{kind}/{id}?as_of=<RFC3339> is intercepted by AsOfMiddleware
//	which wraps the generated mux and intercepts before route dispatch.
//
// All require read scope. When time_travel_enabled=false they return 503.

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// HandleEntityHistory returns the paginated history for one entity.
// Route: GET /v1/{kind}/{id}/history
// kind is one of: clusters, namespaces, nodes, workloads.
func HandleEntityHistory(store Store, kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth.CallerFromContext(r.Context()) == nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if !checkTimeTravelEnabled(w, r, store) {
			return
		}

		id, ok := parseUUIDParam(w, r.PathValue("id"))
		if !ok {
			return
		}

		limit, cursor := parseHistoryPagination(r)
		rows, nextCursor, err := store.ListEntityHistory(r.Context(), kind, id, limit, cursor)
		if err != nil {
			slog.Error("history: list entity history", slog.String("kind", kind), slog.Any("error", err))
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		resp := map[string]any{"items": rows, "next_cursor": nextCursor}
		w.Header().Set("Content-Type", "application/json")
		if encErr := json.NewEncoder(w).Encode(resp); encErr != nil {
			slog.Warn("history: encode response", slog.Any("error", encErr))
		}
	}
}

// checkTimeTravelEnabled writes a 503 and returns false when time travel is
// disabled. Returns true when enabled or when the flag read fails (fail-open
// to preserve availability of other endpoints).
func checkTimeTravelEnabled(w http.ResponseWriter, r *http.Request, store Store) bool {
	enabled, err := store.IsTimeTravelEnabled(r.Context())
	if err != nil {
		slog.Error("history: read time_travel_enabled", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return false
	}
	if !enabled {
		writeTimeTravelDisabled(w)
		return false
	}
	return true
}

// parseUUIDParam parses rawID as a UUID; writes 400 and returns false on error.
func parseUUIDParam(w http.ResponseWriter, rawID string) (uuid.UUID, bool) {
	id, err := uuid.Parse(rawID)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return uuid.Nil, false
	}
	return id, true
}

// parseHistoryPagination reads ?limit and ?cursor from a request.
func parseHistoryPagination(r *http.Request) (limit int, cursor string) {
	limit = 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	return limit, r.URL.Query().Get("cursor")
}

// asOfKindPrefixes maps URL path prefixes to history kind names.
// The four history-bearing kinds supported by Phase 3.
var asOfKindPrefixes = map[string]string{
	"/v1/clusters/":   "clusters",
	"/v1/namespaces/": "namespaces",
	"/v1/nodes/":      "nodes",
	"/v1/workloads/":  "workloads",
}

// resolveAsOfKind returns the history kind and entity ID string for a path
// that matches one of the four supported entity path prefixes, provided the
// path has no additional trailing segments (e.g. /history). Returns ("","")
// when the path does not match any supported prefix.
func resolveAsOfKind(path string) (kind, idStr string) {
	for prefix, k := range asOfKindPrefixes {
		if strings.HasPrefix(path, prefix) {
			rest := path[len(prefix):]
			if !strings.Contains(rest, "/") {
				return k, rest
			}
		}
	}
	return "", ""
}

// AsOfMiddleware wraps next and intercepts GET requests that carry a
// ?as_of=<RFC3339> query parameter for the four history-bearing entity paths.
// When ?as_of is present and the path matches /v1/{kind}/{id} (no trailing
// segment), the middleware resolves the snapshot from the history table and
// responds directly; otherwise it delegates to next.
//
// The middleware is mounted in main.go to wrap the entire mux so it runs
// before the generated router's route-dispatch. Auth middleware must already
// have run so auth.CallerFromContext is populated.
func AsOfMiddleware(store Store) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !shouldInterceptAsOf(r) {
				next.ServeHTTP(w, r)
				return
			}
			serveAsOf(w, r, store)
		})
	}
}

// shouldInterceptAsOf reports whether this request should be handled by the
// as_of interceptor. Returns false for any request that is not a plain GET
// with ?as_of on a supported entity path.
func shouldInterceptAsOf(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if r.URL.Query().Get("as_of") == "" {
		return false
	}
	kind, _ := resolveAsOfKind(r.URL.Path)
	return kind != ""
}

// serveAsOf executes the point-in-time lookup and writes the response.
func serveAsOf(w http.ResponseWriter, r *http.Request, store Store) {
	asOfStr := r.URL.Query().Get("as_of")
	kind, idStr := resolveAsOfKind(r.URL.Path)

	if auth.CallerFromContext(r.Context()) == nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if !checkTimeTravelEnabled(w, r, store) {
		return
	}

	t, err := time.Parse(time.RFC3339, asOfStr)
	if err != nil {
		http.Error(w, "as_of must be an RFC3339 timestamp", http.StatusBadRequest)
		return
	}

	id, ok := parseUUIDParam(w, idStr)
	if !ok {
		return
	}

	row, err := store.GetEntityAsOf(r.Context(), kind, id, t)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found at the requested time", http.StatusNotFound)
			return
		}
		slog.Error("as_of: GetEntityAsOf", slog.String("kind", kind), slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if encErr := json.NewEncoder(w).Encode(row); encErr != nil {
		slog.Warn("as_of: encode response", slog.Any("error", encErr))
	}
}

// writeTimeTravelDisabled writes a 503 problem+json response indicating the
// time-travel feature is currently disabled in settings.
func writeTimeTravelDisabled(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusServiceUnavailable)
	body := map[string]any{
		"type":             "about:blank",
		"title":            "Feature disabled",
		"status":           503,
		"detail":           "time travel is disabled in settings",
		"feature_disabled": "time_travel",
	}
	if encErr := json.NewEncoder(w).Encode(body); encErr != nil {
		slog.Warn("writeTimeTravelDisabled: encode", slog.Any("error", encErr))
	}
}
