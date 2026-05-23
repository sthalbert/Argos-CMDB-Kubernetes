package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

var errReplicaTargetNotFound = errors.New("replicates_from_hostname does not match any existing mirror row")

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
		if in.ReplicatesFromHostname != nil && *in.ReplicatesFromHostname != "" {
			if !in.IsMirror {
				writeProblem(w, http.StatusBadRequest, "Bad Request",
					"replicates_from_hostname requires is_mirror=true")
				return
			}
			if *in.ReplicatesFromHostname == in.Hostname {
				writeProblem(w, http.StatusBadRequest, "Bad Request",
					"replicates_from_hostname cannot equal hostname")
				return
			}
			target, terr := findReplicaTarget(r.Context(), s, *in.ReplicatesFromHostname)
			switch {
			case errors.Is(terr, errReplicaTargetNotFound):
				writeProblem(w, http.StatusBadRequest, "Bad Request", terr.Error())
				return
			case terr != nil:
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", terr.Error())
				return
			}
			if target.ReplicatesFromHostname != nil && *target.ReplicatesFromHostname != "" {
				writeProblem(w, http.StatusBadRequest, "Bad Request",
					"replica target must not itself be a replica")
				return
			}
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
		if p.ReplicatesFromHostname != nil && *p.ReplicatesFromHostname != "" {
			// Read the existing row to know the current is_mirror value.
			existing, err := s.GetImageRegistry(r.Context(), host, prefix)
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "registry not found")
				return
			}
			if err != nil {
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
				return
			}
			isMirror := existing.IsMirror
			if p.IsMirror != nil {
				isMirror = *p.IsMirror
			}
			if !isMirror {
				writeProblem(w, http.StatusBadRequest, "Bad Request",
					"replicates_from_hostname requires is_mirror=true")
				return
			}
			if *p.ReplicatesFromHostname == host {
				writeProblem(w, http.StatusBadRequest, "Bad Request",
					"replicates_from_hostname cannot equal hostname")
				return
			}
			target, terr := findReplicaTarget(r.Context(), s, *p.ReplicatesFromHostname)
			switch {
			case errors.Is(terr, errReplicaTargetNotFound):
				writeProblem(w, http.StatusBadRequest, "Bad Request", terr.Error())
				return
			case terr != nil:
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", terr.Error())
				return
			}
			if target.ReplicatesFromHostname != nil && *target.ReplicatesFromHostname != "" {
				writeProblem(w, http.StatusBadRequest, "Bad Request",
					"replica target must not itself be a replica")
				return
			}
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

// findReplicaTarget locates the (annotation-mirror) row a replica points
// at. We scan all rows for the hostname since the path_prefix isn't part
// of the replica pointer. Returns the first matching mirror row, or
// errReplicaTargetNotFound when the hostname doesn't match any mirror.
// Other errors (e.g. ListImageRegistries failure) are returned as-is so
// the caller can distinguish 4xx (client input) from 5xx (server fault).
func findReplicaTarget(ctx context.Context, s Store, hostname string) (ImageRegistry, error) {
	rows, err := s.ListImageRegistries(ctx)
	if err != nil {
		return ImageRegistry{}, fmt.Errorf("list registries: %w", err)
	}
	for i := range rows {
		if rows[i].Hostname == hostname && rows[i].IsMirror {
			return rows[i], nil
		}
	}
	return ImageRegistry{}, errReplicaTargetNotFound
}
