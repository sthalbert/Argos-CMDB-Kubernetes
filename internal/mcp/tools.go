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
//nolint:funlen // registering 22 tools in one function is inherently long.
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
		mcp.NewTool("list_cloud_accounts",
			mcp.WithDescription("List cloud-provider accounts registered in the CMDB (credentials redacted)"),
		),
		s.handleListCloudAccounts,
	)

	s.mcp.AddTool(
		mcp.NewTool("list_virtual_machines",
			mcp.WithDescription("List non-Kubernetes platform VMs (Vault, DNS, Bastion, ...) registered in the CMDB"),
			mcp.WithString("cloud_account_id", mcp.Description("Filter by cloud account UUID (optional)")),
			mcp.WithString("cloud_account_name", mcp.Description("Filter by cloud account name (optional)")),
			mcp.WithString("region", mcp.Description("Filter by region (optional)")),
			mcp.WithString("role", mcp.Description("Filter by operator-curated role (optional)")),
			mcp.WithString("power_state", mcp.Description("Filter by power state, e.g. running / stopped / terminated (optional)")),
			mcp.WithString("name", mcp.Description("Filter by VM name substring, case-insensitive (optional)")),
			mcp.WithString("image", mcp.Description("Filter by image_id or image_name substring, case-insensitive (optional)")),
			mcp.WithString("application", mcp.Description("Filter to VMs running this product (normalized server-side, optional)")),
			mcp.WithString("application_version", mcp.Description("Narrow `application` to this version (optional, ignored without application)")),
			mcp.WithBoolean("include_terminated", mcp.Description("Include soft-deleted VMs (default false)")),
		),
		s.handleListVirtualMachines,
	)

	s.mcp.AddTool(
		mcp.NewTool("get_virtual_machine",
			mcp.WithDescription("Get a single platform VM by its UUID, including curated applications list"),
			mcp.WithString("id", mcp.Required(), mcp.Description("Virtual machine UUID")),
		),
		s.handleGetVirtualMachine,
	)

	s.mcp.AddTool(
		mcp.NewTool("list_vm_applications_distinct",
			mcp.WithDescription("List the distinct (product, versions[]) tuples seen across non-terminated platform VMs"),
		),
		s.handleListVMApplicationsDistinct,
	)

	s.mcp.AddTool(
		mcp.NewTool("get_cloud_account",
			mcp.WithDescription("Get a single cloud-provider account by its UUID (credentials redacted)"),
			mcp.WithString("id", mcp.Required(), mcp.Description("Cloud account UUID")),
		),
		s.handleGetCloudAccount,
	)

	s.mcp.AddTool(
		mcp.NewTool("search_images",
			mcp.WithDescription("Search for workloads and pods running a specific container image"),
			mcp.WithString("query", mcp.Required(), mcp.Description("Container image name or substring to search for")),
		),
		s.handleSearchImages,
	)

	s.mcp.AddTool(
		mcp.NewTool("list_image_versions",
			mcp.WithDescription("List enriched container images with their latest available tag per variant. Returns one entry per image_repo with all variants nested. Filters compose with AND."),
			mcp.WithString("registry", mcp.Description("Filter by registry hostname, e.g. docker.io, ghcr.io, quay.io (optional)")),
			mcp.WithString("image_repo", mcp.Description("Filter by image_repo substring, case-insensitive, e.g. nginx (optional)")),
			mcp.WithString("variant", mcp.Description("Filter to repos that have this variant family, e.g. alpine, debian-12 (optional)")),
			mcp.WithBoolean("has_error", mcp.Description("When true, return only repos with at least one variant in error. When false, only repos where every variant succeeded. (optional)")),
		),
		s.handleListImageVersions,
	)

	s.mcp.AddTool(
		mcp.NewTool("get_image_version",
			mcp.WithDescription("Get all enriched variants for a given image_repo (e.g., docker.io/library/nginx)"),
			mcp.WithString("image_repo", mcp.Required(), mcp.Description("Fully-qualified image repo, e.g. docker.io/library/nginx, quay.io/prometheus/prometheus")),
		),
		s.handleGetImageVersion,
	)

	s.mcp.AddTool(
		mcp.NewTool("get_image_versions_summary",
			mcp.WithDescription("Aggregate the image_versions store: counts of total repos, total variant rows, repos with at least one error, repos fully ok, plus a per-registry breakdown. Useful for status dashboards and LLM-driven analysis."),
		),
		s.handleGetImageVersionsSummary,
	)
}

// --- tool handlers ----------------------------------------------------------

func (s *Server) handleListClusters(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"name": presence(request.GetString("name", ""))}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		return s.recordCheckAccessFailure(ctx, "list_clusters", args, err), nil
	}
	defer s.finishDeferred(ctx, "list_clusters", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_clusters", time.Since(start)) }()

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Cluster, string, error) {
		return s.store.ListClusters(ctx, maxPageSize, cursor, false)
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
		return s.store.ListNodes(ctx, clusterID, maxPageSize, cursor, false)
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
		return s.store.ListNamespaces(ctx, clusterID, maxPageSize, cursor, false)
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

// eolStatusEOL is the JSON status value for an end-of-life product.
const eolStatusEOL = "eol"

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
	TotalClusters        int               `json:"total_clusters"`
	TotalNodes           int               `json:"total_nodes"`
	TotalVirtualMachines int               `json:"total_virtual_machines"`
	EOL                  int               `json:"eol"`
	ApproachingEOL       int               `json:"approaching_eol"`
	Supported            int               `json:"supported"`
	Unknown              int               `json:"unknown"`
	Entries              []eolSummaryEntry `json:"entries"`
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
		return s.store.ListClusters(ctx, maxPageSize, cursor, false)
	})
	if err != nil {
		return nil, fmt.Errorf("list clusters for eol: %w", err)
	}

	nodes, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Node, string, error) {
		return s.store.ListNodes(ctx, nil, maxPageSize, cursor, false)
	})
	if err != nil {
		return nil, fmt.Errorf("list nodes for eol: %w", err)
	}

	vms, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.VirtualMachine, string, error) {
		return s.store.ListVirtualMachines(ctx, api.VirtualMachineListFilter{}, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list virtual machines for eol: %w", err)
	}

	summary := eolSummary{
		TotalClusters:        len(clusters),
		TotalNodes:           len(nodes),
		TotalVirtualMachines: len(vms),
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

	// Scan virtual machine annotations for EOL data. VMs use a value-type
	// annotations map (not a pointer like clusters/nodes); adapt with
	// a local pointer so extractEOLEntry's signature is reused.
	for i := range vms {
		vm := &vms[i]
		var annPtr *map[string]string
		if vm.Annotations != nil {
			annPtr = &vm.Annotations
		}
		entry := extractEOLEntry(vm.ID.String(), vm.Name, "vm", annPtr)
		countEOLStatus(&summary, entry.Status)
		summary.Entries = append(summary.Entries, entry)
	}

	return jsonResult(summary)
}

// imageSearchResult aggregates workloads, pods, and platform VMs matching
// an image query. The virtual_machines section was added alongside the
// VM coverage tools; existing pods/workloads keys are preserved.
type imageSearchResult struct {
	Query           string               `json:"query"`
	Workloads       []api.Workload       `json:"workloads"`
	Pods            []api.Pod            `json:"pods"`
	VirtualMachines []api.VirtualMachine `json:"virtual_machines"`
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

	vms, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.VirtualMachine, string, error) {
		return s.store.ListVirtualMachines(ctx, api.VirtualMachineListFilter{Image: &query}, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("search virtual machines by image: %w", err)
	}

	return jsonResult(imageSearchResult{
		Query:           query,
		Workloads:       workloads,
		Pods:            pods,
		VirtualMachines: vms,
	})
}

// --- cloud-account & VM tool handlers --------------------------------------

func (s *Server) handleListCloudAccounts(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		return s.recordCheckAccessFailure(ctx, "list_cloud_accounts", args, err), nil
	}
	defer s.finishDeferred(ctx, "list_cloud_accounts", args, &resp, &retErr)
	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_cloud_accounts", time.Since(start)) }()

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.CloudAccount, string, error) {
		return s.store.ListCloudAccounts(ctx, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list cloud accounts: %w", err)
	}
	for i := range items {
		items[i] = redactCloudAccount(items[i])
	}
	return jsonResult(items)
}

// VM filter length caps mirror internal/api/virtual_machine_handlers.go.
const (
	mcpVMNameMaxLen      = 100
	mcpVMImageMaxLen     = 100
	mcpVMAccountMaxLen   = 200
	mcpVMEnumMaxLen      = 64
	mcpVMAppFilterMaxLen = 64
	mcpVMAppVersionMaxLn = 64
)

// buildVMListFilter parses optional MCP request args into a VirtualMachineListFilter,
// applying the same length caps used by the REST handler. Returns a user-facing
// error message on the first violation (so the handler can surface it via
// NewToolResultError).
//
//nolint:gocyclo,gocritic // each parameter is an independent branch; CallToolRequest is by value per SDK signature.
func buildVMListFilter(request mcp.CallToolRequest) (api.VirtualMachineListFilter, string, error) {
	var f api.VirtualMachineListFilter
	if v := request.GetString("cloud_account_id", ""); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return f, "invalid cloud_account_id", fmt.Errorf("parse cloud_account_id: %w", err)
		}
		f.CloudAccountID = &id
	}
	if v := request.GetString("cloud_account_name", ""); v != "" {
		if len(v) > mcpVMAccountMaxLen {
			return f, "cloud_account_name too long", errRequiredField
		}
		f.CloudAccountName = &v
	}
	if v := request.GetString("region", ""); v != "" {
		if len(v) > mcpVMEnumMaxLen {
			return f, "region too long", errRequiredField
		}
		f.Region = &v
	}
	if v := request.GetString("role", ""); v != "" {
		if len(v) > mcpVMEnumMaxLen {
			return f, "role too long", errRequiredField
		}
		f.Role = &v
	}
	if v := request.GetString("power_state", ""); v != "" {
		if len(v) > mcpVMEnumMaxLen {
			return f, "power_state too long", errRequiredField
		}
		f.PowerState = &v
	}
	if v := request.GetString("name", ""); v != "" {
		if len(v) > mcpVMNameMaxLen {
			return f, "name too long", errRequiredField
		}
		f.Name = &v
	}
	if v := request.GetString("image", ""); v != "" {
		if len(v) > mcpVMImageMaxLen {
			return f, "image too long", errRequiredField
		}
		f.Image = &v
	}
	if v := request.GetString("application", ""); v != "" {
		if len(v) > mcpVMAppFilterMaxLen {
			return f, "application too long", errRequiredField
		}
		f.Application = &v
	}
	if v := request.GetString("application_version", ""); v != "" {
		if len(v) > mcpVMAppVersionMaxLn {
			return f, "application_version too long", errRequiredField
		}
		f.ApplicationVersion = &v
	}
	f.IncludeTerminated = request.GetBool("include_terminated", false)
	return f, "", nil
}

func (s *Server) handleListVirtualMachines(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{
		"cloud_account_id":    presence(request.GetString("cloud_account_id", "")),
		"cloud_account_name":  presence(request.GetString("cloud_account_name", "")),
		"region":              presence(request.GetString("region", "")),
		"role":                presence(request.GetString("role", "")),
		"power_state":         presence(request.GetString("power_state", "")),
		"name":                presence(request.GetString("name", "")),
		"image":               presence(request.GetString("image", "")),
		"application":         presence(request.GetString("application", "")),
		"application_version": presence(request.GetString("application_version", "")),
		"include_terminated":  request.GetBool("include_terminated", false),
	}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		return s.recordCheckAccessFailure(ctx, "list_virtual_machines", args, err), nil
	}
	defer s.finishDeferred(ctx, "list_virtual_machines", args, &resp, &retErr)
	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_virtual_machines", time.Since(start)) }()

	filter, problem, err := buildVMListFilter(request)
	if err != nil {
		return mcp.NewToolResultError(problem), nil
	}

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.VirtualMachine, string, error) {
		return s.store.ListVirtualMachines(ctx, filter, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list virtual machines: %w", err)
	}
	return jsonResult(items)
}

func (s *Server) handleGetVirtualMachine(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"id": presence(request.GetString("id", ""))}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		return s.recordCheckAccessFailure(ctx, "get_virtual_machine", args, err), nil
	}
	defer s.finishDeferred(ctx, "get_virtual_machine", args, &resp, &retErr)
	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("get_virtual_machine", time.Since(start)) }()

	id, err := parseID(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	vm, err := s.store.GetVirtualMachine(ctx, id)
	if err != nil {
		return storeError("virtual machine", err)
	}
	return jsonResult(vm)
}

// vmApplicationsResponse wraps the distinct list under a stable top-level
// key so future fields can be added without breaking MCP clients.
type vmApplicationsResponse struct {
	Products []api.VMApplicationDistinct `json:"products"`
}

func (s *Server) handleListVMApplicationsDistinct(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		return s.recordCheckAccessFailure(ctx, "list_vm_applications_distinct", args, err), nil
	}
	defer s.finishDeferred(ctx, "list_vm_applications_distinct", args, &resp, &retErr)
	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_vm_applications_distinct", time.Since(start)) }()

	products, err := s.store.ListDistinctVMApplications(ctx)
	if err != nil {
		return nil, fmt.Errorf("list distinct vm applications: %w", err)
	}
	return jsonResult(vmApplicationsResponse{Products: products})
}

func (s *Server) handleGetCloudAccount(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"id": presence(request.GetString("id", ""))}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		return s.recordCheckAccessFailure(ctx, "get_cloud_account", args, err), nil
	}
	defer s.finishDeferred(ctx, "get_cloud_account", args, &resp, &retErr)
	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("get_cloud_account", time.Since(start)) }()

	id, err := parseID(request)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	acct, err := s.store.GetCloudAccount(ctx, id)
	if err != nil {
		return storeError("cloud account", err)
	}
	return jsonResult(redactCloudAccount(acct))
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
		if ann.Status == eolStatusEOL {
			break
		}
	}

	return entry
}

// countEOLStatus increments the appropriate counter in the summary.
func countEOLStatus(summary *eolSummary, status string) {
	switch status {
	case eolStatusEOL:
		summary.EOL++
	case "approaching_eol":
		summary.ApproachingEOL++
	case "supported":
		summary.Supported++
	default:
		summary.Unknown++
	}
}


// --- image-version tool handlers -------------------------------------------

func (s *Server) handleListImageVersions(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{
		"registry":   request.GetString("registry", ""),
		"image_repo": presence(request.GetString("image_repo", "")),
		"variant":    request.GetString("variant", ""),
	}
	hasErrorRaw := request.GetString("has_error", "")
	if hasErrorRaw != "" {
		args["has_error"] = hasErrorRaw
	}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		return s.recordCheckAccessFailure(ctx, "list_image_versions", args, err), nil
	}
	defer s.finishDeferred(ctx, "list_image_versions", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_image_versions", time.Since(start)) }()

	var lp api.ImageVersionListParams
	lp.Registry = request.GetString("registry", "")
	lp.ImageRepoLike = request.GetString("image_repo", "")
	lp.Variant = request.GetString("variant", "")
	if hasErrorRaw != "" {
		// mcp-go returns the boolean arg as a string; tolerate "true"/"false".
		v := strings.EqualFold(hasErrorRaw, "true")
		lp.HasError = &v
	}

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.ImageVersionRepoView, string, error) {
		lp.Cursor = cursor
		lp.Limit = maxPageSize
		return s.store.ListImageVersionsByRepo(ctx, lp)
	})
	if err != nil {
		return nil, fmt.Errorf("list image versions: %w", err)
	}
	return jsonResult(items)
}

func (s *Server) handleGetImageVersion(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{"image_repo": request.GetString("image_repo", "")}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		s.recordDenial(ctx, "get_image_version", args)
		return mcp.NewToolResultError(err.Error()), nil
	}
	defer s.finishDeferred(ctx, "get_image_version", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("get_image_version", time.Since(start)) }()

	repo := request.GetString("image_repo", "")
	if repo == "" {
		return mcp.NewToolResultError("image_repo is required"), nil
	}

	rows, err := s.store.GetImageVersionsByRepo(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("get image versions by repo: %w", err)
	}
	if len(rows) == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("image_repo %q not found in image_versions", repo)), nil
	}
	view := api.ImageVersionRepoView{
		ImageRepo: rows[0].ImageRepo,
		Registry:  rows[0].Registry,
		Variants:  rows,
	}
	return jsonResult(view)
}

// imageVersionsSummary aggregates the image_versions store into a single
// snapshot that fits comfortably in an LLM context window — counts plus a
// per-registry breakdown — without dumping every row.
type imageVersionsSummary struct {
	TotalRepos        int                              `json:"total_repos"`
	TotalVariants     int                              `json:"total_variants"`
	ReposWithErrors   int                              `json:"repos_with_errors"`
	ReposAllOK        int                              `json:"repos_all_ok"`
	VariantsWithError int                              `json:"variants_with_error"`
	ByRegistry        []imageVersionsRegistrySummary   `json:"by_registry"`
	ErroredRepos      []imageVersionsErroredRepoBucket `json:"errored_repos,omitempty"`
}

type imageVersionsRegistrySummary struct {
	Registry          string `json:"registry"`
	Repos             int    `json:"repos"`
	Variants          int    `json:"variants"`
	ReposWithErrors   int    `json:"repos_with_errors"`
	VariantsWithError int    `json:"variants_with_error"`
}

type imageVersionsErroredRepoBucket struct {
	ImageRepo    string `json:"image_repo"`
	Variant      string `json:"variant"`
	Registry     string `json:"registry"`
	LastError    string `json:"last_error"`
	LastCheckedA string `json:"last_checked_at"`
}

func (s *Server) handleGetImageVersionsSummary(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		return s.recordCheckAccessFailure(ctx, "get_image_versions_summary", args, err), nil
	}
	defer s.finishDeferred(ctx, "get_image_versions_summary", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("get_image_versions_summary", time.Since(start)) }()

	repos, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.ImageVersionRepoView, string, error) {
		return s.store.ListImageVersionsByRepo(ctx, api.ImageVersionListParams{
			Limit:  maxPageSize,
			Cursor: cursor,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("list image versions for summary: %w", err)
	}

	out := imageVersionsSummary{}
	out.TotalRepos = len(repos)
	byReg := map[string]*imageVersionsRegistrySummary{}
	// Surface up to 50 errored variant rows so the LLM can reason about
	// specific failures without flooding the response.
	const maxErroredSamples = 50

	for i := range repos {
		repo := &repos[i]
		out.TotalVariants += len(repo.Variants)
		hasErr := false
		for j := range repo.Variants {
			v := &repo.Variants[j]
			if v.LastError != nil && *v.LastError != "" {
				out.VariantsWithError++
				hasErr = true
				if len(out.ErroredRepos) < maxErroredSamples {
					at := ""
					if !v.LastCheckedAt.IsZero() {
						at = v.LastCheckedAt.UTC().Format(time.RFC3339)
					}
					out.ErroredRepos = append(out.ErroredRepos, imageVersionsErroredRepoBucket{
						ImageRepo:    repo.ImageRepo,
						Variant:      v.Variant,
						Registry:     repo.Registry,
						LastError:    *v.LastError,
						LastCheckedA: at,
					})
				}
			}
		}
		if hasErr {
			out.ReposWithErrors++
		} else {
			out.ReposAllOK++
		}

		bucket, ok := byReg[repo.Registry]
		if !ok {
			bucket = &imageVersionsRegistrySummary{Registry: repo.Registry}
			byReg[repo.Registry] = bucket
		}
		bucket.Repos++
		bucket.Variants += len(repo.Variants)
		if hasErr {
			bucket.ReposWithErrors++
		}
		for j := range repo.Variants {
			if repo.Variants[j].LastError != nil && *repo.Variants[j].LastError != "" {
				bucket.VariantsWithError++
			}
		}
	}

	// Stable order by registry name for reproducible LLM consumption.
	regs := make([]string, 0, len(byReg))
	for k := range byReg {
		regs = append(regs, k)
	}
	sortStrings(regs)
	out.ByRegistry = make([]imageVersionsRegistrySummary, 0, len(regs))
	for _, r := range regs {
		out.ByRegistry = append(out.ByRegistry, *byReg[r])
	}

	return jsonResult(out)
}

// sortStrings is the stdlib sort.Strings without introducing a dependency
// in this file just for one call (avoids a top-level import touch).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
