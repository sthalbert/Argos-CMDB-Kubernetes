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

// extractQueryMax is the maximum byte-length accepted for the ?q= parameter.
const extractQueryMax = 256

// extractServerVersion is the placeholder version label written into the
// extract ZIP README. main.go can later override it via a setter if we
// wire build info through.
var extractServerVersion = "longue-vue"

// string constants reused across validation branches.
const (
	extractFormatCSV        = "csv"
	extractFormatJSON       = "json"
	extractOutcomeTruncated = "truncated"
	extractStatusUnknown    = "unknown"
	extractKindWorkloads    = "workloads"
	extractKindPods         = "pods"
	extractKindVMs          = "virtual_machines"
)

// HandleEolExtract — read scope. GET /v1/eol/extract?format=csv|json
// [&entity_type=...&status=...]. Iterates clusters / nodes / VMs, flattens
// EOL annotations via internal/eolagg, applies optional filters, and
// emits CSV or JSON. The response body is buffered before flushing so
// the X-Longue-Vue-Truncated header can be written before any bytes
// hit the wire — at the row cap, peak buffer is ~10 MB.
//
//nolint:gocyclo,gocognit // validation + data-collection branches inflate the score; each branch is short and self-contained
func HandleEolExtract(store ExtractStore, maxRows int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeRead) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "read scope required")
			return
		}
		q := r.URL.Query()
		format := q.Get("format")
		if format != extractFormatCSV && format != extractFormatJSON {
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
		if status != "" && status != "eol" && status != "approaching_eol" && status != "supported" && status != extractStatusUnknown {
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
			for i := range rows {
				if rows[i].EntityType == entityType {
					filtered = append(filtered, rows[i])
				}
			}
			rows = filtered
		}
		if status != "" {
			filtered := rows[:0]
			for i := range rows {
				if rows[i].Status == status {
					filtered = append(filtered, rows[i])
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
		case extractFormatCSV:
			cw := newExtractCSVWriter(&buf, eolCSVHeader())
			for i := range rows {
				if err := cw.WriteRow(eolRowToCSV(rows[i])); err != nil {
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
		case extractFormatJSON:
			jw := newExtractJSONWriter(&buf)
			for i := range rows {
				if err := jw.WriteRow(rows[i]); err != nil {
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
			outcome = extractOutcomeTruncated
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
	case extractFormatCSV:
		return "text/csv; charset=utf-8"
	case extractFormatJSON:
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

func eolRowToCSV(
	r eolagg.Row, //nolint:gocritic // hugeParam: Row (192 bytes) passed by value; called from a range-by-index loop, value semantics are intentional
) []string {
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

func collectAllVMs(
	ctx context.Context,
	store ExtractStore,
	filter VirtualMachineListFilter, //nolint:gocritic // hugeParam: 80 bytes passed by value; callers always construct inline literals
) ([]VirtualMachine, error) {
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
	for i := range in {
		id := ""
		if in[i].Id != nil {
			id = in[i].Id.String()
		}
		display := ""
		if in[i].DisplayName != nil {
			display = *in[i].DisplayName
		}
		ann := map[string]string{}
		if in[i].Annotations != nil {
			ann = *in[i].Annotations
		}
		out[i] = eolagg.ClusterInput{
			ID: id, Name: in[i].Name, DisplayName: display, Annotations: ann,
		}
	}
	return out
}

func toEolaggNodes(in []Node) []eolagg.NodeInput {
	out := make([]eolagg.NodeInput, len(in))
	for i := range in {
		id := ""
		if in[i].Id != nil {
			id = in[i].Id.String()
		}
		ann := map[string]string{}
		if in[i].Annotations != nil {
			ann = *in[i].Annotations
		}
		out[i] = eolagg.NodeInput{
			ID: id, Name: in[i].Name, ClusterID: in[i].ClusterId.String(), Annotations: ann,
		}
	}
	return out
}

func toEolaggVMs(in []VirtualMachine) []eolagg.VMInput {
	out := make([]eolagg.VMInput, len(in))
	for i := range in {
		display := ""
		if in[i].DisplayName != nil {
			display = *in[i].DisplayName
		}
		out[i] = eolagg.VMInput{
			ID: in[i].ID.String(), Name: in[i].Name, DisplayName: display, Annotations: in[i].Annotations,
		}
	}
	return out
}

// HandleSearchExtract — read scope. GET /v1/search/extract?q=...&kind=workloads|pods|virtual_machines&format=csv|json
// Searches container images (workloads/pods) or VM image/application (virtual_machines).
//
//nolint:gocyclo,gocognit // dispatch + validation branches inflate the score; each branch is short and self-contained
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
		if len(searchQ) > extractQueryMax {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "q must be ≤ 256 characters")
			return
		}
		kind := q.Get("kind")
		if kind != extractKindWorkloads && kind != extractKindPods && kind != extractKindVMs {
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "search", "kind": kind, "outcome": "denied",
			})
			metrics.ObserveExtract("search", "", "denied", 0)
			writeProblem(w, http.StatusBadRequest, "Bad Request", "kind must be 'workloads', 'pods', or 'virtual_machines'")
			return
		}
		format := q.Get("format")
		if format != extractFormatCSV && format != extractFormatJSON {
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
		case extractKindWorkloads:
			rows, objs, err = collectWorkloadExtract(r.Context(), store, searchQ)
		case extractKindPods:
			rows, objs, err = collectPodExtract(r.Context(), store, searchQ)
		case extractKindVMs:
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
		case extractFormatCSV:
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
		case extractFormatJSON:
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
			outcome = extractOutcomeTruncated
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
	case extractKindWorkloads:
		return []string{"cluster", "namespace", "kind", "name", "image_matches", "replicas", "ready_replicas", "updated_at"}
	case extractKindPods:
		return []string{"cluster", "namespace", "name", "workload_kind", "workload_name", "image_matches", "phase", "node", "updated_at"}
	case extractKindVMs:
		return []string{
			"cloud_account", "region", "name", "display_name", "role",
			"power_state", "image_id", "image_name", "applications_matched", "updated_at",
		}
	}
	return nil
}

func kindFilenameSegment(kind string) string {
	if kind == extractKindVMs {
		return "virtual-machines"
	}
	return kind
}

// loadClusterNamespaceIndex fetches all clusters and namespaces and returns
// lookup maps keyed by UUID.
//
//nolint:gocyclo // two sequential paginated fetches; complexity from nil-guard + map build, not from branching logic
func loadClusterNamespaceIndex(
	ctx context.Context,
	store ExtractStore,
) (clusterByID map[uuid.UUID]string, nsByID map[uuid.UUID]Namespace, _ error) {
	clusters, err := collectAllClusters(ctx, store)
	if err != nil {
		return nil, nil, fmt.Errorf("loadClusterNamespaceIndex clusters: %w", err)
	}
	clusterByID = make(map[uuid.UUID]string, len(clusters))
	for i := range clusters {
		if clusters[i].Id == nil {
			continue
		}
		name := clusters[i].Name
		if clusters[i].DisplayName != nil && *clusters[i].DisplayName != "" {
			name = *clusters[i].DisplayName
		}
		clusterByID[*clusters[i].Id] = name
	}

	nsByID = make(map[uuid.UUID]Namespace)
	cursor := ""
	for {
		items, next, err := store.ListNamespaces(ctx, nil, extractPageSize, cursor, false)
		if err != nil {
			return nil, nil, fmt.Errorf("loadClusterNamespaceIndex namespaces: %w", err)
		}
		for i := range items {
			if items[i].Id != nil {
				nsByID[*items[i].Id] = items[i]
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
	return clusterByID[ns.ClusterId], ns.Name
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

func collectWorkloadExtract(
	ctx context.Context,
	store ExtractStore,
	q string,
) (rows [][]string, objs []any, _ error) {
	clusterByID, nsByID, err := loadClusterNamespaceIndex(ctx, store)
	if err != nil {
		return nil, nil, err
	}

	cursor := ""
	for {
		items, next, err := store.ListWorkloads(ctx, WorkloadListFilter{ImageSubstring: &q}, extractPageSize, cursor)
		if err != nil {
			return nil, nil, fmt.Errorf("collectWorkloadExtract: %w", err)
		}
		for i := range items {
			w := &items[i]
			cluster, namespace := lookupClusterNamespace(w.NamespaceId, nsByID, clusterByID)
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
				id = w.Id.String()
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

//
//nolint:gocyclo // two lookups (cluster/ns + workload) plus pod iteration; complexity is inherent not accidental
func collectPodExtract(
	ctx context.Context,
	store ExtractStore,
	q string,
) (rows [][]string, objs []any, _ error) {
	clusterByID, nsByID, err := loadClusterNamespaceIndex(ctx, store)
	if err != nil {
		return nil, nil, err
	}

	allWorkloads, err := collectAllWorkloads(ctx, store)
	if err != nil {
		return nil, nil, err
	}
	workloadByID := make(map[uuid.UUID]Workload, len(allWorkloads))
	for i := range allWorkloads {
		if allWorkloads[i].Id != nil {
			workloadByID[*allWorkloads[i].Id] = allWorkloads[i]
		}
	}

	cursor := ""
	for {
		items, next, err := store.ListPods(ctx, PodListFilter{ImageSubstring: &q}, extractPageSize, cursor)
		if err != nil {
			return nil, nil, fmt.Errorf("collectPodExtract: %w", err)
		}
		for i := range items {
			p := &items[i]
			cluster, namespace := lookupClusterNamespace(p.NamespaceId, nsByID, clusterByID)
			var workloadKind, workloadName string
			if p.WorkloadId != nil {
				if wl, ok := workloadByID[*p.WorkloadId]; ok {
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
				id = p.Id.String()
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

func collectVMExtract(
	ctx context.Context,
	store ExtractStore,
	q string,
) (rows [][]string, objs []any, _ error) {
	accounts, err := collectAllCloudAccounts(ctx, store)
	if err != nil {
		return nil, nil, err
	}
	accountNameByID := make(map[uuid.UUID]string, len(accounts))
	for i := range accounts {
		accountNameByID[accounts[i].ID] = accounts[i].Name
	}

	// Union by-image and by-application results.
	union := make(map[uuid.UUID]VirtualMachine)
	byImage, err := collectAllVMs(ctx, store, VirtualMachineListFilter{Image: &q})
	if err != nil {
		return nil, nil, fmt.Errorf("collectVMExtract image: %w", err)
	}
	for i := range byImage {
		union[byImage[i].ID] = byImage[i]
	}
	byApp, err := collectAllVMs(ctx, store, VirtualMachineListFilter{Application: &q})
	if err != nil {
		return nil, nil, fmt.Errorf("collectVMExtract application: %w", err)
	}
	for i := range byApp {
		union[byApp[i].ID] = byApp[i]
	}

	qLower := strings.ToLower(q)
	for _, v := range union { //nolint:gocritic // rangeValCopy: VirtualMachine (512 bytes) — map range copy is unavoidable without a pointer map
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

// HandleSearchExtractZip — read scope. GET /v1/search/extract.zip?q=...
// Returns a ZIP bundle of workloads.csv + pods.csv + virtual_machines.csv + README.txt.
// Truncation is a per-CSV decision; if any of the three exceeds maxRows
// it is truncated independently and X-Longue-Vue-Truncated is set on
// the response. Row counts in the README reflect the truncated counts.
//
//nolint:gocyclo,gocognit // three sequential collect+emit calls with shared error-coalescing; complexity is inherent
func HandleSearchExtractZip(store ExtractStore, maxRows int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeRead) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "read scope required")
			return
		}
		q := r.URL.Query().Get("q")
		if q == "" {
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "search", "format": "zip", "outcome": "denied",
			})
			metrics.ObserveExtract("search", "zip", "denied", 0)
			writeProblem(w, http.StatusBadRequest, "Bad Request", "q is required")
			return
		}
		if len(q) > extractQueryMax {
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "search", "format": "zip", "outcome": "denied",
			})
			metrics.ObserveExtract("search", "zip", "denied", 0)
			writeProblem(w, http.StatusBadRequest, "Bad Request", "q is longer than 256 characters")
			return
		}

		generated := time.Now()
		var buf bytes.Buffer
		zw := newExtractZIPWriter(&buf, extractZIPMeta{
			Query: q, GeneratedAt: generated, Version: extractServerVersion,
		})

		totalRows := 0
		truncated := false

		emit := func(name string, header []string, rows [][]string) error {
			if len(rows) > maxRows {
				rows = rows[:maxRows]
				truncated = true
			}
			cw, err := zw.AddCSV(name, header)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if err := cw.WriteRow(row); err != nil {
					return err
				}
			}
			if err := cw.Close(); err != nil {
				return err
			}
			zw.SetRowCount(name, len(rows))
			totalRows += len(rows)
			return nil
		}

		wlRows, _, err := collectWorkloadExtract(r.Context(), store, q)
		if err == nil {
			err = emit("workloads.csv", searchCSVHeader(extractKindWorkloads), wlRows)
		}
		if err == nil {
			var podRows [][]string
			podRows, _, err = collectPodExtract(r.Context(), store, q)
			if err == nil {
				err = emit("pods.csv", searchCSVHeader(extractKindPods), podRows)
			}
		}
		if err == nil {
			var vmRows [][]string
			vmRows, _, err = collectVMExtract(r.Context(), store, q)
			if err == nil {
				err = emit("virtual_machines.csv", searchCSVHeader("virtual_machines"), vmRows)
			}
		}
		if err == nil {
			err = zw.Close()
		}
		if err != nil {
			slog.Error("extract: zip", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "search", "format": "zip", "outcome": "error",
			})
			metrics.ObserveExtract("search", "zip", "error", 0)
			return
		}

		outcome := "ok"
		if truncated {
			outcome = extractOutcomeTruncated
			w.Header().Set("X-Longue-Vue-Truncated", "true")
		}
		filename := fmt.Sprintf("longue-vue-search-%s-%s.zip", slugForFilename(q), extractTimestamp(generated))
		w.Header().Set("Content-Type", extractContentType("zip"))
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())

		SetAuditDetails(r.Context(), map[string]any{
			"action": "extract", "page": "search", "format": "zip", "q": q,
			"row_count": totalRows, "truncated": truncated, "outcome": outcome,
		})
		metrics.ObserveExtract("search", "zip", outcome, totalRows)
	}
}
