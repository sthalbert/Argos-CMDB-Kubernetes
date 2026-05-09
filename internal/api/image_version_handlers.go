package api

import (
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// EnricherTrigger abstracts the methods used by HandleRefreshImageVersions.
// Defined here so handlers don't pull in the imageversions package types directly.
type EnricherTrigger interface {
	Trigger() bool
	IsRunning() bool
}

// HandleListImageVersions returns a paginated list of distinct image_repos
// with their variants nested. Filters: registry, image_repo (substring),
// variant, has_error, last_checked_before. Pagination via opaque cursor.
func HandleListImageVersions(s Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		params := ImageVersionListParams{
			Limit:         parseIntDefault(q.Get("limit"), 50),
			Cursor:        q.Get("cursor"),
			Registry:      q.Get("registry"),
			ImageRepoLike: q.Get("image_repo"),
			Variant:       q.Get("variant"),
		}
		if v := q.Get("has_error"); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "has_error must be a boolean")
				return
			}
			params.HasError = &b
		}
		if v := q.Get("last_checked_before"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "last_checked_before must be RFC 3339")
				return
			}
			params.LastCheckedBefore = &t
		}

		items, next, err := s.ListImageVersionsByRepo(r.Context(), params)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":       items,
			"next_cursor": next,
		})
	})
}

// HandleGetImageVersion returns the detail (all variants) for one image_repo.
// The image_repo path parameter is URL-encoded.
func HandleGetImageVersion(s Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("image_repo")
		if decoded, err := url.PathUnescape(repo); err == nil {
			repo = decoded
		}
		rows, err := s.GetImageVersionsByRepo(r.Context(), repo)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
			return
		}
		if len(rows) == 0 {
			writeProblem(w, http.StatusNotFound, "Not Found", "image_repo not found")
			return
		}
		view := ImageVersionRepoView{
			ImageRepo: rows[0].ImageRepo,
			Registry:  rows[0].Registry,
			Variants:  rows,
		}
		writeJSON(w, http.StatusOK, view)
	})
}

// HandleRefreshImageVersions triggers an immediate enrichment cycle.
// Returns 409 if the feature is disabled, 202 with {queued, already_running} otherwise.
func HandleRefreshImageVersions(s Store, enr EnricherTrigger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		settings, err := s.GetSettings(r.Context())
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
			return
		}
		if !settings.ImageVersionsEnabled {
			writeProblem(w, http.StatusConflict, "Conflict", "image_versions_enabled is false")
			return
		}
		running := enr.Trigger()
		writeJSON(w, http.StatusAccepted, map[string]any{
			"queued":          !running,
			"already_running": running,
		})
	})
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
