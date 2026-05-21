package api

import (
	"encoding/json"
	"errors"
	"net/http"
)

// pathPrefixFromRequest decodes the path_prefix path param, mapping the
// literal "_root" sentinel to an empty string (the canonical form for
// non-mirror / unscoped rows). Empty value is treated as "_root" too.
func pathPrefixFromRequest(r *http.Request) string {
	p := r.PathValue("path_prefix")
	if p == "" || p == "_root" {
		return ""
	}
	return p
}

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
func HandleUpdateImageRegistry(s Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.PathValue("hostname")
		if host == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "hostname is required")
			return
		}
		prefix := pathPrefixFromRequest(r)
		var p ImageRegistryPatch
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", err.Error())
			return
		}
		if p.RateLimitPerSec != nil && *p.RateLimitPerSec <= 0 {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "rate_limit_per_sec must be > 0")
			return
		}
		out, err := s.UpdateImageRegistry(r.Context(), host, prefix, p)
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
		prefix := pathPrefixFromRequest(r)
		err := s.DeleteImageRegistry(r.Context(), host, prefix)
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

// HandleGetImageRegistryCredentials returns the plaintext robot-account
// credentials for a mirror registry row. Admin-only, audit-logged.
func HandleGetImageRegistryCredentials(s Store) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.PathValue("hostname")
		if host == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "hostname is required")
			return
		}
		prefix := pathPrefixFromRequest(r)
		reg, err := s.GetImageRegistry(r.Context(), host, prefix)
		switch {
		case errors.Is(err, ErrNotFound):
			writeProblem(w, http.StatusNotFound, "Not Found", "registry not found")
			return
		case err != nil:
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
			return
		}
		if !reg.IsMirror {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "not a mirror registry")
			return
		}
		tok, err := s.GetMirrorAuthToken(r.Context(), host, prefix)
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
			return
		}
		user := ""
		if reg.AuthUsername != nil {
			user = *reg.AuthUsername
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"auth_username": user,
			"auth_token":    tok,
		})
	})
}
