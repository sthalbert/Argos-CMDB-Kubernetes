package api

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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

// HandleSearchExtract — read scope. GET /v1/search/extract?q=...&kind=workloads|pods|virtual_machines&format=csv|json
// Searches container images (workloads/pods) or VM image/application (virtual_machines).
//
//nolint:gocyclo // dispatch + validation branches; each is short
func HandleSearchExtract(store ExtractStore, maxRows int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeRead) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "read scope required")
			return
		}
		q := r.URL.Query()
		searchQ := q.Get("q")
		if searchQ == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "q is required")
			return
		}
		if len(searchQ) > 256 {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "q must be ≤ 256 characters")
			return
		}
		kind := q.Get("kind")
		if kind != "workloads" && kind != "pods" && kind != "virtual_machines" {
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "search", "kind": kind, "outcome": "denied",
			})
			metrics.ObserveExtract("search", "", "denied", 0)
			writeProblem(w, http.StatusBadRequest, "Bad Request", "kind must be 'workloads', 'pods', or 'virtual_machines'")
			return
		}
		format := q.Get("format")
		if format != "csv" && format != "json" {
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "search", "kind": kind, "format": format, "outcome": "denied",
			})
			metrics.ObserveExtract("search", format, "denied", 0)
			writeProblem(w, http.StatusBadRequest, "Bad Request", "format must be 'csv' or 'json'")
			return
		}

		var (
			rows [][]string
			objs []any
			err  error
		)
		switch kind {
		case "workloads":
			rows, objs, err = collectWorkloadExtract(r.Context(), store, searchQ)
		case "pods":
			rows, objs, err = collectPodExtract(r.Context(), store, searchQ)
		case "virtual_machines":
			rows, objs, err = collectVMExtract(r.Context(), store, searchQ)
		}
		if err != nil {
			slog.Error("extract: search collect", slog.String("kind", kind), slog.Any("error", err))
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "search", "kind": kind, "format": format, "outcome": "error",
			})
			metrics.ObserveExtract("search", format, "error", 0)
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		truncated := false
		if len(rows) > maxRows {
			rows = rows[:maxRows]
			objs = objs[:maxRows]
			truncated = true
		}

		var buf bytes.Buffer
		switch format {
		case "csv":
			header := searchCSVHeader(kind)
			cw := newExtractCSVWriter(&buf, header)
			for _, row := range rows {
				if err := cw.WriteRow(row); err != nil {
					slog.Error("extract: search csv row", slog.Any("error", err))
					writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
					return
				}
			}
			if err := cw.Close(); err != nil {
				slog.Error("extract: search csv close", slog.Any("error", err))
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
				return
			}
		case "json":
			jw := newExtractJSONWriter(&buf)
			for _, obj := range objs {
				if err := jw.WriteRow(obj); err != nil {
					slog.Error("extract: search json row", slog.Any("error", err))
					writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
					return
				}
			}
			if err := jw.Close(); err != nil {
				slog.Error("extract: search json close", slog.Any("error", err))
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
				return
			}
		}

		outcome := "ok"
		if truncated {
			outcome = "truncated"
			w.Header().Set("X-Longue-Vue-Truncated", "true")
		}
		kindSeg := kindFilenameSegment(kind)
		ts := extractTimestamp(time.Now())
		filename := fmt.Sprintf("longue-vue-search-%s-%s-%s.%s", kindSeg, slugForFilename(searchQ), ts, format)
		w.Header().Set("Content-Type", extractContentType(format))
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())

		SetAuditDetails(r.Context(), map[string]any{
			"action":    "extract",
			"page":      "search",
			"kind":      kind,
			"format":    format,
			"q":         searchQ,
			"row_count": len(rows),
			"truncated": truncated,
			"outcome":   outcome,
		})
		metrics.ObserveExtract("search", format, outcome, len(rows))
	}
}

func searchCSVHeader(kind string) []string {
	switch kind {
	case "workloads":
		return []string{"cluster", "namespace", "kind", "name", "image_matches", "replicas", "ready_replicas", "updated_at"}
	case "pods":
		return []string{"cluster", "namespace", "name", "workload_kind", "workload_name", "image_matches", "phase", "node", "updated_at"}
	case "virtual_machines":
		return []string{"cloud_account", "region", "name", "display_name", "role", "power_state", "image_id", "image_name", "applications_matched", "updated_at"}
	}
	return nil
}

func kindFilenameSegment(kind string) string {
	if kind == "virtual_machines" {
		return "virtual-machines"
	}
	return kind
}

// loadClusterNamespaceIndex fetches all clusters and namespaces and returns
// lookup maps keyed by UUID.
func loadClusterNamespaceIndex(ctx context.Context, store ExtractStore) (map[uuid.UUID]string, map[uuid.UUID]Namespace, error) {
	clusters, err := collectAllClusters(ctx, store)
	if err != nil {
		return nil, nil, fmt.Errorf("loadClusterNamespaceIndex clusters: %w", err)
	}
	clusterByID := make(map[uuid.UUID]string, len(clusters))
	for _, c := range clusters {
		if c.Id == nil {
			continue
		}
		name := c.Name
		if c.DisplayName != nil && *c.DisplayName != "" {
			name = *c.DisplayName
		}
		clusterByID[uuid.UUID(*c.Id)] = name
	}

	var nsByID map[uuid.UUID]Namespace
	nsByID = make(map[uuid.UUID]Namespace)
	cursor := ""
	for {
		items, next, err := store.ListNamespaces(ctx, nil, extractPageSize, cursor, false)
		if err != nil {
			return nil, nil, fmt.Errorf("loadClusterNamespaceIndex namespaces: %w", err)
		}
		for _, ns := range items {
			if ns.Id != nil {
				nsByID[uuid.UUID(*ns.Id)] = ns
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return clusterByID, nsByID, nil
}

func lookupClusterNamespace(nsID uuid.UUID, nsByID map[uuid.UUID]Namespace, clusterByID map[uuid.UUID]string) (cluster, namespace string) {
	ns, ok := nsByID[nsID]
	if !ok {
		return "", nsID.String()
	}
	return clusterByID[uuid.UUID(ns.ClusterId)], ns.Name
}

func joinMatchedImages(list *ContainerList, q string) string {
	if list == nil {
		return ""
	}
	qLower := strings.ToLower(q)
	var hits []string
	for _, c := range *list {
		img, ok := c["image"].(string)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(img), qLower) {
			hits = append(hits, img)
		}
	}
	return strings.Join(hits, ";")
}

func joinMatchedApplications(apps []VMApplication, qLower string) string {
	var hits []string
	for _, app := range apps {
		if strings.Contains(strings.ToLower(app.Product), qLower) {
			hits = append(hits, app.Product)
		}
	}
	return strings.Join(hits, ";")
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefIntStr(i *int) string {
	if i == nil {
		return ""
	}
	return fmt.Sprintf("%d", *i)
}

func derefTimeStr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func collectAllWorkloads(ctx context.Context, store ExtractStore) ([]Workload, error) {
	var out []Workload
	cursor := ""
	for {
		items, next, err := store.ListWorkloads(ctx, WorkloadListFilter{}, extractPageSize, cursor)
		if err != nil {
			return nil, fmt.Errorf("collectAllWorkloads: %w", err)
		}
		out = append(out, items...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

func collectAllCloudAccounts(ctx context.Context, store ExtractStore) ([]CloudAccount, error) {
	var out []CloudAccount
	cursor := ""
	for {
		items, next, err := store.ListCloudAccounts(ctx, extractPageSize, cursor)
		if err != nil {
			return nil, fmt.Errorf("collectAllCloudAccounts: %w", err)
		}
		out = append(out, items...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

func collectWorkloadExtract(ctx context.Context, store ExtractStore, q string) ([][]string, []any, error) {
	clusterByID, nsByID, err := loadClusterNamespaceIndex(ctx, store)
	if err != nil {
		return nil, nil, err
	}

	var rows [][]string
	var objs []any
	cursor := ""
	for {
		items, next, err := store.ListWorkloads(ctx, WorkloadListFilter{ImageSubstring: &q}, extractPageSize, cursor)
		if err != nil {
			return nil, nil, fmt.Errorf("collectWorkloadExtract: %w", err)
		}
		for _, w := range items {
			cluster, namespace := lookupClusterNamespace(uuid.UUID(w.NamespaceId), nsByID, clusterByID)
			imageMatches := joinMatchedImages(w.Containers, q)
			replicas := derefIntStr(w.Replicas)
			readyReplicas := derefIntStr(w.ReadyReplicas)
			updatedAt := derefTimeStr(w.UpdatedAt)
			rows = append(rows, []string{
				cluster, namespace, string(w.Kind), w.Name,
				imageMatches, replicas, readyReplicas, updatedAt,
			})
			id := ""
			if w.Id != nil {
				id = uuid.UUID(*w.Id).String()
			}
			var replicasInt *int
			if w.Replicas != nil {
				v := *w.Replicas
				replicasInt = &v
			}
			var readyReplicasInt *int
			if w.ReadyReplicas != nil {
				v := *w.ReadyReplicas
				readyReplicasInt = &v
			}
			var updatedAtTime *time.Time
			if w.UpdatedAt != nil {
				v := w.UpdatedAt.UTC()
				updatedAtTime = &v
			}
			objs = append(objs, map[string]any{
				"id":             id,
				"cluster":        cluster,
				"namespace":      namespace,
				"kind":           string(w.Kind),
				"name":           w.Name,
				"image_matches":  imageMatches,
				"replicas":       replicasInt,
				"ready_replicas": readyReplicasInt,
				"updated_at":     updatedAtTime,
			})
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return rows, objs, nil
}

func collectPodExtract(ctx context.Context, store ExtractStore, q string) ([][]string, []any, error) {
	clusterByID, nsByID, err := loadClusterNamespaceIndex(ctx, store)
	if err != nil {
		return nil, nil, err
	}

	allWorkloads, err := collectAllWorkloads(ctx, store)
	if err != nil {
		return nil, nil, err
	}
	workloadByID := make(map[uuid.UUID]Workload, len(allWorkloads))
	for _, w := range allWorkloads {
		if w.Id != nil {
			workloadByID[uuid.UUID(*w.Id)] = w
		}
	}

	var rows [][]string
	var objs []any
	cursor := ""
	for {
		items, next, err := store.ListPods(ctx, PodListFilter{ImageSubstring: &q}, extractPageSize, cursor)
		if err != nil {
			return nil, nil, fmt.Errorf("collectPodExtract: %w", err)
		}
		for _, p := range items {
			cluster, namespace := lookupClusterNamespace(uuid.UUID(p.NamespaceId), nsByID, clusterByID)
			var workloadKind, workloadName string
			if p.WorkloadId != nil {
				if wl, ok := workloadByID[uuid.UUID(*p.WorkloadId)]; ok {
					workloadKind = string(wl.Kind)
					workloadName = wl.Name
				}
			}
			imageMatches := joinMatchedImages(p.Containers, q)
			phase := derefStr(p.Phase)
			node := derefStr(p.NodeName)
			updatedAt := derefTimeStr(p.UpdatedAt)
			rows = append(rows, []string{
				cluster, namespace, p.Name, workloadKind, workloadName,
				imageMatches, phase, node, updatedAt,
			})
			id := ""
			if p.Id != nil {
				id = uuid.UUID(*p.Id).String()
			}
			objs = append(objs, map[string]any{
				"id":            id,
				"cluster":       cluster,
				"namespace":     namespace,
				"name":          p.Name,
				"workload_kind": workloadKind,
				"workload_name": workloadName,
				"image_matches": imageMatches,
				"phase":         phase,
				"node":          node,
				"updated_at":    derefTimeStr(p.UpdatedAt),
			})
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return rows, objs, nil
}

func collectVMExtract(ctx context.Context, store ExtractStore, q string) ([][]string, []any, error) {
	accounts, err := collectAllCloudAccounts(ctx, store)
	if err != nil {
		return nil, nil, err
	}
	accountNameByID := make(map[uuid.UUID]string, len(accounts))
	for _, a := range accounts {
		accountNameByID[a.ID] = a.Name
	}

	// Union by-image and by-application results.
	union := make(map[uuid.UUID]VirtualMachine)
	byImage, err := collectAllVMs(ctx, store, VirtualMachineListFilter{Image: &q})
	if err != nil {
		return nil, nil, fmt.Errorf("collectVMExtract image: %w", err)
	}
	for _, v := range byImage {
		union[v.ID] = v
	}
	byApp, err := collectAllVMs(ctx, store, VirtualMachineListFilter{Application: &q})
	if err != nil {
		return nil, nil, fmt.Errorf("collectVMExtract application: %w", err)
	}
	for _, v := range byApp {
		union[v.ID] = v
	}

	qLower := strings.ToLower(q)
	var rows [][]string
	var objs []any
	for _, v := range union {
		account := accountNameByID[v.CloudAccountID]
		if account == "" {
			account = v.CloudAccountID.String()
		}
		appsMatched := joinMatchedApplications(v.Applications, qLower)
		updatedAt := v.UpdatedAt.UTC().Format(time.RFC3339)
		rows = append(rows, []string{
			account,
			derefStr(v.Region),
			v.Name,
			derefStr(v.DisplayName),
			derefStr(v.Role),
			v.PowerState,
			derefStr(v.ImageID),
			derefStr(v.ImageName),
			appsMatched,
			updatedAt,
		})
		objs = append(objs, map[string]any{
			"id":                   v.ID.String(),
			"cloud_account":        account,
			"region":               derefStr(v.Region),
			"name":                 v.Name,
			"display_name":         derefStr(v.DisplayName),
			"role":                 derefStr(v.Role),
			"power_state":          v.PowerState,
			"image_id":             derefStr(v.ImageID),
			"image_name":           derefStr(v.ImageName),
			"applications_matched": appsMatched,
			"updated_at":           updatedAt,
		})
	}
	return rows, objs, nil
}

// HandleSearchExtractZip is a stub; implementation is Task 10.
func HandleSearchExtractZip(_ ExtractStore, _ int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, http.StatusNotImplemented, "Not Implemented", "zip extract is implemented in Task 10")
	}
}
