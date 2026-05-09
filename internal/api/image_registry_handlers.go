package api

import (
	"encoding/json"
	"errors"
	"net/http"
)

// HandleListImageRegistries returns all rows from image_versions_registries.
func HandleListImageRegistries(s Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := s.ListImageRegistries(r.Context())
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})
}

// HandleCreateImageRegistry inserts a new registry row.
// Returns 400 on bad input, 409 on hostname conflict, 201 with the row otherwise.
func HandleCreateImageRegistry(s Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in ImageRegistryUpsert
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", err.Error())
			return
		}
		if in.Hostname == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "hostname is required")
			return
		}
		if in.RateLimitPerSec <= 0 {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "rate_limit_per_sec must be > 0")
			return
		}
		out, err := s.CreateImageRegistry(r.Context(), in)
		switch {
		case errors.Is(err, ErrConflict):
			writeProblem(w, http.StatusConflict, "Conflict", "hostname already exists")
			return
		case err != nil:
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, out)
	})
}

// HandleUpdateImageRegistry applies a merge-patch to a registry row.
// Returns 400 on bad rate, 404 on missing hostname, 200 with the row otherwise.
func HandleUpdateImageRegistry(s Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.PathValue("hostname")
		if host == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "hostname is required")
			return
		}
		var p ImageRegistryPatch
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", err.Error())
			return
		}
		if p.RateLimitPerSec != nil && *p.RateLimitPerSec <= 0 {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "rate_limit_per_sec must be > 0")
			return
		}
		out, err := s.UpdateImageRegistry(r.Context(), host, p)
		switch {
		case errors.Is(err, ErrNotFound):
			writeProblem(w, http.StatusNotFound, "Not Found", "registry not found")
			return
		case err != nil:
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// HandleDeleteImageRegistry removes a registry. Returns 204 on success.
func HandleDeleteImageRegistry(s Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.PathValue("hostname")
		if host == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "hostname is required")
			return
		}
		err := s.DeleteImageRegistry(r.Context(), host)
		switch {
		case errors.Is(err, ErrNotFound):
			writeProblem(w, http.StatusNotFound, "Not Found", "registry not found")
			return
		case err != nil:
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
