package api

import (
	"encoding/json"
	"net/http"

	"github.com/sthalbert/argos/internal/auth"
)

// SettingsHandlers provides hand-written HTTP handlers for the runtime
// settings endpoints. These live outside the generated OpenAPI router
// because they were added after the initial spec (ADR-0012). Both
// endpoints require admin scope.

// HandleGetSettings returns the current runtime settings.
func HandleGetSettings(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		settings, err := store.GetSettings(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(settings) //nolint:errcheck // response write to HTTP client; nothing to handle
	}
}

// HandleUpdateSettings applies a merge-patch on the settings row.
func HandleUpdateSettings(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		var patch SettingsPatch
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		settings, err := store.UpdateSettings(r.Context(), patch)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(settings) //nolint:errcheck // response write to HTTP client; nothing to handle
	}
}

// requireAdmin checks that the caller has admin role. Returns false
// (and writes a 403) when the caller is missing or non-admin.
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil || caller.Role != auth.RoleAdmin {
		http.Error(w, "admin role required", http.StatusForbidden)
		return false
	}
	return true
}
