package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/eolagg"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

// ExtractStore is the narrow slice of Store the extract handlers consume.
type ExtractStore interface {
	ListClusters(ctx context.Context, limit int, cursor string, includeTerminated bool) ([]Cluster, string, error)
	ListNodes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string, includeTerminated bool) ([]Node, string, error)
	ListNamespaces(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string, includeTerminated bool) ([]Namespace, string, error)
	ListWorkloads(ctx context.Context, filter WorkloadListFilter, limit int, cursor string) ([]Workload, string, error)
	ListPods(ctx context.Context, filter PodListFilter, limit int, cursor string) ([]Pod, string, error)
	ListVirtualMachines(ctx context.Context, filter VirtualMachineListFilter, limit int, cursor string) ([]VirtualMachine, string, error)
	ListCloudAccounts(ctx context.Context, limit int, cursor string) ([]CloudAccount, string, error)
}

const extractPageSize = 200

// HandleEolExtract — read scope. GET /v1/eol/extract?format=csv|json
// [&entity_type=...&status=...]. Iterates clusters / nodes / VMs, flattens
// EOL annotations via internal/eolagg, applies optional filters, and
// emits CSV or JSON. The response body is buffered before flushing so
// the X-Longue-Vue-Truncated header can be written before any bytes
// hit the wire — at the row cap, peak buffer is ~10 MB.
//
//nolint:gocyclo // validation branches inflate the score; each branch is short
func HandleEolExtract(store ExtractStore, maxRows int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeRead) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "read scope required")
			return
		}
		q := r.URL.Query()
		format := q.Get("format")
		if format != "csv" && format != "json" {
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "eol", "format": format, "outcome": "denied",
			})
			metrics.ObserveExtract("eol", format, "denied", 0)
			writeProblem(w, http.StatusBadRequest, "Bad Request", "format must be 'csv' or 'json'")
			return
		}
		entityType := q.Get("entity_type")
		if entityType != "" && entityType != "cluster" && entityType != "node" && entityType != "vm" {
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "eol", "format": format, "outcome": "denied",
			})
			metrics.ObserveExtract("eol", format, "denied", 0)
			writeProblem(w, http.StatusBadRequest, "Bad Request", "entity_type must be 'cluster', 'node', or 'vm'")
			return
		}
		status := q.Get("status")
		if status != "" && status != "eol" && status != "approaching_eol" && status != "supported" && status != "unknown" {
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "eol", "format": format, "outcome": "denied",
			})
			metrics.ObserveExtract("eol", format, "denied", 0)
			writeProblem(w, http.StatusBadRequest, "Bad Request", "status must be 'eol', 'approaching_eol', 'supported', or 'unknown'")
			return
		}

		clusters, err := collectAllClusters(r.Context(), store)
		if err != nil {
			slog.Error("extract: list clusters", slog.Any("error", err))
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "eol", "format": format, "outcome": "error",
			})
			metrics.ObserveExtract("eol", format, "error", 0)
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		nodes, err := collectAllNodes(r.Context(), store)
		if err != nil {
			slog.Error("extract: list nodes", slog.Any("error", err))
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "eol", "format": format, "outcome": "error",
			})
			metrics.ObserveExtract("eol", format, "error", 0)
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		vms, err := collectAllVMs(r.Context(), store, VirtualMachineListFilter{})
		if err != nil {
			slog.Error("extract: list virtual machines", slog.Any("error", err))
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "eol", "format": format, "outcome": "error",
			})
			metrics.ObserveExtract("eol", format, "error", 0)
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		rows := eolagg.Flatten(toEolaggClusters(clusters), toEolaggNodes(nodes), toEolaggVMs(vms))
		if entityType != "" {
			filtered := rows[:0]
			for _, row := range rows {
				if row.EntityType == entityType {
					filtered = append(filtered, row)
				}
			}
			rows = filtered
		}
		if status != "" {
			filtered := rows[:0]
			for _, row := range rows {
				if row.Status == status {
					filtered = append(filtered, row)
				}
			}
			rows = filtered
		}

		truncated := false
		if len(rows) > maxRows {
			rows = rows[:maxRows]
			truncated = true
		}

		var buf bytes.Buffer
		switch format {
		case "csv":
			cw := newExtractCSVWriter(&buf, eolCSVHeader())
			for _, row := range rows {
				if err := cw.WriteRow(eolRowToCSV(row)); err != nil {
					slog.Error("extract: csv row", slog.Any("error", err))
					writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
					return
				}
			}
			if err := cw.Close(); err != nil {
				slog.Error("extract: csv close", slog.Any("error", err))
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
				return
			}
		case "json":
			jw := newExtractJSONWriter(&buf)
			for _, row := range rows {
				if err := jw.WriteRow(row); err != nil {
					slog.Error("extract: json row", slog.Any("error", err))
					writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
					return
				}
			}
			if err := jw.Close(); err != nil {
				slog.Error("extract: json close", slog.Any("error", err))
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
				return
			}
		}

		outcome := "ok"
		if truncated {
			outcome = "truncated"
			w.Header().Set("X-Longue-Vue-Truncated", "true")
		}
		filename := fmt.Sprintf("longue-vue-eol-%s.%s", extractTimestamp(time.Now()), format)
		w.Header().Set("Content-Type", extractContentType(format))
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())

		SetAuditDetails(r.Context(), map[string]any{
			"action":      "extract",
			"page":        "eol",
			"format":      format,
			"entity_type": entityType,
			"status":      status,
			"row_count":   len(rows),
			"truncated":   truncated,
			"outcome":     outcome,
		})
		metrics.ObserveExtract("eol", format, outcome, len(rows))
	}
}

func extractContentType(format string) string {
	switch format {
	case "csv":
		return "text/csv; charset=utf-8"
	case "json":
		return "application/json; charset=utf-8"
	case "zip":
		return "application/zip"
	}
	return "application/octet-stream"
}

func eolCSVHeader() []string {
	return []string{
		"entity_type", "entity_id", "entity_name", "cluster",
		"product", "cycle", "status", "eol_date",
		"latest", "latest_available", "support", "checked_at",
	}
}

func eolRowToCSV(r eolagg.Row) []string {
	return []string{
		r.EntityType, r.EntityID, r.EntityName, r.Cluster,
		r.Product, r.Cycle, r.Status, r.EOLDate,
		r.Latest, r.LatestAvailable, r.Support, r.CheckedAt,
	}
}

func collectAllClusters(ctx context.Context, store ExtractStore) ([]Cluster, error) {
	var out []Cluster
	cursor := ""
	for {
		items, next, err := store.ListClusters(ctx, extractPageSize, cursor, false)
		if err != nil {
			return nil, fmt.Errorf("listClusters: %w", err)
		}
		out = append(out, items...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

func collectAllNodes(ctx context.Context, store ExtractStore) ([]Node, error) {
	var out []Node
	cursor := ""
	for {
		items, next, err := store.ListNodes(ctx, nil, extractPageSize, cursor, false)
		if err != nil {
			return nil, fmt.Errorf("listNodes: %w", err)
		}
		out = append(out, items...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

func collectAllVMs(ctx context.Context, store ExtractStore, filter VirtualMachineListFilter) ([]VirtualMachine, error) {
	var out []VirtualMachine
	cursor := ""
	for {
		items, next, err := store.ListVirtualMachines(ctx, filter, extractPageSize, cursor)
		if err != nil {
			return nil, fmt.Errorf("listVirtualMachines: %w", err)
		}
		out = append(out, items...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

func toEolaggClusters(in []Cluster) []eolagg.ClusterInput {
	out := make([]eolagg.ClusterInput, len(in))
	for i, c := range in {
		id := ""
		if c.Id != nil {
			id = c.Id.String()
		}
		display := ""
		if c.DisplayName != nil {
			display = *c.DisplayName
		}
		ann := map[string]string{}
		if c.Annotations != nil {
			ann = *c.Annotations
		}
		out[i] = eolagg.ClusterInput{
			ID: id, Name: c.Name, DisplayName: display, Annotations: ann,
		}
	}
	return out
}

func toEolaggNodes(in []Node) []eolagg.NodeInput {
	out := make([]eolagg.NodeInput, len(in))
	for i, n := range in {
		id := ""
		if n.Id != nil {
			id = n.Id.String()
		}
		ann := map[string]string{}
		if n.Annotations != nil {
			ann = *n.Annotations
		}
		out[i] = eolagg.NodeInput{
			ID: id, Name: n.Name, ClusterID: n.ClusterId.String(), Annotations: ann,
		}
	}
	return out
}

func toEolaggVMs(in []VirtualMachine) []eolagg.VMInput {
	out := make([]eolagg.VMInput, len(in))
	for i, v := range in {
		display := ""
		if v.DisplayName != nil {
			display = *v.DisplayName
		}
		out[i] = eolagg.VMInput{
			ID: v.ID.String(), Name: v.Name, DisplayName: display, Annotations: v.Annotations,
		}
	}
	return out
}

// Stubs — Task 9 / Task 10 will implement these.

// HandleSearchExtract is a stub; implementation is Task 9.
func HandleSearchExtract(_ ExtractStore, _ int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, http.StatusNotImplemented, "Not Implemented", "search extract is implemented in Task 9")
	}
}

// HandleSearchExtractZip is a stub; implementation is Task 10.
func HandleSearchExtractZip(_ ExtractStore, _ int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, http.StatusNotImplemented, "Not Implemented", "zip extract is implemented in Task 10")
	}
}
