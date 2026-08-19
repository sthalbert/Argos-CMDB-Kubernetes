package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

const prListNameMaxLen = 100

// validatePolicyReportCreate validates the decoded POST body. Returns
// the RFC 7807 detail for a 400, or "" when valid. The namespace
// referential check stays in the handler (it needs the store).
func validatePolicyReportCreate(in *PolicyReportCreate) string {
	if in.Name == "" {
		return "name required"
	}
	if in.ClusterID == uuid.Nil {
		return "cluster_id required"
	}
	for _, f := range []struct {
		name  string
		value int
	}{
		{"summary_pass", in.SummaryPass},
		{"summary_fail", in.SummaryFail},
		{"summary_warn", in.SummaryWarn},
		{"summary_error", in.SummaryError},
		{"summary_skip", in.SummarySkip},
	} {
		// The columns are INTEGER: bound the counts so an oversized
		// value is a 400, not a failed INSERT surfacing as a 500.
		if f.value < 0 || f.value > math.MaxInt32 {
			return f.name + " must be between 0 and 2147483647"
		}
	}
	if len(in.ResultsRaw) > 0 && string(in.ResultsRaw) != jsonNullLiteral {
		if b := bytes.TrimSpace(in.ResultsRaw); len(b) == 0 || b[0] != '[' {
			return "results_raw must be a JSON array"
		}
	}
	return ""
}

// HandleCreatePolicyReport serves POST /v1/policy-reports: upsert by
// (cluster_id, namespace_id, name), source='api' (ADR-0043 §3b).
//
//nolint:gocyclo // sequential decode→validate→upsert error handling
func HandleCreatePolicyReport(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeWrite) {
			return
		}
		if !requirePoliciesEnabled(w, r, store) {
			return
		}
		var in PolicyReportCreate
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			if strings.Contains(err.Error(), "http: request body too large") {
				writeProblem(w, http.StatusRequestEntityTooLarge, "Payload Too Large",
					"request body exceeds 1 MiB limit")
				return
			}
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		if detail := validatePolicyReportCreate(&in); detail != "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", detail)
			return
		}
		// scope_kind is stored verbatim: the collector writes K8s kinds
		// as-is and the list filter compares case-insensitively, so
		// normalising here would only split the same kind across two
		// spellings (ReplicaSet vs Replicaset).
		if in.NamespaceID != nil && !checkNamespaceBelongsToCluster(w, r, store, *in.NamespaceID, in.ClusterID) {
			return
		}
		pr := PolicyReportRow{
			ClusterID:    in.ClusterID,
			NamespaceID:  in.NamespaceID,
			Name:         in.Name,
			ScopeKind:    in.ScopeKind,
			ScopeName:    in.ScopeName,
			SummaryPass:  in.SummaryPass,
			SummaryFail:  in.SummaryFail,
			SummaryWarn:  in.SummaryWarn,
			SummaryError: in.SummaryError,
			SummarySkip:  in.SummarySkip,
			ResultsRaw:   in.ResultsRaw,
			Source:       SourceAPI,
		}

		id, err := store.UpsertPolicyReport(r.Context(), pr)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				writeProblem(
					w,
					http.StatusConflict,
					"Conflict",
					"a collector-managed report already exists at this key; it will be overwritten on the next collector tick when the report no longer exists in-cluster",
				)
				return
			}
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusUnprocessableEntity, "Unprocessable Entity",
					"referenced cluster or namespace does not exist")
				return
			}
			slog.Error("create policy report", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		created, err := store.GetPolicyReport(r.Context(), id)
		if err != nil {
			slog.Error("read back created policy report", slog.String("id", id.String()), slog.Any("error", err))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			//nolint:errcheck // response write; nothing to handle
			fmt.Fprintf(w, `{"id":"%s","warning":"policy report created but read-back failed"}`, id)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

// HandleListPolicyReports serves GET /v1/policy-reports with the
// cursor-paginated, filterable report inventory (ADR-0043).
//
//nolint:gocognit,gocyclo // five optional filters; each branch is trivial
func HandleListPolicyReports(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		if !requirePoliciesEnabled(w, r, store) {
			return
		}
		q := r.URL.Query()

		var filter PolicyReportListFilter
		if raw := q.Get("cluster_id"); raw != "" {
			id, parseErr := uuid.Parse(raw)
			if parseErr != nil {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "cluster_id must be a valid UUID")
				return
			}
			filter.ClusterID = &id
		}
		if raw := q.Get("namespace_id"); raw != "" {
			id, parseErr := uuid.Parse(raw)
			if parseErr != nil {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "namespace_id must be a valid UUID")
				return
			}
			filter.NamespaceID = &id
		}
		if v := q.Get("name"); v != "" {
			if len(v) > prListNameMaxLen {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "name too long")
				return
			}
			filter.Name = &v
		}
		if v := q.Get("scope_kind"); v != "" {
			if len(v) > prListNameMaxLen {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "scope_kind too long")
				return
			}
			filter.ScopeKind = &v
		}
		if v := q.Get("scope_name"); v != "" {
			if len(v) > prListNameMaxLen {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "scope_name too long")
				return
			}
			filter.ScopeName = &v
		}

		page := parseListPage(r)
		items, next, err := store.ListPolicyReports(r.Context(), filter, page)
		if err != nil {
			writeListError(w, "list policy reports", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			respKeyItems:      items,
			respKeyNextCursor: next,
		})
	}
}

// HandleGetPolicyReport serves GET /v1/policy-reports/{id}.
func HandleGetPolicyReport(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		if !requirePoliciesEnabled(w, r, store) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		pr, err := store.GetPolicyReport(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("get policy report", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, pr)
	}
}

// HandleDeletePolicyReport serves DELETE /v1/policy-reports/{id}.
// Only API-authored rows (source='api') can be deleted; collector-managed
// rows return 404 (ADR-0043 §3b).
func HandleDeletePolicyReport(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeWrite) {
			return
		}
		if !requirePoliciesEnabled(w, r, store) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if err := store.DeletePolicyReport(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found",
					"policy report not found or is collector-managed (cannot delete)")
				return
			}
			slog.Error("delete policy report", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
