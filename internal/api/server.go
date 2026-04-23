package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/auth"
)

var (
	errRunbookURLInvalid = errors.New("runbook_url is not a valid URL")
	errRunbookURLScheme  = errors.New("runbook_url must use http or https scheme")
)

// validateRunbookURL rejects runbook URLs that use a scheme other than
// http or https. This prevents javascript: and data: XSS vectors when
// the URL is rendered as an <a href> in the UI.
func validateRunbookURL(raw *string) error {
	if raw == nil || *raw == "" {
		return nil
	}
	u, err := url.Parse(*raw)
	if err != nil {
		return errRunbookURLInvalid
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return errRunbookURLScheme
	}
	return nil
}

// Server implements StrictServerInterface for the Argos REST API.
type Server struct {
	version      string
	store        Store
	cookiePolicy auth.SecureCookiePolicy
	oidc         *auth.OIDCProvider // nil when OIDC is not configured
}

// NewServer wires the handlers with a persistence backend and the build
// version reported on health probes. `cookiePolicy` governs the Secure
// flag on session cookies (see ADR-0007); auto = mirror request scheme.
// `oidc` may be nil to disable the OIDC flow entirely.
func NewServer(version string, store Store, cookiePolicy auth.SecureCookiePolicy, oidc *auth.OIDCProvider) *Server {
	return &Server{version: version, store: store, cookiePolicy: cookiePolicy, oidc: oidc}
}

var _ StrictServerInterface = (*Server)(nil)

// ── Problem helpers ──────────────────────────────────────────────────

func problemNotFound() Problem {
	return Problem{Type: "about:blank", Title: "Not Found", Status: 404}
}

func problemConflict(err error) Problem {
	detail := err.Error()
	return Problem{Type: "about:blank", Title: "Conflict", Status: 409, Detail: &detail}
}

func problemBadRequest(title, detail string) Problem {
	p := Problem{Type: "about:blank", Title: title, Status: 400}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

func problemServiceUnavailable(detail string) Problem {
	p := Problem{Type: "about:blank", Title: "Not Ready", Status: 503}
	if detail != "" {
		p.Detail = &detail
	}
	return p
}

// storeErr wraps a store-layer error with a handler context string so
// the wrapcheck linter is satisfied and stack context is preserved.
func storeErr(op string, err error) error {
	return fmt.Errorf("%s: %w", op, err)
}

// ── Health probes ────────────────────────────────────────────────────

// GetHealthz reports that the process is alive.
func (s *Server) GetHealthz(_ context.Context, _ GetHealthzRequestObject) (GetHealthzResponseObject, error) {
	return GetHealthz200JSONResponse(Health{Status: Ok, Version: &s.version}), nil
}

// GetReadyz reports whether the service can accept traffic by pinging the store.
func (s *Server) GetReadyz(ctx context.Context, _ GetReadyzRequestObject) (GetReadyzResponseObject, error) {
	if err := s.store.Ping(ctx); err != nil {
		slog.Error("readyz: store ping failed", slog.Any("error", err))
		return GetReadyz503ApplicationProblemPlusJSONResponse(problemServiceUnavailable("database not reachable")), nil
	}
	return GetReadyz200JSONResponse(Health{Status: Ok, Version: &s.version}), nil
}

// ── Clusters ─────────────────────────────────────────────────────────

// ListClusters returns a paged list of clusters.
func (s *Server) ListClusters(ctx context.Context, req ListClustersRequestObject) (ListClustersResponseObject, error) {
	// Exact name filter: short-circuit to GetClusterByName and return a
	// single-item list (or empty). Used by the push collector to resolve
	// its cluster record without paginating.
	if req.Params.Name != nil && *req.Params.Name != "" {
		c, err := s.store.GetClusterByName(ctx, *req.Params.Name)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return ListClusters200JSONResponse(ClusterList{Items: []Cluster{}}), nil
			}
			return nil, fmt.Errorf("getClusterByName: %w", err)
		}
		c = withClusterLayer(c)
		return ListClusters200JSONResponse(ClusterList{Items: []Cluster{c}}), nil
	}

	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	cursor := ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}

	items, next, err := s.store.ListClusters(ctx, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("listClusters: %w", err)
	}

	for i := range items {
		items[i] = withClusterLayer(items[i])
	}
	resp := ClusterList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListClusters200JSONResponse(resp), nil
}

// CreateCluster registers a new cluster.
func (s *Server) CreateCluster(ctx context.Context, req CreateClusterRequestObject) (CreateClusterResponseObject, error) {
	body := *req.Body
	if body.Name == "" {
		return CreateCluster400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'name' is required")),
		}, nil
	}
	if err := validateRunbookURL(body.RunbookUrl); err != nil {
		return CreateCluster400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid field", err.Error())),
		}, nil
	}

	c, err := s.store.CreateCluster(ctx, body)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return CreateCluster409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse(problemConflict(err)),
			}, nil
		}
		return nil, fmt.Errorf("createCluster: %w", err)
	}
	c = withClusterLayer(c)

	loc := "/v1/clusters/"
	if c.Id != nil {
		loc += c.Id.String()
	}
	return CreateCluster201JSONResponse{
		Body:    c,
		Headers: CreateCluster201ResponseHeaders{Location: loc},
	}, nil
}

// GetCluster fetches a cluster by id.
func (s *Server) GetCluster(ctx context.Context, req GetClusterRequestObject) (GetClusterResponseObject, error) {
	c, err := s.store.GetCluster(ctx, req.Id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return GetCluster404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("getCluster: %w", err)
	}
	return GetCluster200JSONResponse(withClusterLayer(c)), nil
}

// UpdateCluster applies merge-patch updates to a cluster.
func (s *Server) UpdateCluster(ctx context.Context, req UpdateClusterRequestObject) (UpdateClusterResponseObject, error) {
	if err := validateRunbookURL(req.Body.RunbookUrl); err != nil {
		return UpdateCluster400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid field", err.Error())),
		}, nil
	}
	c, err := s.store.UpdateCluster(ctx, req.Id, *req.Body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return UpdateCluster404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("updateCluster: %w", err)
	}
	return UpdateCluster200JSONResponse(withClusterLayer(c)), nil
}

// DeleteCluster removes a cluster. Before deleting, it snapshots the
// cluster metadata and cascade counts into the audit event so the
// record is self-contained even after the row is gone (ADR-0010).
func (s *Server) DeleteCluster(ctx context.Context, req DeleteClusterRequestObject) (DeleteClusterResponseObject, error) {
	// Capture the pre-deletion snapshot for audit enrichment.
	cluster, err := s.store.GetCluster(ctx, req.Id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeleteCluster404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("deleteCluster: get snapshot: %w", err)
	}

	counts, err := s.store.CountClusterChildren(ctx, req.Id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		slog.Error("deleteCluster: count children failed, proceeding without cascade counts",
			slog.Any("error", err),
			slog.String("cluster_id", req.Id.String()),
		)
	}

	if err := s.store.DeleteCluster(ctx, req.Id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeleteCluster404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("deleteCluster: %w", err)
	}

	SetAuditDetails(ctx, clusterDeletionSnapshot(&cluster, counts))
	return DeleteCluster204Response{}, nil
}

// clusterDeletionSnapshot builds the audit-event details map for a
// cluster deletion, capturing identity, curated metadata, and the
// cascade impact (ADR-0010).
func clusterDeletionSnapshot(c *Cluster, counts CascadeCounts) map[string]any {
	details := map[string]any{
		"cluster_name": c.Name,
		"cascade_counts": map[string]int{
			"namespaces":               counts.Namespaces,
			"nodes":                    counts.Nodes,
			"pods":                     counts.Pods,
			"workloads":                counts.Workloads,
			"services":                 counts.Services,
			"ingresses":                counts.Ingresses,
			"persistent_volumes":       counts.PersistentVolumes,
			"persistent_volume_claims": counts.PersistentVolumeClaims,
		},
	}
	if c.DisplayName != nil {
		details["cluster_display_name"] = *c.DisplayName
	}
	if c.Environment != nil {
		details["cluster_environment"] = *c.Environment
	}
	if c.Owner != nil {
		details["cluster_owner"] = *c.Owner
	}
	if c.Criticality != nil {
		details["cluster_criticality"] = *c.Criticality
	}
	return details
}

// ── Nodes ────────────────────────────────────────────────────────────

// ListNodes returns a paged list of nodes, optionally filtered by cluster_id.
func (s *Server) ListNodes(ctx context.Context, req ListNodesRequestObject) (ListNodesResponseObject, error) {
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	cursor := ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}

	items, next, err := s.store.ListNodes(ctx, req.Params.ClusterId, limit, cursor)
	if err != nil {
		return nil, storeErr("listNodes", err)
	}

	for i := range items {
		items[i] = withNodeLayer(items[i])
	}
	resp := NodeList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListNodes200JSONResponse(resp), nil
}

// CreateNode registers a new node under a cluster.
func (s *Server) CreateNode(ctx context.Context, req CreateNodeRequestObject) (CreateNodeResponseObject, error) {
	body := *req.Body
	if body.Name == "" {
		return CreateNode400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'name' is required")),
		}, nil
	}
	if body.ClusterId == (uuid.UUID{}) {
		return CreateNode400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'cluster_id' is required")),
		}, nil
	}
	if err := validateRunbookURL(body.RunbookUrl); err != nil {
		return CreateNode400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid field", err.Error())),
		}, nil
	}

	n, err := s.store.UpsertNode(ctx, body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return CreateNode404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		if errors.Is(err, ErrConflict) {
			return CreateNode409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse(problemConflict(err)),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	n = withNodeLayer(n)

	loc := "/v1/nodes/"
	if n.Id != nil {
		loc += n.Id.String()
	}
	return CreateNode201JSONResponse{
		Body:    n,
		Headers: CreateNode201ResponseHeaders{Location: loc},
	}, nil
}

// GetNode fetches a node by id.
func (s *Server) GetNode(ctx context.Context, req GetNodeRequestObject) (GetNodeResponseObject, error) {
	n, err := s.store.GetNode(ctx, req.Id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return GetNode404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return GetNode200JSONResponse(withNodeLayer(n)), nil
}

// UpdateNode applies merge-patch updates to a node.
func (s *Server) UpdateNode(ctx context.Context, req UpdateNodeRequestObject) (UpdateNodeResponseObject, error) {
	if err := validateRunbookURL(req.Body.RunbookUrl); err != nil {
		return UpdateNode400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid field", err.Error())),
		}, nil
	}
	n, err := s.store.UpdateNode(ctx, req.Id, *req.Body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return UpdateNode404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return UpdateNode200JSONResponse(withNodeLayer(n)), nil
}

// DeleteNode removes a node.
func (s *Server) DeleteNode(ctx context.Context, req DeleteNodeRequestObject) (DeleteNodeResponseObject, error) {
	if err := s.store.DeleteNode(ctx, req.Id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeleteNode404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return DeleteNode204Response{}, nil
}

// ── Namespaces ───────────────────────────────────────────────────────

// ListNamespaces returns a paged list of namespaces, optionally filtered by cluster_id.
func (s *Server) ListNamespaces(ctx context.Context, req ListNamespacesRequestObject) (ListNamespacesResponseObject, error) {
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	cursor := ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}

	items, next, err := s.store.ListNamespaces(ctx, req.Params.ClusterId, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	for i := range items {
		items[i] = withNamespaceLayer(items[i])
	}
	resp := NamespaceList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListNamespaces200JSONResponse(resp), nil
}

// CreateNamespace registers a new namespace under a cluster.
func (s *Server) CreateNamespace(ctx context.Context, req CreateNamespaceRequestObject) (CreateNamespaceResponseObject, error) {
	body := *req.Body
	if body.Name == "" {
		return CreateNamespace400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'name' is required")),
		}, nil
	}
	if body.ClusterId == (uuid.UUID{}) {
		return CreateNamespace400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'cluster_id' is required")),
		}, nil
	}
	if err := validateRunbookURL(body.RunbookUrl); err != nil {
		return CreateNamespace400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid field", err.Error())),
		}, nil
	}

	n, err := s.store.UpsertNamespace(ctx, body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return CreateNamespace404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		if errors.Is(err, ErrConflict) {
			return CreateNamespace409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse(problemConflict(err)),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	n = withNamespaceLayer(n)

	loc := "/v1/namespaces/"
	if n.Id != nil {
		loc += n.Id.String()
	}
	return CreateNamespace201JSONResponse{
		Body:    n,
		Headers: CreateNamespace201ResponseHeaders{Location: loc},
	}, nil
}

// GetNamespace fetches a namespace by id.
func (s *Server) GetNamespace(ctx context.Context, req GetNamespaceRequestObject) (GetNamespaceResponseObject, error) {
	n, err := s.store.GetNamespace(ctx, req.Id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return GetNamespace404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return GetNamespace200JSONResponse(withNamespaceLayer(n)), nil
}

// UpdateNamespace applies merge-patch updates.
func (s *Server) UpdateNamespace(ctx context.Context, req UpdateNamespaceRequestObject) (UpdateNamespaceResponseObject, error) {
	if err := validateRunbookURL(req.Body.RunbookUrl); err != nil {
		return UpdateNamespace400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid field", err.Error())),
		}, nil
	}
	n, err := s.store.UpdateNamespace(ctx, req.Id, *req.Body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return UpdateNamespace404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return UpdateNamespace200JSONResponse(withNamespaceLayer(n)), nil
}

// DeleteNamespace removes a namespace.
func (s *Server) DeleteNamespace(ctx context.Context, req DeleteNamespaceRequestObject) (DeleteNamespaceResponseObject, error) {
	if err := s.store.DeleteNamespace(ctx, req.Id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeleteNamespace404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return DeleteNamespace204Response{}, nil
}

// ── Pods ─────────────────────────────────────────────────────────────

// ListPods returns a paged list of pods, optionally filtered by namespace_id,
// node_name, and/or container image substring.
func (s *Server) ListPods(ctx context.Context, req ListPodsRequestObject) (ListPodsResponseObject, error) {
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	cursor := ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	filter := PodListFilter{
		NamespaceID:    req.Params.NamespaceId,
		NodeName:       req.Params.NodeName,
		ImageSubstring: req.Params.Image,
	}

	items, next, err := s.store.ListPods(ctx, filter, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	for i := range items {
		items[i] = withPodLayer(items[i])
	}
	resp := PodList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListPods200JSONResponse(resp), nil
}

// CreatePod registers a new pod under a namespace.
func (s *Server) CreatePod(ctx context.Context, req CreatePodRequestObject) (CreatePodResponseObject, error) {
	body := *req.Body
	if body.Name == "" {
		return CreatePod400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'name' is required")),
		}, nil
	}
	if body.NamespaceId == (uuid.UUID{}) {
		return CreatePod400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'namespace_id' is required")),
		}, nil
	}

	p, err := s.store.UpsertPod(ctx, body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return CreatePod404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		if errors.Is(err, ErrConflict) {
			return CreatePod409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse(problemConflict(err)),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	p = withPodLayer(p)

	loc := "/v1/pods/"
	if p.Id != nil {
		loc += p.Id.String()
	}
	return CreatePod201JSONResponse{
		Body:    p,
		Headers: CreatePod201ResponseHeaders{Location: loc},
	}, nil
}

// GetPod fetches a pod by id.
func (s *Server) GetPod(ctx context.Context, req GetPodRequestObject) (GetPodResponseObject, error) {
	p, err := s.store.GetPod(ctx, req.Id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return GetPod404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return GetPod200JSONResponse(withPodLayer(p)), nil
}

// UpdatePod applies merge-patch updates.
func (s *Server) UpdatePod(ctx context.Context, req UpdatePodRequestObject) (UpdatePodResponseObject, error) {
	p, err := s.store.UpdatePod(ctx, req.Id, *req.Body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return UpdatePod404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return UpdatePod200JSONResponse(withPodLayer(p)), nil
}

// DeletePod removes a pod.
func (s *Server) DeletePod(ctx context.Context, req DeletePodRequestObject) (DeletePodResponseObject, error) {
	if err := s.store.DeletePod(ctx, req.Id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeletePod404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return DeletePod204Response{}, nil
}

// ── Workloads ────────────────────────────────────────────────────────

// ListWorkloads returns a paged list of workloads, optionally filtered by
// namespace_id and/or kind.
func (s *Server) ListWorkloads(ctx context.Context, req ListWorkloadsRequestObject) (ListWorkloadsResponseObject, error) {
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	cursor := ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}
	if req.Params.Kind != nil && !req.Params.Kind.Valid() {
		return ListWorkloads400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid filter", "query 'kind' is not a known workload kind")),
		}, nil
	}
	filter := WorkloadListFilter{
		NamespaceID:    req.Params.NamespaceId,
		Kind:           req.Params.Kind,
		ImageSubstring: req.Params.Image,
	}

	items, next, err := s.store.ListWorkloads(ctx, filter, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	for i := range items {
		items[i] = withWorkloadLayer(items[i])
	}
	resp := WorkloadList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListWorkloads200JSONResponse(resp), nil
}

// CreateWorkload registers a new workload under a namespace.
func (s *Server) CreateWorkload(ctx context.Context, req CreateWorkloadRequestObject) (CreateWorkloadResponseObject, error) {
	body := *req.Body
	if body.Name == "" {
		return CreateWorkload400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'name' is required")),
		}, nil
	}
	if body.NamespaceId == (uuid.UUID{}) {
		return CreateWorkload400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'namespace_id' is required")),
		}, nil
	}
	if !body.Kind.Valid() {
		return CreateWorkload400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(
				problemBadRequest("Invalid field", "field 'kind' must be one of Deployment, StatefulSet, DaemonSet"),
			),
		}, nil
	}

	wl, err := s.store.UpsertWorkload(ctx, body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return CreateWorkload404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		if errors.Is(err, ErrConflict) {
			return CreateWorkload409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse(problemConflict(err)),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	wl = withWorkloadLayer(wl)

	loc := "/v1/workloads/"
	if wl.Id != nil {
		loc += wl.Id.String()
	}
	return CreateWorkload201JSONResponse{
		Body:    wl,
		Headers: CreateWorkload201ResponseHeaders{Location: loc},
	}, nil
}

// GetWorkload fetches a workload by id.
func (s *Server) GetWorkload(ctx context.Context, req GetWorkloadRequestObject) (GetWorkloadResponseObject, error) {
	wl, err := s.store.GetWorkload(ctx, req.Id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return GetWorkload404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return GetWorkload200JSONResponse(withWorkloadLayer(wl)), nil
}

// UpdateWorkload applies merge-patch updates.
func (s *Server) UpdateWorkload(ctx context.Context, req UpdateWorkloadRequestObject) (UpdateWorkloadResponseObject, error) {
	wl, err := s.store.UpdateWorkload(ctx, req.Id, *req.Body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return UpdateWorkload404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return UpdateWorkload200JSONResponse(withWorkloadLayer(wl)), nil
}

// DeleteWorkload removes a workload.
func (s *Server) DeleteWorkload(ctx context.Context, req DeleteWorkloadRequestObject) (DeleteWorkloadResponseObject, error) {
	if err := s.store.DeleteWorkload(ctx, req.Id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeleteWorkload404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return DeleteWorkload204Response{}, nil
}

// ── Services ─────────────────────────────────────────────────────────

// ListServices returns a paged list of services, optionally filtered by namespace_id.
func (s *Server) ListServices(ctx context.Context, req ListServicesRequestObject) (ListServicesResponseObject, error) {
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	cursor := ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}

	items, next, err := s.store.ListServices(ctx, req.Params.NamespaceId, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	for i := range items {
		items[i] = withServiceLayer(items[i])
	}
	resp := ServiceList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListServices200JSONResponse(resp), nil
}

// CreateService registers a new service under a namespace.
func (s *Server) CreateService(ctx context.Context, req CreateServiceRequestObject) (CreateServiceResponseObject, error) {
	body := *req.Body
	if body.Name == "" {
		return CreateService400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'name' is required")),
		}, nil
	}
	if body.NamespaceId == (uuid.UUID{}) {
		return CreateService400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'namespace_id' is required")),
		}, nil
	}
	if body.Type != nil && !isValidServiceType(*body.Type) {
		return CreateService400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(
				problemBadRequest("Invalid field", "field 'type' must be one of ClusterIP, NodePort, LoadBalancer, ExternalName"),
			),
		}, nil
	}

	svc, err := s.store.UpsertService(ctx, body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return CreateService404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		if errors.Is(err, ErrConflict) {
			return CreateService409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse(problemConflict(err)),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	svc = withServiceLayer(svc)

	loc := "/v1/services/"
	if svc.Id != nil {
		loc += svc.Id.String()
	}
	return CreateService201JSONResponse{
		Body:    svc,
		Headers: CreateService201ResponseHeaders{Location: loc},
	}, nil
}

// GetService fetches a service by id.
func (s *Server) GetService(ctx context.Context, req GetServiceRequestObject) (GetServiceResponseObject, error) {
	svc, err := s.store.GetService(ctx, req.Id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return GetService404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return GetService200JSONResponse(withServiceLayer(svc)), nil
}

// UpdateService applies merge-patch updates.
func (s *Server) UpdateService(ctx context.Context, req UpdateServiceRequestObject) (UpdateServiceResponseObject, error) {
	body := *req.Body
	if body.Type != nil && !isValidServiceType(*body.Type) {
		return UpdateService400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(
				problemBadRequest("Invalid field", "field 'type' must be one of ClusterIP, NodePort, LoadBalancer, ExternalName"),
			),
		}, nil
	}
	svc, err := s.store.UpdateService(ctx, req.Id, body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return UpdateService404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return UpdateService200JSONResponse(withServiceLayer(svc)), nil
}

// DeleteService removes a service.
func (s *Server) DeleteService(ctx context.Context, req DeleteServiceRequestObject) (DeleteServiceResponseObject, error) {
	if err := s.store.DeleteService(ctx, req.Id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeleteService404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return DeleteService204Response{}, nil
}

// ── Ingresses ────────────────────────────────────────────────────────

// ListIngresses returns a paged list of ingresses, optionally filtered by namespace_id.
func (s *Server) ListIngresses(ctx context.Context, req ListIngressesRequestObject) (ListIngressesResponseObject, error) {
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	cursor := ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}

	items, next, err := s.store.ListIngresses(ctx, req.Params.NamespaceId, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	for i := range items {
		items[i] = withIngressLayer(items[i])
	}
	resp := IngressList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListIngresses200JSONResponse(resp), nil
}

// CreateIngress registers a new ingress under a namespace.
func (s *Server) CreateIngress(ctx context.Context, req CreateIngressRequestObject) (CreateIngressResponseObject, error) {
	body := *req.Body
	if body.Name == "" {
		return CreateIngress400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'name' is required")),
		}, nil
	}
	if body.NamespaceId == (uuid.UUID{}) {
		return CreateIngress400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'namespace_id' is required")),
		}, nil
	}

	ing, err := s.store.UpsertIngress(ctx, body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return CreateIngress404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		if errors.Is(err, ErrConflict) {
			return CreateIngress409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse(problemConflict(err)),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	ing = withIngressLayer(ing)

	loc := "/v1/ingresses/"
	if ing.Id != nil {
		loc += ing.Id.String()
	}
	return CreateIngress201JSONResponse{
		Body:    ing,
		Headers: CreateIngress201ResponseHeaders{Location: loc},
	}, nil
}

// GetIngress fetches an ingress by id.
func (s *Server) GetIngress(ctx context.Context, req GetIngressRequestObject) (GetIngressResponseObject, error) {
	ing, err := s.store.GetIngress(ctx, req.Id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return GetIngress404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return GetIngress200JSONResponse(withIngressLayer(ing)), nil
}

// UpdateIngress applies merge-patch updates.
func (s *Server) UpdateIngress(ctx context.Context, req UpdateIngressRequestObject) (UpdateIngressResponseObject, error) {
	ing, err := s.store.UpdateIngress(ctx, req.Id, *req.Body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return UpdateIngress404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return UpdateIngress200JSONResponse(withIngressLayer(ing)), nil
}

// DeleteIngress removes an ingress.
func (s *Server) DeleteIngress(ctx context.Context, req DeleteIngressRequestObject) (DeleteIngressResponseObject, error) {
	if err := s.store.DeleteIngress(ctx, req.Id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeleteIngress404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return DeleteIngress204Response{}, nil
}

func isValidServiceType(t ServiceType) bool {
	switch t {
	case ClusterIP, NodePort, LoadBalancer, ExternalName:
		return true
	}
	return false
}

// ── Persistent Volumes ───────────────────────────────────────────────

// ListPersistentVolumes returns a paged list of PVs.
func (s *Server) ListPersistentVolumes(ctx context.Context, req ListPersistentVolumesRequestObject) (ListPersistentVolumesResponseObject, error) {
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	cursor := ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}

	items, next, err := s.store.ListPersistentVolumes(ctx, req.Params.ClusterId, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	for i := range items {
		items[i] = withPersistentVolumeLayer(items[i])
	}
	resp := PersistentVolumeList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListPersistentVolumes200JSONResponse(resp), nil
}

// CreatePersistentVolume registers a new PV under a cluster.
func (s *Server) CreatePersistentVolume(ctx context.Context, req CreatePersistentVolumeRequestObject) (CreatePersistentVolumeResponseObject, error) {
	body := *req.Body
	if body.Name == "" {
		return CreatePersistentVolume400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'name' is required")),
		}, nil
	}
	if body.ClusterId == (uuid.UUID{}) {
		return CreatePersistentVolume400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'cluster_id' is required")),
		}, nil
	}

	pv, err := s.store.UpsertPersistentVolume(ctx, body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return CreatePersistentVolume404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		if errors.Is(err, ErrConflict) {
			return CreatePersistentVolume409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse(problemConflict(err)),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	pv = withPersistentVolumeLayer(pv)

	loc := "/v1/persistentvolumes/"
	if pv.Id != nil {
		loc += pv.Id.String()
	}
	return CreatePersistentVolume201JSONResponse{
		Body:    pv,
		Headers: CreatePersistentVolume201ResponseHeaders{Location: loc},
	}, nil
}

// GetPersistentVolume fetches a PV by id.
func (s *Server) GetPersistentVolume(ctx context.Context, req GetPersistentVolumeRequestObject) (GetPersistentVolumeResponseObject, error) {
	pv, err := s.store.GetPersistentVolume(ctx, req.Id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return GetPersistentVolume404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return GetPersistentVolume200JSONResponse(withPersistentVolumeLayer(pv)), nil
}

// UpdatePersistentVolume applies merge-patch updates.
func (s *Server) UpdatePersistentVolume(ctx context.Context, req UpdatePersistentVolumeRequestObject) (UpdatePersistentVolumeResponseObject, error) {
	pv, err := s.store.UpdatePersistentVolume(ctx, req.Id, *req.Body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return UpdatePersistentVolume404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return UpdatePersistentVolume200JSONResponse(withPersistentVolumeLayer(pv)), nil
}

// DeletePersistentVolume removes a PV.
func (s *Server) DeletePersistentVolume(ctx context.Context, req DeletePersistentVolumeRequestObject) (DeletePersistentVolumeResponseObject, error) {
	if err := s.store.DeletePersistentVolume(ctx, req.Id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeletePersistentVolume404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return DeletePersistentVolume204Response{}, nil
}

// ── Persistent Volume Claims ─────────────────────────────────────────

// ListPersistentVolumeClaims returns a paged list of PVCs.
func (s *Server) ListPersistentVolumeClaims(
	ctx context.Context, req ListPersistentVolumeClaimsRequestObject,
) (ListPersistentVolumeClaimsResponseObject, error) {
	limit := 0
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	cursor := ""
	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}

	items, next, err := s.store.ListPersistentVolumeClaims(ctx, req.Params.NamespaceId, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	for i := range items {
		items[i] = withPersistentVolumeClaimLayer(items[i])
	}
	resp := PersistentVolumeClaimList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListPersistentVolumeClaims200JSONResponse(resp), nil
}

// CreatePersistentVolumeClaim registers a new PVC under a namespace.
func (s *Server) CreatePersistentVolumeClaim(
	ctx context.Context, req CreatePersistentVolumeClaimRequestObject,
) (CreatePersistentVolumeClaimResponseObject, error) {
	body := *req.Body
	if body.Name == "" {
		return CreatePersistentVolumeClaim400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'name' is required")),
		}, nil
	}
	if body.NamespaceId == (uuid.UUID{}) {
		return CreatePersistentVolumeClaim400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'namespace_id' is required")),
		}, nil
	}

	pvc, err := s.store.UpsertPersistentVolumeClaim(ctx, body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return CreatePersistentVolumeClaim404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		if errors.Is(err, ErrConflict) {
			return CreatePersistentVolumeClaim409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse(problemConflict(err)),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	pvc = withPersistentVolumeClaimLayer(pvc)

	loc := "/v1/persistentvolumeclaims/"
	if pvc.Id != nil {
		loc += pvc.Id.String()
	}
	return CreatePersistentVolumeClaim201JSONResponse{
		Body:    pvc,
		Headers: CreatePersistentVolumeClaim201ResponseHeaders{Location: loc},
	}, nil
}

// GetPersistentVolumeClaim fetches a PVC by id.
func (s *Server) GetPersistentVolumeClaim(
	ctx context.Context, req GetPersistentVolumeClaimRequestObject,
) (GetPersistentVolumeClaimResponseObject, error) {
	pvc, err := s.store.GetPersistentVolumeClaim(ctx, req.Id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return GetPersistentVolumeClaim404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return GetPersistentVolumeClaim200JSONResponse(withPersistentVolumeClaimLayer(pvc)), nil
}

// UpdatePersistentVolumeClaim applies merge-patch updates.
func (s *Server) UpdatePersistentVolumeClaim(
	ctx context.Context, req UpdatePersistentVolumeClaimRequestObject,
) (UpdatePersistentVolumeClaimResponseObject, error) {
	pvc, err := s.store.UpdatePersistentVolumeClaim(ctx, req.Id, *req.Body)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return UpdatePersistentVolumeClaim404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return UpdatePersistentVolumeClaim200JSONResponse(withPersistentVolumeClaimLayer(pvc)), nil
}

// DeletePersistentVolumeClaim removes a PVC.
func (s *Server) DeletePersistentVolumeClaim(
	ctx context.Context, req DeletePersistentVolumeClaimRequestObject,
) (DeletePersistentVolumeClaimResponseObject, error) {
	if err := s.store.DeletePersistentVolumeClaim(ctx, req.Id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeletePersistentVolumeClaim404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	return DeletePersistentVolumeClaim204Response{}, nil
}

// ── Reconcile handlers (ADR-0009: push collector) ────────────────────

// ReconcileNodes deletes every node of the given cluster whose name is
// not in keep_names.
func (s *Server) ReconcileNodes(ctx context.Context, req ReconcileNodesRequestObject) (ReconcileNodesResponseObject, error) {
	body := *req.Body
	if body.ClusterId == (uuid.UUID{}) {
		return ReconcileNodes400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'cluster_id' is required")),
		}, nil
	}
	n, err := s.store.DeleteNodesNotIn(ctx, body.ClusterId, body.KeepNames)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return ReconcileNodes200JSONResponse(ReconcileResult{Deleted: n}), nil
}

// ReconcileNamespaces deletes every namespace of the given cluster whose
// name is not in keep_names.
func (s *Server) ReconcileNamespaces(ctx context.Context, req ReconcileNamespacesRequestObject) (ReconcileNamespacesResponseObject, error) {
	body := *req.Body
	if body.ClusterId == (uuid.UUID{}) {
		return ReconcileNamespaces400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'cluster_id' is required")),
		}, nil
	}
	n, err := s.store.DeleteNamespacesNotIn(ctx, body.ClusterId, body.KeepNames)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return ReconcileNamespaces200JSONResponse(ReconcileResult{Deleted: n}), nil
}

// ReconcilePersistentVolumes deletes every PV of the given cluster whose
// name is not in keep_names.
func (s *Server) ReconcilePersistentVolumes(
	ctx context.Context, req ReconcilePersistentVolumesRequestObject,
) (ReconcilePersistentVolumesResponseObject, error) {
	body := *req.Body
	if body.ClusterId == (uuid.UUID{}) {
		return ReconcilePersistentVolumes400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'cluster_id' is required")),
		}, nil
	}
	n, err := s.store.DeletePersistentVolumesNotIn(ctx, body.ClusterId, body.KeepNames)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return ReconcilePersistentVolumes200JSONResponse(ReconcileResult{Deleted: n}), nil
}

// ReconcilePods deletes every pod of the given namespace whose name is
// not in keep_names.
func (s *Server) ReconcilePods(ctx context.Context, req ReconcilePodsRequestObject) (ReconcilePodsResponseObject, error) {
	body := *req.Body
	if body.NamespaceId == (uuid.UUID{}) {
		return ReconcilePods400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'namespace_id' is required")),
		}, nil
	}
	n, err := s.store.DeletePodsNotIn(ctx, body.NamespaceId, body.KeepNames)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return ReconcilePods200JSONResponse(ReconcileResult{Deleted: n}), nil
}

// ReconcileWorkloads deletes every workload of the given namespace whose
// (kind, name) tuple is not in the parallel keep_kinds/keep_names arrays.
func (s *Server) ReconcileWorkloads(ctx context.Context, req ReconcileWorkloadsRequestObject) (ReconcileWorkloadsResponseObject, error) {
	body := *req.Body
	if body.NamespaceId == (uuid.UUID{}) {
		return ReconcileWorkloads400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'namespace_id' is required")),
		}, nil
	}
	if len(body.KeepKinds) != len(body.KeepNames) {
		return ReconcileWorkloads400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(
				problemBadRequest("Invalid request body", "keep_kinds and keep_names must have equal length"),
			),
		}, nil
	}
	n, err := s.store.DeleteWorkloadsNotIn(ctx, body.NamespaceId, body.KeepKinds, body.KeepNames)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return ReconcileWorkloads200JSONResponse(ReconcileResult{Deleted: n}), nil
}

// ReconcileServices deletes every service of the given namespace whose
// name is not in keep_names.
func (s *Server) ReconcileServices(ctx context.Context, req ReconcileServicesRequestObject) (ReconcileServicesResponseObject, error) {
	body := *req.Body
	if body.NamespaceId == (uuid.UUID{}) {
		return ReconcileServices400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'namespace_id' is required")),
		}, nil
	}
	n, err := s.store.DeleteServicesNotIn(ctx, body.NamespaceId, body.KeepNames)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return ReconcileServices200JSONResponse(ReconcileResult{Deleted: n}), nil
}

// ReconcileIngresses deletes every ingress of the given namespace whose
// name is not in keep_names.
func (s *Server) ReconcileIngresses(ctx context.Context, req ReconcileIngressesRequestObject) (ReconcileIngressesResponseObject, error) {
	body := *req.Body
	if body.NamespaceId == (uuid.UUID{}) {
		return ReconcileIngresses400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'namespace_id' is required")),
		}, nil
	}
	n, err := s.store.DeleteIngressesNotIn(ctx, body.NamespaceId, body.KeepNames)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return ReconcileIngresses200JSONResponse(ReconcileResult{Deleted: n}), nil
}

// ReconcilePersistentVolumeClaims deletes every PVC of the given namespace
// whose name is not in keep_names.
func (s *Server) ReconcilePersistentVolumeClaims(
	ctx context.Context, req ReconcilePersistentVolumeClaimsRequestObject,
) (ReconcilePersistentVolumeClaimsResponseObject, error) {
	body := *req.Body
	if body.NamespaceId == (uuid.UUID{}) {
		return ReconcilePersistentVolumeClaims400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "field 'namespace_id' is required")),
		}, nil
	}
	n, err := s.store.DeletePersistentVolumeClaimsNotIn(ctx, body.NamespaceId, body.KeepNames)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return ReconcilePersistentVolumeClaims200JSONResponse(ReconcileResult{Deleted: n}), nil
}
