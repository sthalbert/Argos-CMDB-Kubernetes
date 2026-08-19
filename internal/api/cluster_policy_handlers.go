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

const cpListNameMaxLen = 100

// Enum values from the OpenAPI contract (ClusterPolicyCreate).
const (
	resourceTypeClusterPolicy = "ClusterPolicy"
	resourceTypePolicy        = "Policy"
	policyScopeCluster        = "cluster"
	policyScopeNamespace      = "namespace"
	failurePolicyFail         = "Fail"
	failurePolicyIgnore       = "Ignore"
)

// jsonNullLiteral is the 4-byte payload json.Marshal produces for a
// missing value; a json.RawMessage holding it is valid JSON but carries
// no data.
const jsonNullLiteral = "null"

var (
	validScopes        = map[string]bool{policyScopeCluster: true, policyScopeNamespace: true}
	validResourceTypes = map[string]bool{resourceTypeClusterPolicy: true, resourceTypePolicy: true}
	validSeverities    = map[string]bool{"critical": true, "high": true, "medium": true, "low": true, "info": true}
	validActions       = map[string]bool{"enforce": true, "audit": true}
)

// normalizeClusterPolicyEnums lowercases/title-cases the optional enum
// fields in place and validates them against the OpenAPI contract.
// Returns the RFC 7807 detail for a 400, or "" when valid.
//
//nolint:gocyclo // sequential field validation; each check is trivial
func normalizeClusterPolicyEnums(in *ClusterPolicyCreate) string {
	if in.Action != nil {
		a := strings.ToLower(*in.Action)
		if !validActions[a] {
			return "action must be enforce or audit"
		}
		in.Action = &a
	}
	if in.Severity != nil {
		s := strings.ToLower(*in.Severity)
		if !validSeverities[s] {
			return "severity must be one of critical, high, medium, low, info"
		}
		in.Severity = &s
	}
	if in.RulesCount != nil && (*in.RulesCount < 0 || *in.RulesCount > math.MaxInt32) {
		return "rules_count must be between 0 and 2147483647"
	}
	if in.FailurePolicy != nil {
		fp := titleCaseFailurePolicy(*in.FailurePolicy)
		if fp != failurePolicyFail && fp != failurePolicyIgnore {
			return "failure_policy must be Fail or Ignore"
		}
		in.FailurePolicy = &fp
	}
	return ""
}

// validateClusterPolicyCreate normalises and validates the decoded POST
// body in place (scope defaulting, action/severity/failure_policy
// casing). Returns the RFC 7807 detail for a 400, or "" when valid.
// Referential checks that need the store (namespace lookup) stay in the
// handler.
//
//nolint:gocyclo // sequential field validation; each check is trivial
func validateClusterPolicyCreate(in *ClusterPolicyCreate) string {
	if in.Name == "" {
		return "name required"
	}
	if in.ClusterID == uuid.Nil {
		return "cluster_id required"
	}
	if in.ResourceType == "" {
		return "resource_type required"
	}
	if !validResourceTypes[in.ResourceType] {
		return "resource_type must be ClusterPolicy or Policy"
	}
	if in.Scope == "" {
		if in.ResourceType == resourceTypePolicy {
			in.Scope = policyScopeNamespace
		} else {
			in.Scope = policyScopeCluster
		}
	}
	in.Scope = strings.ToLower(in.Scope)
	if !validScopes[in.Scope] {
		return "scope must be cluster or namespace"
	}
	if in.ResourceType == resourceTypePolicy && in.Scope != policyScopeNamespace {
		return "scope must be namespace when resource_type is Policy"
	}
	if in.ResourceType == resourceTypeClusterPolicy && in.Scope != policyScopeCluster {
		return "scope must be cluster when resource_type is ClusterPolicy"
	}
	if len(in.SpecRaw) == 0 || string(in.SpecRaw) == jsonNullLiteral {
		return "spec_raw required"
	}
	if b := bytes.TrimSpace(in.SpecRaw); len(b) == 0 || b[0] != '{' {
		return "spec_raw must be a JSON object"
	}
	if in.ResourceType == resourceTypePolicy && in.NamespaceID == nil {
		return "namespace_id required when resource_type is Policy"
	}
	if in.ResourceType == resourceTypeClusterPolicy && in.NamespaceID != nil {
		return "namespace_id must be omitted when resource_type is ClusterPolicy"
	}
	return normalizeClusterPolicyEnums(in)
}

// checkNamespaceBelongsToCluster validates that nsID exists and belongs
// to clusterID; writes the 422/500 problem and returns false otherwise.
// Shared by the two Kyverno POST handlers.
func checkNamespaceBelongsToCluster(w http.ResponseWriter, r *http.Request, store Store, nsID, clusterID uuid.UUID) bool {
	ns, err := store.GetNamespace(r.Context(), nsID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeProblem(w, http.StatusUnprocessableEntity, "Unprocessable Entity",
				"namespace_id does not exist")
			return false
		}
		slog.Error("lookup namespace for policy write", slog.Any("error", err))
		writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
		return false
	}
	if ns.ClusterId != clusterID {
		writeProblem(w, http.StatusUnprocessableEntity, "Unprocessable Entity",
			"namespace_id does not belong to the specified cluster_id")
		return false
	}
	return true
}

// requirePoliciesEnabled gates every Kyverno policy-view endpoint on the
// policies_enabled settings toggle (ADR-0043). Writes 500 when the settings
// read fails, 409 when the feature is off; returns true only when enabled.
// Mirrors checkTimeTravelEnabled in history_handlers.go.
func requirePoliciesEnabled(w http.ResponseWriter, r *http.Request, store Store) bool {
	settings, err := store.GetSettings(r.Context())
	if err != nil {
		slog.Error("settings unavailable", slog.Any("error", err))
		writeProblem(w, http.StatusInternalServerError, "settings unavailable", "")
		return false
	}
	if !settings.PoliciesEnabled {
		writeProblem(w, http.StatusConflict, "policies disabled",
			"enable policies_enabled in admin settings to use this endpoint")
		return false
	}
	return true
}

// HandleCreateClusterPolicy serves POST /v1/cluster-policies: upsert by
// (cluster_id, namespace_id, name), source='api' (ADR-0043 §3b).
//
//nolint:gocyclo // sequential decode→validate→upsert error handling
func HandleCreateClusterPolicy(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeWrite) {
			return
		}
		if !requirePoliciesEnabled(w, r, store) {
			return
		}
		var in ClusterPolicyCreate
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
		if detail := validateClusterPolicyCreate(&in); detail != "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", detail)
			return
		}
		if in.NamespaceID != nil && !checkNamespaceBelongsToCluster(w, r, store, *in.NamespaceID, in.ClusterID) {
			return
		}
		cp := ClusterPolicyRow{
			ClusterID:       in.ClusterID,
			NamespaceID:     in.NamespaceID,
			Name:            in.Name,
			ResourceType:    in.ResourceType,
			Scope:           in.Scope,
			Description:     in.Description,
			Category:        in.Category,
			Severity:        in.Severity,
			Action:          in.Action,
			FailurePolicy:   in.FailurePolicy,
			Background:      in.Background,
			RuleTypes:       in.RuleTypes,
			RulesCount:      in.RulesCount,
			TargetResources: in.TargetResources,
			KeyExclusions:   in.KeyExclusions,
			Ready:           in.Ready,
			Annotations:     in.Annotations,
			SpecRaw:         in.SpecRaw,
			Source:          SourceAPI,
		}

		id, err := store.UpsertClusterPolicy(r.Context(), cp)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				writeProblem(
					w,
					http.StatusConflict,
					"Conflict",
					"a collector-managed policy already exists at this key; it will be overwritten on the next collector tick when the policy no longer exists in-cluster",
				)
				return
			}
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusUnprocessableEntity, "Unprocessable Entity",
					"referenced cluster or namespace does not exist")
				return
			}
			slog.Error("create cluster policy", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		created, err := store.GetClusterPolicy(r.Context(), id)
		if err != nil {
			slog.Error("read back created cluster policy", slog.String("id", id.String()), slog.Any("error", err))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			//nolint:errcheck // response write; nothing to handle
			fmt.Fprintf(w, `{"id":"%s","warning":"cluster policy created but read-back failed"}`, id)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

func titleCaseFailurePolicy(s string) string {
	switch strings.ToLower(s) {
	case "fail":
		return failurePolicyFail
	case "ignore":
		return failurePolicyIgnore
	default:
		return s
	}
}

// HandleListClusterPolicies serves GET /v1/cluster-policies with the
// cursor-paginated, filterable policy inventory (ADR-0043).
//
//nolint:gocognit,gocyclo // eight optional filters; each branch is trivial
func HandleListClusterPolicies(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		if !requirePoliciesEnabled(w, r, store) {
			return
		}
		q := r.URL.Query()

		var filter ClusterPolicyListFilter
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
			if len(v) > cpListNameMaxLen {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "name too long")
				return
			}
			filter.Name = &v
		}
		if v := q.Get("resource_type"); v != "" {
			filter.ResourceType = &v
		}
		if v := q.Get("action"); v != "" {
			filter.Action = &v
		}
		if v := q.Get("severity"); v != "" {
			filter.Severity = &v
		}
		if v := q.Get("failure_policy"); v != "" {
			filter.FailurePolicy = &v
		}
		if v := q.Get("category"); v != "" {
			filter.Category = &v
		}

		page := parseListPage(r)
		items, next, err := store.ListClusterPolicies(r.Context(), filter, page)
		if err != nil {
			writeListError(w, "list cluster policies", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			respKeyItems:      items,
			respKeyNextCursor: next,
		})
	}
}

// HandleGetClusterPolicy serves GET /v1/cluster-policies/{id}.
func HandleGetClusterPolicy(store Store) http.HandlerFunc {
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
		cp, err := store.GetClusterPolicy(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("get cluster policy", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, cp)
	}
}

// HandleDeleteClusterPolicy serves DELETE /v1/cluster-policies/{id}.
// Only API-authored rows (source='api') can be deleted; collector-managed
// rows return 404 (ADR-0043 §3b).
func HandleDeleteClusterPolicy(store Store) http.HandlerFunc {
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
		if err := store.DeleteClusterPolicy(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found",
					"cluster policy not found or is collector-managed (cannot delete)")
				return
			}
			slog.Error("delete cluster policy", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
