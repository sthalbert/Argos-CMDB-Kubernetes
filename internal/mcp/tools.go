//nolint:gocritic // MCP SDK handler signature passes CallToolRequest by value.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/impact"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

var errRequiredField = errors.New("required field missing")

// registerTools adds all read-only CMDB tools to the MCP server.
//
//nolint:funlen // registering 17 tools in one function is inherently long.
func (s *Server) registerTools() {
	s.mcp.AddTool(
		mcp.NewTool("list_clusters",
			mcp.WithDescription("List all Kubernetes clusters in the CMDB with their version, provider, region, and EOL status"),
			mcp.WithString("name", mcp.Description("Filter by cluster name substring (optional)")),
		),
		s.handleListClusters,
	)

	s.mcp.AddTool(
		mcp.NewTool("get_cluster",
			mcp.WithDescription("Get a single cluster by its UUID"),
			mcp.WithString("id", mcp.Required(), mcp.Description("Cluster UUID")),
		),
		s.handleGetCluster,
	)

	s.mcp.AddTool(
		mcp.NewTool("list_nodes",
			mcp.WithDescription("List Kubernetes nodes, optionally filtered by cluster"),
			mcp.WithString("cluster_id", mcp.Description("Filter by cluster UUID (optional)")),
		),
		s.handleListNodes,
	)

	s.mcp.AddTool(
		mcp.NewTool("get_node",
			mcp.WithDescription("Get a single node by its UUID"),
			mcp.WithString("id", mcp.Required(), mcp.Description("Node UUID")),
		),
		s.handleGetNode,
	)

	s.mcp.AddTool(
		mcp.NewTool("list_namespaces",
			mcp.WithDescription("List Kubernetes namespaces, optionally filtered by cluster"),
			mcp.WithString("cluster_id", mcp.Description("Filter by cluster UUID (optional)")),
		),
		s.handleListNamespaces,
	)

	s.mcp.AddTool(
		mcp.NewTool("get_namespace",
			mcp.WithDescription("Get a single namespace by its UUID"),
			mcp.WithString("id", mcp.Required(), mcp.Description("Namespace UUID")),
		),
		s.handleGetNamespace,
	)

	s.mcp.AddTool(
		mcp.NewTool("list_workloads",
			mcp.WithDescription("List workloads (Deployments, StatefulSets, DaemonSets), optionally filtered"),
			mcp.WithString("namespace_id", mcp.Description("Filter by namespace UUID (optional)")),
			mcp.WithString("kind", mcp.Description("Filter by workload kind: Deployment, StatefulSet, DaemonSet (optional)")),
			mcp.WithString("image", mcp.Description("Filter by container image substring (optional)")),
		),
		s.handleListWorkloads,
	)

	s.mcp.AddTool(
		mcp.NewTool("get_workload",
			mcp.WithDescription("Get a single workload by its UUID"),
			mcp.WithString("id", mcp.Required(), mcp.Description("Workload UUID")),
		),
		s.handleGetWorkload,
	)

	s.mcp.AddTool(
		mcp.NewTool("list_pods",
			mcp.WithDescription("List pods, optionally filtered by namespace, node, workload, or image"),
			mcp.WithString("namespace_id", mcp.Description("Filter by namespace UUID (optional)")),
			mcp.WithString("node_name", mcp.Description("Filter by node name (optional)")),
			mcp.WithString("workload_id", mcp.Description("Filter by owning workload UUID (optional)")),
			mcp.WithString("image", mcp.Description("Filter by container image substring (optional)")),
		),
		s.handleListPods,
	)

	s.mcp.AddTool(
		mcp.NewTool("get_pod",
			mcp.WithDescription("Get a single pod by its UUID"),
			mcp.WithString("id", mcp.Required(), mcp.Description("Pod UUID")),
		),
		s.handleGetPod,
	)

	s.mcp.AddTool(
		mcp.NewTool("list_services",
			mcp.WithDescription("List Kubernetes services, optionally filtered by namespace"),
			mcp.WithString("namespace_id", mcp.Description("Filter by namespace UUID (optional)")),
		),
		s.handleListServices,
	)

	s.mcp.AddTool(
		mcp.NewTool("list_ingresses",
			mcp.WithDescription("List Kubernetes ingresses, optionally filtered by namespace"),
			mcp.WithString("namespace_id", mcp.Description("Filter by namespace UUID (optional)")),
		),
		s.handleListIngresses,
	)

	s.mcp.AddTool(
		mcp.NewTool("list_persistent_volumes",
			mcp.WithDescription("List persistent volumes, optionally filtered by cluster"),
			mcp.WithString("cluster_id", mcp.Description("Filter by cluster UUID (optional)")),
		),
		s.handleListPersistentVolumes,
	)

	s.mcp.AddTool(
		mcp.NewTool("list_persistent_volume_claims",
			mcp.WithDescription("List persistent volume claims, optionally filtered by namespace"),
			mcp.WithString("namespace_id", mcp.Description("Filter by namespace UUID (optional)")),
		),
		s.handleListPersistentVolumeClaims,
	)

	s.mcp.AddTool(
		mcp.NewTool("get_impact_graph",
			mcp.WithDescription("Get the impact/dependency graph for a CMDB entity, showing upstream and downstream relationships"),
			mcp.WithString("entity_type",
				mcp.Required(),
				mcp.Description("Entity type: cluster, node, namespace, pod, workload, service, ingress, persistentvolume, persistentvolumeclaim"),
			),
			mcp.WithString("id", mcp.Required(), mcp.Description("Entity UUID")),
			mcp.WithNumber("depth", mcp.Description("Maximum traversal depth (default 2, max 5)")),
		),
		s.handleGetImpactGraph,
	)

	s.mcp.AddTool(
		mcp.NewTool("get_eol_summary",
			mcp.WithDescription("Get a summary of end-of-life status across all clusters and nodes, with counts by EOL status"),
		),
		s.handleGetEOLSummary,
	)

	s.mcp.AddTool(
		mcp.NewTool("search_images",
			mcp.WithDescription("Search for workloads and pods running a specific container image"),
			mcp.WithString("query", mcp.Required(), mcp.Description("Container image name or substring to search for")),
		),
		s.handleSearchImages,
	)
}

// --- tool handlers ----------------------------------------------------------

func (s *Server) handleListClusters(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"name": presence(request.GetString("name", ""))}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "list_clusters", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "list_clusters", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_clusters", time.Since(start)) }()

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Cluster, string, error) {
		return s.store.ListClusters(ctx, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list clusters: %w", err)
	}

	// Optional name filter.
	if name := request.GetString("name", ""); name != "" {
		filtered := items[:0]
		lower := strings.ToLower(name)
		for _, c := range items {
			if strings.Contains(strings.ToLower(c.Name), lower) {
				filtered = append(filtered, c)
			}
		}
		items = filtered
	}

	return jsonResult(items)
}

func (s *Server) handleGetCluster(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"id": request.GetString("id", "")}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "get_cluster", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "get_cluster", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("get_cluster", time.Since(start)) }()

	id, err := parseID(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	cluster, err := s.store.GetCluster(ctx, id)
	if err != nil {
		return storeError("cluster", err)
	}
	return jsonResult(cluster)
}

func (s *Server) handleListNodes(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"cluster_id": request.GetString("cluster_id", "")}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "list_nodes", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "list_nodes", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_nodes", time.Since(start)) }()

	clusterID, err := parseOptionalUUID(request, "cluster_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Node, string, error) {
		return s.store.ListNodes(ctx, clusterID, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	return jsonResult(items)
}

func (s *Server) handleGetNode(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"id": request.GetString("id", "")}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "get_node", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "get_node", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("get_node", time.Since(start)) }()

	id, err := parseID(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	node, err := s.store.GetNode(ctx, id)
	if err != nil {
		return storeError("node", err)
	}
	return jsonResult(node)
}

func (s *Server) handleListNamespaces(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"cluster_id": request.GetString("cluster_id", "")}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "list_namespaces", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "list_namespaces", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_namespaces", time.Since(start)) }()

	clusterID, err := parseOptionalUUID(request, "cluster_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Namespace, string, error) {
		return s.store.ListNamespaces(ctx, clusterID, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	return jsonResult(items)
}

func (s *Server) handleGetNamespace(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"id": request.GetString("id", "")}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "get_namespace", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "get_namespace", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("get_namespace", time.Since(start)) }()

	id, err := parseID(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	ns, err := s.store.GetNamespace(ctx, id)
	if err != nil {
		return storeError("namespace", err)
	}
	return jsonResult(ns)
}

func (s *Server) handleListWorkloads(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{
		"namespace_id": request.GetString("namespace_id", ""),
		"kind":         request.GetString("kind", ""),
		"image":        presence(request.GetString("image", "")),
	}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "list_workloads", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "list_workloads", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_workloads", time.Since(start)) }()

	var filter api.WorkloadListFilter

	nsID, err := parseOptionalUUID(request, "namespace_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	filter.NamespaceID = nsID

	if kind := request.GetString("kind", ""); kind != "" {
		wk := api.WorkloadKind(kind)
		filter.Kind = &wk
	}
	if img := request.GetString("image", ""); img != "" {
		filter.ImageSubstring = &img
	}

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Workload, string, error) {
		return s.store.ListWorkloads(ctx, filter, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list workloads: %w", err)
	}
	return jsonResult(items)
}

func (s *Server) handleGetWorkload(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"id": request.GetString("id", "")}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "get_workload", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "get_workload", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("get_workload", time.Since(start)) }()

	id, err := parseID(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	w, err := s.store.GetWorkload(ctx, id)
	if err != nil {
		return storeError("workload", err)
	}
	return jsonResult(w)
}

func (s *Server) handleListPods(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{
		"namespace_id": request.GetString("namespace_id", ""),
		"node_name":    presence(request.GetString("node_name", "")), // node names may embed cloud instance IDs
		"workload_id":  request.GetString("workload_id", ""),
		"image":        presence(request.GetString("image", "")),
	}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "list_pods", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "list_pods", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_pods", time.Since(start)) }()

	var filter api.PodListFilter

	nsID, err := parseOptionalUUID(request, "namespace_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	filter.NamespaceID = nsID

	wlID, err := parseOptionalUUID(request, "workload_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	filter.WorkloadID = wlID

	if nn := request.GetString("node_name", ""); nn != "" {
		filter.NodeName = &nn
	}
	if img := request.GetString("image", ""); img != "" {
		filter.ImageSubstring = &img
	}

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Pod, string, error) {
		return s.store.ListPods(ctx, filter, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	return jsonResult(items)
}

func (s *Server) handleGetPod(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"id": request.GetString("id", "")}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "get_pod", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "get_pod", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("get_pod", time.Since(start)) }()

	id, err := parseID(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	pod, err := s.store.GetPod(ctx, id)
	if err != nil {
		return storeError("pod", err)
	}
	return jsonResult(pod)
}

func (s *Server) handleListServices(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"namespace_id": request.GetString("namespace_id", "")}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "list_services", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "list_services", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_services", time.Since(start)) }()

	nsID, err := parseOptionalUUID(request, "namespace_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Service, string, error) {
		return s.store.ListServices(ctx, nsID, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	return jsonResult(items)
}

func (s *Server) handleListIngresses(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"namespace_id": request.GetString("namespace_id", "")}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "list_ingresses", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "list_ingresses", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_ingresses", time.Since(start)) }()

	nsID, err := parseOptionalUUID(request, "namespace_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Ingress, string, error) {
		return s.store.ListIngresses(ctx, nsID, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list ingresses: %w", err)
	}
	return jsonResult(items)
}

func (s *Server) handleListPersistentVolumes(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"cluster_id": request.GetString("cluster_id", "")}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "list_persistent_volumes", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "list_persistent_volumes", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_persistent_volumes", time.Since(start)) }()

	clusterID, err := parseOptionalUUID(request, "cluster_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.PersistentVolume, string, error) {
		return s.store.ListPersistentVolumes(ctx, clusterID, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list persistent volumes: %w", err)
	}
	return jsonResult(items)
}

func (s *Server) handleListPersistentVolumeClaims(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"namespace_id": request.GetString("namespace_id", "")}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "list_persistent_volume_claims", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "list_persistent_volume_claims", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_persistent_volume_claims", time.Since(start)) }()

	nsID, err := parseOptionalUUID(request, "namespace_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.PersistentVolumeClaim, string, error) {
		return s.store.ListPersistentVolumeClaims(ctx, nsID, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list persistent volume claims: %w", err)
	}
	return jsonResult(items)
}

func (s *Server) handleGetImpactGraph(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{
		"entity_type": request.GetString("entity_type", ""),
		"id":          request.GetString("id", ""),
		"depth":       request.GetFloat("depth", 2),
	}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "get_impact_graph", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "get_impact_graph", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("get_impact_graph", time.Since(start)) }()

	if s.traverser == nil {
		return mcp.NewToolResultError("impact graph traversal is not configured"), nil
	}

	entityType := request.GetString("entity_type", "")
	if !impact.ValidEntityType(entityType) {
		return mcp.NewToolResultError(
			fmt.Sprintf("invalid entity_type %q", entityType),
		), nil
	}

	id, err := parseID(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	depth := 2
	if d := request.GetFloat("depth", 2); d != 2 {
		depth = int(d)
		if depth < 1 {
			depth = 1
		}
		if depth > 3 {
			depth = 3
		}
	}

	graph, err := s.traverser.Traverse(ctx, impact.EntityType(entityType), id, depth)
	if err != nil {
		return nil, fmt.Errorf("impact traverse: %w", err)
	}
	return jsonResult(graph)
}

// eolSummaryEntry represents one entity's EOL status in the summary.
type eolSummaryEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Product string `json:"product,omitempty"`
	Status  string `json:"status"` // "eol", "approaching_eol", "supported", "unknown"
	EOLDate string `json:"eol_date,omitempty"`
}

// eolSummary is the response for the get_eol_summary tool.
type eolSummary struct {
	TotalClusters  int               `json:"total_clusters"`
	TotalNodes     int               `json:"total_nodes"`
	EOL            int               `json:"eol"`
	ApproachingEOL int               `json:"approaching_eol"`
	Supported      int               `json:"supported"`
	Unknown        int               `json:"unknown"`
	Entries        []eolSummaryEntry `json:"entries"`
}

func (s *Server) handleGetEOLSummary(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "get_eol_summary", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "get_eol_summary", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("get_eol_summary", time.Since(start)) }()

	clusters, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Cluster, string, error) {
		return s.store.ListClusters(ctx, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list clusters for eol: %w", err)
	}

	nodes, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Node, string, error) {
		return s.store.ListNodes(ctx, nil, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list nodes for eol: %w", err)
	}

	summary := eolSummary{
		TotalClusters: len(clusters),
		TotalNodes:    len(nodes),
	}

	// Scan cluster annotations for EOL data.
	for _, c := range clusters {
		entry := extractEOLEntry(idStr(c.Id), c.Name, "cluster", c.Annotations)
		countEOLStatus(&summary, entry.Status)
		summary.Entries = append(summary.Entries, entry)
	}

	// Scan node annotations for EOL data.
	for _, n := range nodes {
		entry := extractEOLEntry(idStr(n.Id), n.Name, "node", n.Annotations)
		countEOLStatus(&summary, entry.Status)
		summary.Entries = append(summary.Entries, entry)
	}

	return jsonResult(summary)
}

// imageSearchResult aggregates workloads and pods matching an image query.
type imageSearchResult struct {
	Query     string         `json:"query"`
	Workloads []api.Workload `json:"workloads"`
	Pods      []api.Pod      `json:"pods"`
}

func (s *Server) handleSearchImages(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"query": presence(request.GetString("query", ""))}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "search_images", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "search_images", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("search_images", time.Since(start)) }()

	query := request.GetString("query", "")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	workloads, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Workload, string, error) {
		return s.store.ListWorkloads(ctx, api.WorkloadListFilter{ImageSubstring: &query}, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("search workloads by image: %w", err)
	}

	pods, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Pod, string, error) {
		return s.store.ListPods(ctx, api.PodListFilter{ImageSubstring: &query}, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("search pods by image: %w", err)
	}

	return jsonResult(imageSearchResult{
		Query:     query,
		Workloads: workloads,
		Pods:      pods,
	})
}

// --- helpers ----------------------------------------------------------------

// parseID extracts and validates the required "id" UUID argument.
func parseID(request mcp.CallToolRequest) (uuid.UUID, error) {
	raw := request.GetString("id", "")
	if raw == "" {
		return uuid.Nil, fmt.Errorf("id: %w", errRequiredField)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid id: %w", err)
	}
	return id, nil
}

// parseOptionalUUID extracts an optional UUID argument. Returns nil when
// the argument is absent or empty.
//
//nolint:nilnil // nil UUID + nil error means "optional field absent", not an error.
func parseOptionalUUID(request mcp.CallToolRequest, key string) (*uuid.UUID, error) {
	raw := request.GetString(key, "")
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", key, err)
	}
	return &id, nil
}

// jsonResult marshals v to JSON and returns it as an MCP text result.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return mcp.NewToolResultText(string(data)), nil
}

// storeError maps store errors to user-facing MCP tool errors.
// ErrNotFound becomes "X not found"; all other errors are logged
// server-side and masked with a generic message.
//
//nolint:unparam // error is always nil by design — errors become tool results.
func storeError(entity string, err error) (*mcp.CallToolResult, error) {
	if errors.Is(err, api.ErrNotFound) {
		return mcp.NewToolResultError(fmt.Sprintf("%s not found", entity)), nil
	}
	slog.Error("mcp store error", slog.String("entity", entity), slog.Any("error", err))
	return mcp.NewToolResultError(fmt.Sprintf("internal error fetching %s", entity)), nil
}

// idStr converts an optional UUID pointer to its string form.
func idStr(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// extractEOLEntry inspects annotations for longue-vue.io/eol.* keys and
// builds a summary entry. Returns status "unknown" when no EOL
// annotation is found.
func extractEOLEntry(id, name, entityType string, annotations *map[string]string) eolSummaryEntry {
	entry := eolSummaryEntry{
		ID:     id,
		Name:   name,
		Type:   entityType,
		Status: "unknown",
	}

	if annotations == nil {
		return entry
	}

	for key, val := range *annotations {
		if !strings.HasPrefix(key, "longue-vue.io/eol.") {
			continue
		}

		// Parse the annotation JSON to extract status and eol_date.
		var ann struct {
			Status  string `json:"status"`
			EOLDate string `json:"eol_date"`
			Product string `json:"product"`
		}
		if err := json.Unmarshal([]byte(val), &ann); err != nil {
			slog.Debug("mcp: failed to parse eol annotation", slog.String("key", key), slog.Any("error", err))
			continue
		}

		entry.Product = ann.Product
		entry.Status = ann.Status
		entry.EOLDate = ann.EOLDate
		// Take the worst status if multiple EOL annotations exist.
		if ann.Status == "eol" {
			break
		}
	}

	return entry
}

// countEOLStatus increments the appropriate counter in the summary.
func countEOLStatus(summary *eolSummary, status string) {
	switch status {
	case "eol":
		summary.EOL++
	case "approaching_eol":
		summary.ApproachingEOL++
	case "supported":
		summary.Supported++
	default:
		summary.Unknown++
	}
}
