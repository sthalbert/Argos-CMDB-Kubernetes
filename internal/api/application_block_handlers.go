package api

// Hand-written HTTP handlers for /v1/application-blocks (ADR-0029 §2.2).
// Operator-curated; collectors never touch these endpoints.
//
// Scope matrix (mirrors the ADR table):
//   POST   /v1/application-blocks         → write
//   GET    /v1/application-blocks         → read
//   GET    /v1/application-blocks/{id}    → read
//   PATCH  /v1/application-blocks/{id}    → write
//   DELETE /v1/application-blocks/{id}    → admin
//
// POST is idempotent on `name` (200 on hit, 201 on insert) — mirrors
// EnsureCluster. The store normalises Name on write (NormalizeApplicationName
// → kebab-case), so a duplicate detected here means an existing row with
// the same normalised key.

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// requireScope is a thin scope guard mirroring the inline pattern used by
// the virtual-machine + extract handlers. Returns false (and writes a
// 403) when the caller is missing or lacks the named scope.
func requireScope(w http.ResponseWriter, r *http.Request, scope string) bool {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil || !caller.HasScope(scope) {
		writeProblem(w, http.StatusForbidden, "Forbidden", scope+" scope required")
		return false
	}
	return true
}

// parseLimit pulls an optional `limit` from query string. Falls back to
// the caller-supplied default when missing or unparseable. The store
// itself clamps to a hard upper bound (200).
func parseLimit(raw string, def int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// HandleCreateApplicationBlock — write scope. POST /v1/application-blocks.
// Idempotent on name: returns 200 with the existing row on a duplicate,
// 201 with the new row on first insert. Mirrors EnsureCluster.
func HandleCreateApplicationBlock(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeWrite) {
			return
		}
		var in ApplicationBlockCreate
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		// Use the same normalisation the store applies so the empty-name
		// check rejects whitespace-only inputs too.
		if NormalizeApplicationName(in.Name) == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "name required")
			return
		}
		blk, err := store.CreateApplicationBlock(r.Context(), in)
		if err == nil {
			writeJSON(w, http.StatusCreated, blk)
			return
		}
		if errors.Is(err, ErrConflict) {
			existing, getErr := store.GetApplicationBlockByName(r.Context(), in.Name)
			if getErr == nil {
				writeJSON(w, http.StatusOK, existing)
				return
			}
			// Conflict on insert but lookup-by-name failed: surface the
			// original conflict rather than the secondary lookup error so
			// the operator sees the actionable message.
			slog.Error("application_block conflict lookup",
				slog.Any("error", getErr), slog.String("name", in.Name))
			writeProblem(w, http.StatusConflict, "Conflict", err.Error())
			return
		}
		slog.Error("create application_block", slog.Any("error", err))
		writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
	}
}

// HandleListApplicationBlocks — read scope. GET /v1/application-blocks.
// Query params: name (case-insensitive substring), owner (exact),
// limit, cursor.
func HandleListApplicationBlocks(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		q := r.URL.Query()
		limit := parseLimit(q.Get("limit"), 50)
		cursor := q.Get("cursor")

		var filter ApplicationBlockListFilter
		if v := q.Get("name"); v != "" {
			filter.Name = &v
		}
		if v := q.Get("owner"); v != "" {
			filter.Owner = &v
		}

		items, next, err := store.ListApplicationBlocks(r.Context(), filter, limit, cursor)
		if err != nil {
			slog.Error("list application_blocks", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":       items,
			"next_cursor": next,
		})
	}
}

// HandleGetApplicationBlock — read scope. GET /v1/application-blocks/{id}.
func HandleGetApplicationBlock(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		blk, err := store.GetApplicationBlock(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("get application_block", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, blk)
	}
}

// HandlePatchApplicationBlock — write scope. PATCH /v1/application-blocks/{id}.
// Merge-patch on the mutable fields; name is intentionally NOT mutable
// here (ADR-0029 §2.2 — renaming would orphan inbound links).
func HandlePatchApplicationBlock(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeWrite) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		var in ApplicationBlockPatch
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		blk, err := store.UpdateApplicationBlock(r.Context(), id, in)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("patch application_block", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, blk)
	}
}

// HandleDeleteApplicationBlock — admin scope. DELETE /v1/application-blocks/{id}.
// Migration 00046 declares ON DELETE SET NULL on applications.application_block_id,
// so dependent Applications survive (just become un-blocked).
func HandleDeleteApplicationBlock(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if err := store.DeleteApplicationBlock(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("delete application_block", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
