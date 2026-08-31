package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/httputil"
)

var (
	errRunbookURLInvalid              = errors.New("runbook_url is not a valid URL")
	errRunbookURLScheme               = errors.New("runbook_url must use http or https scheme")
	errImageVersionsDisabled          = errors.New("image_versions_enabled is false")
	errImageVersionsEnricherMissing   = errors.New("enricher not available")
	errImageRegistryHostnameConflict  = errors.New("hostname already exists")
	errImageOriginMappingNameConflict = errors.New("image_name already exists")
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

// Server implements StrictServerInterface for the longue-vue REST API.
type Server struct {
	version       string
	store         Store
	cookiePolicy  auth.SecureCookiePolicy
	oidc          *auth.OIDCProvider // nil when OIDC is not configured
	loginLimiter  *LoginRateLimiter  // per-IP login rate limiter (ADR-0007 IMP-009)
	verifyLimiter *VerifyRateLimiter // per-IP rate limit on the ingest verify endpoint (ADR-0016 §5)
	// trustedProxies is the operator-supplied CIDR list whose
	// X-Forwarded-For / X-Forwarded-Proto headers are honored
	// (ADR-0017 §2). Empty/nil means no peer is trusted, which is the
	// secure default; the existing pentest-reproducer test in
	// internal/httputil/httputil_test.go documents the chosen behavior.
	trustedProxies []*net.IPNet
	// enricher is the image-versions enricher trigger. Nil when the feature
	// is disabled; SetEnricher injects it after NewServer.
	enricher EnricherTrigger
}

// NewServer wires the handlers with a persistence backend and the build
// version reported on health probes. `cookiePolicy` governs the Secure
// flag on session cookies (see ADR-0007); auto = mirror request scheme.
// `oidc` may be nil to disable the OIDC flow entirely. `verifyLimiter`
// gates POST /v1/auth/verify (ADR-0016 §5); pass nil to disable rate
// limiting (test fixtures usually do).
func NewServer(
	version string,
	store Store,
	cookiePolicy auth.SecureCookiePolicy,
	oidc *auth.OIDCProvider,
	loginLimiter *LoginRateLimiter,
	verifyLimiter *VerifyRateLimiter,
) *Server {
	return &Server{
		version:       version,
		store:         store,
		cookiePolicy:  cookiePolicy,
		oidc:          oidc,
		loginLimiter:  loginLimiter,
		verifyLimiter: verifyLimiter,
	}
}

// SetTrustedProxies installs the operator-supplied CIDR list at startup.
// Pass nil or an empty slice to ignore X-Forwarded-* unconditionally —
// the secure default. longue-vue's main.go calls this once after parsing
// LONGUE_VUE_TRUSTED_PROXIES; tests typically leave it unset.
func (s *Server) SetTrustedProxies(p []*net.IPNet) {
	s.trustedProxies = p
}

// SetEnricher injects the image-versions enricher trigger. Called by main.go
// after NewServer when the feature is enabled.
func (s *Server) SetEnricher(e EnricherTrigger) {
	s.enricher = e
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

// listBadRequest builds the 400 problem body for ErrInvalidCursor /
// ErrInvalidSort returned by List* store methods.
func listBadRequest(err error) Problem {
	detail := err.Error()
	return Problem{Type: "about:blank", Title: "Bad Request", Status: http.StatusBadRequest, Detail: &detail}
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

// ListClusters returns a paged cluster list. name= is the uniform
// ci-substring/glob filter (it USED to be an exact-match short-circuit
// to GetClusterByName; recon 2026-07-10 found zero live callers of the
// exact semantics — the push collector bootstraps via the idempotent
// POST /v1/clusters, ADR-0016).
//
//nolint:gocyclo // parameter extraction; if-chains are unavoidable for optional pointer params
func (s *Server) ListClusters(ctx context.Context, req ListClustersRequestObject) (ListClustersResponseObject, error) {
	page := ListPage{}
	if req.Params.Limit != nil {
		page.Limit = *req.Params.Limit
	}
	if req.Params.Cursor != nil {
		page.Cursor = *req.Params.Cursor
	}
	if req.Params.Sort != nil {
		page.Sort = *req.Params.Sort
	}
	if req.Params.Order != nil {
		page.Order = string(*req.Params.Order)
	}
	filter := ClusterListFilter{}
	if req.Params.Name != nil && *req.Params.Name != "" {
		n := *req.Params.Name
		filter.Name = &n
	}
	if req.Params.IncludeTerminated != nil {
		filter.IncludeTerminated = *req.Params.IncludeTerminated
	}

	cutoff, staleEnabled := s.clusterStaleCutoff(ctx)
	if req.Params.Stale != nil {
		if staleEnabled {
			v := *req.Params.Stale
			filter.Stale = &v
			filter.StaleCutoff = cutoff
		} else if *req.Params.Stale {
			// Feature disabled: nothing is stale by definition.
			return ListClusters200JSONResponse(ClusterList{Items: []Cluster{}}), nil
		}
	}

	items, next, err := s.store.ListClusters(ctx, filter, page)
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrInvalidSort) {
			return ListClusters400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(listBadRequest(err)),
			}, nil
		}
		return nil, storeErr("listClusters", err)
	}
	for i := range items {
		items[i] = withClusterStaleness(withClusterLayer(items[i]), cutoff, staleEnabled)
	}
	resp := ClusterList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListClusters200JSONResponse(resp), nil
}

// CreateCluster registers a cluster idempotently on its name.
//
// On insert: returns 201 Created with the new row. On hit (a cluster with the
// same name already exists): returns 200 OK with the existing row unchanged.
// The request body is ignored on hit — callers wanting to update fields on
// an existing cluster must follow up with PATCH /v1/clusters/{id}.
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

	c, created, err := s.store.EnsureCluster(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("createCluster: %w", err)
	}
	c = withClusterLayer(c)
	cutoff, staleEnabled := s.clusterStaleCutoff(ctx)
	c = withClusterStaleness(c, cutoff, staleEnabled)
	if !created {
		// Steady-state collector tick: the ensure only refreshed the
		// last_seen_at heartbeat. Drop the per-tick audit row (ADR-0024,
		// same as CreateNode's OutcomeNoChange). Trade-off: the rare
		// RESTORE ensure is also skipped here — the resurrection stays
		// traceable via its clusters_history `restore` row.
		SetAuditSkip(ctx)
	}

	loc := "/v1/clusters/"
	if c.Id != nil {
		loc += c.Id.String()
	}
	if created {
		return CreateCluster201JSONResponse{
			Body:    c,
			Headers: CreateCluster201ResponseHeaders{Location: &loc},
		}, nil
	}
	return CreateCluster200JSONResponse{
		Body:    c,
		Headers: CreateCluster200ResponseHeaders{Location: &loc},
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
	cutoff, staleEnabled := s.clusterStaleCutoff(ctx)
	return GetCluster200JSONResponse(withClusterStaleness(withClusterLayer(c), cutoff, staleEnabled)), nil
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
	cutoff, staleEnabled := s.clusterStaleCutoff(ctx)
	return UpdateCluster200JSONResponse(withClusterStaleness(withClusterLayer(c), cutoff, staleEnabled)), nil
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
		slog.Error(
			"deleteCluster: count children failed, proceeding without cascade counts",
			slog.Any("error", err),
			slog.String("cluster_id", req.Id.String()),
		)
	}

	if err := s.store.SoftDeleteCluster(ctx, req.Id); err != nil {
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

// ListNodes returns a paged list of nodes, optionally filtered by cluster_id and/or name.
//
//nolint:gocyclo // parameter extraction and error mapping; complexity is not branching
func (s *Server) ListNodes(ctx context.Context, req ListNodesRequestObject) (ListNodesResponseObject, error) {
	page := ListPage{}
	if req.Params.Limit != nil {
		page.Limit = *req.Params.Limit
	}
	if req.Params.Cursor != nil {
		page.Cursor = *req.Params.Cursor
	}
	if req.Params.Sort != nil {
		page.Sort = *req.Params.Sort
	}
	if req.Params.Order != nil {
		page.Order = string(*req.Params.Order)
	}
	filter := NodeListFilter{ClusterID: req.Params.ClusterId}
	if req.Params.Name != nil {
		n := *req.Params.Name
		filter.Name = &n
	}
	if req.Params.IncludeTerminated != nil {
		filter.IncludeTerminated = *req.Params.IncludeTerminated
	}

	items, next, err := s.store.ListNodes(ctx, filter, page)
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrInvalidSort) {
			return ListNodes400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(listBadRequest(err)),
			}, nil
		}
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

	n, outcome, err := s.store.UpsertNode(ctx, body)
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
	if outcome == OutcomeNoChange {
		SetAuditSkip(ctx)
	}
	n = withNodeLayer(n)

	loc := "/v1/nodes/"
	if n.Id != nil {
		loc += n.Id.String()
	}
	return CreateNode201JSONResponse{
		Body:    n,
		Headers: CreateNode201ResponseHeaders{Location: &loc},
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

// ListNamespaces returns a paged list of namespaces, optionally filtered by cluster_id and/or name.
//
//nolint:gocyclo // parameter extraction and error mapping; complexity is not branching
func (s *Server) ListNamespaces(ctx context.Context, req ListNamespacesRequestObject) (ListNamespacesResponseObject, error) {
	page := ListPage{}
	if req.Params.Limit != nil {
		page.Limit = *req.Params.Limit
	}
	if req.Params.Cursor != nil {
		page.Cursor = *req.Params.Cursor
	}
	if req.Params.Sort != nil {
		page.Sort = *req.Params.Sort
	}
	if req.Params.Order != nil {
		page.Order = string(*req.Params.Order)
	}
	filter := NamespaceListFilter{ClusterID: req.Params.ClusterId}
	if req.Params.Name != nil {
		n := *req.Params.Name
		filter.Name = &n
	}
	if req.Params.IncludeTerminated != nil {
		filter.IncludeTerminated = *req.Params.IncludeTerminated
	}

	items, next, err := s.store.ListNamespaces(ctx, filter, page)
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrInvalidSort) {
			return ListNamespaces400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(listBadRequest(err)),
			}, nil
		}
		return nil, storeErr("listNamespaces", err)
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

	n, outcome, err := s.store.UpsertNamespace(ctx, body)
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
	if outcome == OutcomeNoChange {
		SetAuditSkip(ctx)
	}
	n = withNamespaceLayer(n)

	loc := "/v1/namespaces/"
	if n.Id != nil {
		loc += n.Id.String()
	}
	return CreateNamespace201JSONResponse{
		Body:    n,
		Headers: CreateNamespace201ResponseHeaders{Location: &loc},
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
	if err := s.store.SoftDeleteNamespace(ctx, req.Id); err != nil {
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
	page := ListPage{}
	if req.Params.Limit != nil {
		page.Limit = *req.Params.Limit
	}
	if req.Params.Cursor != nil {
		page.Cursor = *req.Params.Cursor
	}
	if req.Params.Sort != nil {
		page.Sort = *req.Params.Sort
	}
	if req.Params.Order != nil {
		page.Order = string(*req.Params.Order)
	}
	filter := PodListFilter{
		NamespaceID:    req.Params.NamespaceId,
		NodeName:       req.Params.NodeName,
		WorkloadID:     req.Params.WorkloadId,
		ImageSubstring: req.Params.Image,
		Name:           req.Params.Name,
	}

	items, next, err := s.store.ListPods(ctx, filter, page)
	if err != nil {
		if errors.Is(err, ErrInvalidSort) || errors.Is(err, ErrInvalidCursor) {
			return ListPods400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid parameter", err.Error())),
			}, nil
		}
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

	p, outcome, err := s.store.UpsertPod(ctx, body)
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
	if outcome == OutcomeNoChange {
		SetAuditSkip(ctx)
	}
	p = withPodLayer(p)

	loc := "/v1/pods/"
	if p.Id != nil {
		loc += p.Id.String()
	}
	return CreatePod201JSONResponse{
		Body:    p,
		Headers: CreatePod201ResponseHeaders{Location: &loc},
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
	decorated := withPodLayer(p)
	var containers []map[string]any
	if decorated.Containers != nil {
		containers = *decorated.Containers
	}
	return GetPodEnrichedResponse{
		Pod:                decorated,
		ContainersVersions: EnrichContainersVersions(ctx, s.store, containers),
	}, nil
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
//
//nolint:gocritic,gocyclo // req is passed by value to satisfy the oapi-codegen ServerInterface signature; link-aware filter branches inflate cyclo count.
func (s *Server) ListWorkloads(ctx context.Context, req ListWorkloadsRequestObject) (ListWorkloadsResponseObject, error) {
	if req.Params.Kind != nil && !req.Params.Kind.Valid() {
		return ListWorkloads400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid filter", "query 'kind' is not a known workload kind")),
		}, nil
	}

	page := ListPage{}
	if req.Params.Limit != nil {
		page.Limit = *req.Params.Limit
	}
	if req.Params.Cursor != nil {
		page.Cursor = *req.Params.Cursor
	}
	if req.Params.Sort != nil {
		page.Sort = *req.Params.Sort
	}
	if req.Params.Order != nil {
		page.Order = string(*req.Params.Order)
	}

	includeTerminated := false
	if req.Params.IncludeTerminated != nil {
		includeTerminated = *req.Params.IncludeTerminated
	}

	filter := WorkloadListFilter{
		NamespaceID:       req.Params.NamespaceId,
		Kind:              req.Params.Kind,
		ImageSubstring:    req.Params.Image,
		IncludeTerminated: includeTerminated,
		// ADR-0029 link-aware filters. id wins over name; the store layer
		// enforces the precedence and normalises the name.
		ApplicationID:   req.Params.ApplicationId,
		ApplicationName: req.Params.ApplicationName,
		Unlinked:        req.Params.Unlinked,
		Name:            req.Params.Name,
	}

	items, next, err := s.store.ListWorkloads(ctx, filter, page)
	if err != nil {
		if errors.Is(err, ErrInvalidSort) || errors.Is(err, ErrInvalidCursor) {
			return ListWorkloads400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid parameter", err.Error())),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}

	for i := range items {
		items[i] = withWorkloadLayer(items[i])
	}
	// ADR-0029 §6: project the read-only inherited DICT. Bulk-fetches the
	// distinct linked applications in one query (no N+1).
	decorateWorkloadsDICT(ctx, s.store, items)
	// ADR-0022/0032: opt-in per-container latest-tag + eol_status enrichment.
	// Off by default so plain list responses stay cheap.
	s.enrichWorkloadsContainersVersions(ctx, req.Params.Include, items)
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

	wl, outcome, err := s.store.UpsertWorkload(ctx, body)
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
	if outcome == OutcomeNoChange {
		SetAuditSkip(ctx)
	}
	wl = withWorkloadLayer(wl)
	wl = decorateWorkloadDICT(ctx, s.store, wl)

	loc := "/v1/workloads/"
	if wl.Id != nil {
		loc += wl.Id.String()
	}
	return CreateWorkload201JSONResponse{
		Body:    wl,
		Headers: CreateWorkload201ResponseHeaders{Location: &loc},
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
	decorated := withWorkloadLayer(wl)
	decorated = decorateWorkloadDICT(ctx, s.store, decorated)
	var containers []map[string]any
	if decorated.Containers != nil {
		containers = *decorated.Containers
	}
	return GetWorkloadEnrichedResponse{
		Workload:           decorated,
		ContainersVersions: EnrichContainersVersions(ctx, s.store, containers),
	}, nil
}

// UpdateWorkload applies merge-patch updates.
//
// ADR-0029 §2.3: when the body carries application_id or application_name,
// resolve to a concrete UUID first (id wins on conflict). The store layer
// is unaware of names; the handler is the resolution boundary so the
// store contract stays a thin SQL projection.
func (s *Server) UpdateWorkload(ctx context.Context, req UpdateWorkloadRequestObject) (UpdateWorkloadResponseObject, error) {
	body := *req.Body
	if body.ApplicationId != nil || (body.ApplicationName != nil && *body.ApplicationName != "") {
		resolved, err := ResolveApplicationID(ctx, s.store, body.ApplicationId, body.ApplicationName)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return UpdateWorkload400ApplicationProblemPlusJSONResponse{
					BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid application", err.Error())),
				}, nil
			}
			return nil, fmt.Errorf("resolve application: %w", err)
		}
		body.ApplicationId = resolved
		body.ApplicationName = nil // store doesn't read this.
	}
	// ADR-0029 §2.3 unlink: an explicit `"application_id": null` clears the FK
	// (detected pre-decode by DetectWorkloadUnlinkMiddleware). An explicit id
	// value wins over null, so only clear when no id was resolved.
	clearApplication := body.ApplicationId == nil && workloadClearApplicationFromCtx(ctx)
	wl, err := s.store.UpdateWorkload(ctx, req.Id, body, clearApplication)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return UpdateWorkload404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("store: %w", err)
	}
	decorated := decorateWorkloadDICT(ctx, s.store, withWorkloadLayer(wl))
	return UpdateWorkload200JSONResponse(decorated), nil
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

// ListServices returns a paged list of services, optionally filtered by namespace_id and/or name.
//
//nolint:gocyclo // parameter extraction and error mapping; complexity is not branching
func (s *Server) ListServices(ctx context.Context, req ListServicesRequestObject) (ListServicesResponseObject, error) {
	page := ListPage{}
	if req.Params.Limit != nil {
		page.Limit = *req.Params.Limit
	}
	if req.Params.Cursor != nil {
		page.Cursor = *req.Params.Cursor
	}
	if req.Params.Sort != nil {
		page.Sort = *req.Params.Sort
	}
	if req.Params.Order != nil {
		page.Order = string(*req.Params.Order)
	}
	filter := ServiceListFilter{NamespaceID: req.Params.NamespaceId}
	if req.Params.Name != nil {
		n := *req.Params.Name
		filter.Name = &n
	}

	items, next, err := s.store.ListServices(ctx, filter, page)
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrInvalidSort) {
			return ListServices400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(listBadRequest(err)),
			}, nil
		}
		return nil, storeErr("listServices", err)
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

	svc, outcome, err := s.store.UpsertService(ctx, body)
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
	if outcome == OutcomeNoChange {
		SetAuditSkip(ctx)
	}
	svc = withServiceLayer(svc)

	loc := "/v1/services/"
	if svc.Id != nil {
		loc += svc.Id.String()
	}
	return CreateService201JSONResponse{
		Body:    svc,
		Headers: CreateService201ResponseHeaders{Location: &loc},
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

// ListIngresses returns a paged list of ingresses, optionally filtered by namespace_id and/or name.
//
//nolint:gocyclo // parameter extraction and error mapping; complexity is not branching
func (s *Server) ListIngresses(ctx context.Context, req ListIngressesRequestObject) (ListIngressesResponseObject, error) {
	page := ListPage{}
	if req.Params.Limit != nil {
		page.Limit = *req.Params.Limit
	}
	if req.Params.Cursor != nil {
		page.Cursor = *req.Params.Cursor
	}
	if req.Params.Sort != nil {
		page.Sort = *req.Params.Sort
	}
	if req.Params.Order != nil {
		page.Order = string(*req.Params.Order)
	}
	filter := IngressListFilter{NamespaceID: req.Params.NamespaceId}
	if req.Params.Name != nil {
		n := *req.Params.Name
		filter.Name = &n
	}

	items, next, err := s.store.ListIngresses(ctx, filter, page)
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrInvalidSort) {
			return ListIngresses400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(listBadRequest(err)),
			}, nil
		}
		return nil, storeErr("listIngresses", err)
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

	ing, outcome, err := s.store.UpsertIngress(ctx, body)
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
	if outcome == OutcomeNoChange {
		SetAuditSkip(ctx)
	}
	ing = withIngressLayer(ing)

	loc := "/v1/ingresses/"
	if ing.Id != nil {
		loc += ing.Id.String()
	}
	return CreateIngress201JSONResponse{
		Body:    ing,
		Headers: CreateIngress201ResponseHeaders{Location: &loc},
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
//
//nolint:gocyclo // parameter extraction and error mapping; complexity is not branching
func (s *Server) ListPersistentVolumes(ctx context.Context, req ListPersistentVolumesRequestObject) (ListPersistentVolumesResponseObject, error) {
	page := ListPage{}
	if req.Params.Limit != nil {
		page.Limit = *req.Params.Limit
	}
	if req.Params.Cursor != nil {
		page.Cursor = *req.Params.Cursor
	}
	if req.Params.Sort != nil {
		page.Sort = *req.Params.Sort
	}
	if req.Params.Order != nil {
		page.Order = string(*req.Params.Order)
	}
	filter := PersistentVolumeListFilter{ClusterID: req.Params.ClusterId}
	if req.Params.Name != nil {
		n := *req.Params.Name
		filter.Name = &n
	}

	items, next, err := s.store.ListPersistentVolumes(ctx, filter, page)
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrInvalidSort) {
			return ListPersistentVolumes400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(listBadRequest(err)),
			}, nil
		}
		return nil, storeErr("listPersistentVolumes", err)
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

	pv, outcome, err := s.store.UpsertPersistentVolume(ctx, body)
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
	if outcome == OutcomeNoChange {
		SetAuditSkip(ctx)
	}
	pv = withPersistentVolumeLayer(pv)

	loc := "/v1/persistentvolumes/"
	if pv.Id != nil {
		loc += pv.Id.String()
	}
	return CreatePersistentVolume201JSONResponse{
		Body:    pv,
		Headers: CreatePersistentVolume201ResponseHeaders{Location: &loc},
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
//
//nolint:gocyclo // parameter extraction and error mapping; complexity is not branching
func (s *Server) ListPersistentVolumeClaims(
	ctx context.Context, req ListPersistentVolumeClaimsRequestObject,
) (ListPersistentVolumeClaimsResponseObject, error) {
	page := ListPage{}
	if req.Params.Limit != nil {
		page.Limit = *req.Params.Limit
	}
	if req.Params.Cursor != nil {
		page.Cursor = *req.Params.Cursor
	}
	if req.Params.Sort != nil {
		page.Sort = *req.Params.Sort
	}
	if req.Params.Order != nil {
		page.Order = string(*req.Params.Order)
	}
	filter := PersistentVolumeClaimListFilter{NamespaceID: req.Params.NamespaceId}
	if req.Params.Name != nil {
		n := *req.Params.Name
		filter.Name = &n
	}

	items, next, err := s.store.ListPersistentVolumeClaims(ctx, filter, page)
	if err != nil {
		if errors.Is(err, ErrInvalidCursor) || errors.Is(err, ErrInvalidSort) {
			return ListPersistentVolumeClaims400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(listBadRequest(err)),
			}, nil
		}
		return nil, storeErr("listPersistentVolumeClaims", err)
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

	pvc, outcome, err := s.store.UpsertPersistentVolumeClaim(ctx, body)
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
	if outcome == OutcomeNoChange {
		SetAuditSkip(ctx)
	}
	pvc = withPersistentVolumeClaimLayer(pvc)

	loc := "/v1/persistentvolumeclaims/"
	if pvc.Id != nil {
		loc += pvc.Id.String()
	}
	return CreatePersistentVolumeClaim201JSONResponse{
		Body:    pvc,
		Headers: CreatePersistentVolumeClaim201ResponseHeaders{Location: &loc},
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
	if n == 0 {
		SetAuditSkipReason(ctx, "reconcile_empty")
		SetAuditSkip(ctx)
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
	if n == 0 {
		SetAuditSkipReason(ctx, "reconcile_empty")
		SetAuditSkip(ctx)
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
	if n == 0 {
		SetAuditSkipReason(ctx, "reconcile_empty")
		SetAuditSkip(ctx)
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
	if n == 0 {
		SetAuditSkipReason(ctx, "reconcile_empty")
		SetAuditSkip(ctx)
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
	if n == 0 {
		SetAuditSkipReason(ctx, "reconcile_empty")
		SetAuditSkip(ctx)
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
	if n == 0 {
		SetAuditSkipReason(ctx, "reconcile_empty")
		SetAuditSkip(ctx)
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
	if n == 0 {
		SetAuditSkipReason(ctx, "reconcile_empty")
		SetAuditSkip(ctx)
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
	if n == 0 {
		SetAuditSkipReason(ctx, "reconcile_empty")
		SetAuditSkip(ctx)
	}
	return ReconcilePersistentVolumeClaims200JSONResponse(ReconcileResult{Deleted: n}), nil
}

// ── Image versions ────────────────────────────────────────────────────

// imageVersionRowToVariant converts a flat DB row to an ImageVersionVariant.
// Row is taken by pointer to avoid copying ~160 bytes on each call.
func imageVersionRowToVariant(row *ImageVersionRow) ImageVersionVariant {
	v := ImageVersionVariant{
		Variant:       row.Variant,
		LatestTag:     row.LatestTag,
		Source:        ImageVersionVariantSource(row.Source),
		LastCheckedAt: row.LastCheckedAt,
		LastError:     row.LastError,
		LastErrorAt:   row.LastErrorAt,
	}
	if len(row.Annotation) > 0 {
		var ann map[string]any
		if err := json.Unmarshal(row.Annotation, &ann); err == nil {
			v.Annotation = &ann
		}
	}
	return v
}

// repoViewToImageVersion converts an ImageVersionRepoView to the API ImageVersion.
// Iterates by index to avoid copying each ImageVersionRow (~160 bytes).
func repoViewToImageVersion(rv ImageVersionRepoView) ImageVersion {
	variants := make([]ImageVersionVariant, 0, len(rv.Variants))
	for i := range rv.Variants {
		variants = append(variants, imageVersionRowToVariant(&rv.Variants[i]))
	}
	return ImageVersion{
		ImageRepo: rv.ImageRepo,
		Registry:  rv.Registry,
		Variants:  variants,
	}
}

// ListImageVersions returns a paginated list of distinct image repos with their variants.
func (s *Server) ListImageVersions(ctx context.Context, req ListImageVersionsRequestObject) (ListImageVersionsResponseObject, error) {
	p := req.Params
	params := ImageVersionListParams{
		Limit:             50,
		LastCheckedBefore: p.LastCheckedBefore,
		HasError:          p.HasError,
	}
	if p.Limit != nil {
		params.Limit = *p.Limit
	}
	if p.Cursor != nil {
		params.Cursor = *p.Cursor
	}
	if p.Registry != nil {
		params.Registry = *p.Registry
	}
	if p.ImageRepo != nil {
		params.ImageRepoLike = *p.ImageRepo
	}
	if p.Variant != nil {
		params.Variant = *p.Variant
	}

	views, next, err := s.store.ListImageVersionsByRepo(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	items := make([]ImageVersion, 0, len(views))
	for _, rv := range views {
		items = append(items, repoViewToImageVersion(rv))
	}
	resp := ListImageVersions200JSONResponse(ImageVersionList{
		Items: items,
	})
	if next != "" {
		resp.NextCursor = &next
	}
	return resp, nil
}

// GetImageVersion returns all variant rows for a single image_repo.
func (s *Server) GetImageVersion(ctx context.Context, req GetImageVersionRequestObject) (GetImageVersionResponseObject, error) {
	rows, err := s.store.GetImageVersionsByRepo(ctx, req.ImageRepo)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if len(rows) == 0 {
		return GetImageVersion404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
		}, nil
	}
	rv := ImageVersionRepoView{
		ImageRepo: rows[0].ImageRepo,
		Registry:  rows[0].Registry,
		Variants:  rows,
	}
	return GetImageVersion200JSONResponse(repoViewToImageVersion(rv)), nil
}

// RefreshImageVersions triggers an immediate enrichment cycle (admin only).
func (s *Server) RefreshImageVersions(ctx context.Context, _ RefreshImageVersionsRequestObject) (RefreshImageVersionsResponseObject, error) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	if !settings.ImageVersionsEnabled {
		return RefreshImageVersions409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse(problemConflict(errImageVersionsDisabled)),
		}, nil
	}
	if s.enricher == nil {
		return RefreshImageVersions409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse(problemConflict(errImageVersionsEnricherMissing)),
		}, nil
	}
	running := s.enricher.Trigger()
	return RefreshImageVersions202JSONResponse(ImageVersionRefreshResponse{
		Queued:         !running,
		AlreadyRunning: running,
	}), nil
}

// ── Image registries ─────────────────────────────────────────────────

// ListImageRegistries lists all image registry allowlist rows.
func (s *Server) ListImageRegistries(ctx context.Context, _ ListImageRegistriesRequestObject) (ListImageRegistriesResponseObject, error) {
	items, err := s.store.ListImageRegistries(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return ListImageRegistries200JSONResponse{Items: items}, nil
}

// CreateImageRegistry inserts a new registry row.
func (s *Server) CreateImageRegistry(ctx context.Context, req CreateImageRegistryRequestObject) (CreateImageRegistryResponseObject, error) {
	in := req.Body
	if in.Hostname == "" {
		return CreateImageRegistry400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Bad Request", "hostname is required")),
		}, nil
	}
	if in.RateLimitPerSec <= 0 {
		return CreateImageRegistry400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Bad Request", "rate_limit_per_sec must be > 0")),
		}, nil
	}
	out, err := s.store.CreateImageRegistry(ctx, *in)
	switch {
	case errors.Is(err, ErrConflict):
		return CreateImageRegistry409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse(problemConflict(errImageRegistryHostnameConflict)),
		}, nil
	case err != nil:
		return nil, fmt.Errorf("store: %w", err)
	}
	return CreateImageRegistry201JSONResponse(out), nil
}

// PatchImageRegistry applies a merge-patch to an existing registry row.
func (s *Server) PatchImageRegistry(ctx context.Context, req PatchImageRegistryRequestObject) (PatchImageRegistryResponseObject, error) {
	out, err := s.store.UpdateImageRegistry(ctx, req.Hostname, decodePathPrefix(req.PathPrefix), *req.Body)
	switch {
	case errors.Is(err, ErrNotFound):
		return PatchImageRegistry404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
		}, nil
	case err != nil:
		return nil, fmt.Errorf("store: %w", err)
	}
	return PatchImageRegistry200JSONResponse(out), nil
}

// DeleteImageRegistry removes a registry from the allowlist.
func (s *Server) DeleteImageRegistry(ctx context.Context, req DeleteImageRegistryRequestObject) (DeleteImageRegistryResponseObject, error) {
	err := s.store.DeleteImageRegistry(ctx, req.Hostname, decodePathPrefix(req.PathPrefix))
	switch {
	case errors.Is(err, ErrNotFound):
		return DeleteImageRegistry404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
		}, nil
	case err != nil:
		return nil, fmt.Errorf("store: %w", err)
	}
	return DeleteImageRegistry204Response{}, nil
}

// ── Image origin mappings (ADR-0030) ─────────────────────────────────

// ListImageOriginMappings returns a cursor-paginated slice of mappings.
func (s *Server) ListImageOriginMappings(
	ctx context.Context, req ListImageOriginMappingsRequestObject,
) (ListImageOriginMappingsResponseObject, error) {
	params := StoreListImageOriginMappingsParams{}
	if req.Params.Limit != nil {
		params.Limit = *req.Params.Limit
	}
	if req.Params.Cursor != nil {
		params.Cursor = *req.Params.Cursor
	}
	if req.Params.PublicRegistry != nil {
		params.PublicRegistry = *req.Params.PublicRegistry
	}
	if req.Params.Q != nil {
		params.Q = *req.Params.Q
	}
	items, next, err := s.store.ListImageOriginMappings(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	resp := ListImageOriginMappings200JSONResponse{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return resp, nil
}

// GetImageOriginMapping returns a single mapping by image_name.
func (s *Server) GetImageOriginMapping(
	ctx context.Context, req GetImageOriginMappingRequestObject,
) (GetImageOriginMappingResponseObject, error) {
	got, err := s.store.GetImageOriginMapping(ctx, req.ImageName)
	switch {
	case errors.Is(err, ErrNotFound):
		return GetImageOriginMapping404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
		}, nil
	case err != nil:
		return nil, fmt.Errorf("store: %w", err)
	}
	return GetImageOriginMapping200JSONResponse(got), nil
}

// CreateImageOriginMapping inserts a new (image_name, public_registry).
func (s *Server) CreateImageOriginMapping(
	ctx context.Context, req CreateImageOriginMappingRequestObject,
) (CreateImageOriginMappingResponseObject, error) {
	in := req.Body
	if err := validateImageName(in.ImageName); err != nil {
		return CreateImageOriginMapping400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Bad Request", err.Error())),
		}, nil
	}
	if err := validatePublicRegistry(in.PublicRegistry); err != nil {
		return CreateImageOriginMapping400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Bad Request", err.Error())),
		}, nil
	}
	if err := validateNotes(in.Notes); err != nil {
		return CreateImageOriginMapping400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Bad Request", err.Error())),
		}, nil
	}
	caller := callerIDFromContext(ctx)
	out, err := s.store.CreateImageOriginMapping(ctx,
		ImageOriginMappingCreate{
			ImageName:      in.ImageName,
			PublicRegistry: in.PublicRegistry,
			Notes:          in.Notes,
		}, caller)
	switch {
	case errors.Is(err, ErrConflict):
		return CreateImageOriginMapping409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse(problemConflict(errImageOriginMappingNameConflict)),
		}, nil
	case err != nil:
		return nil, fmt.Errorf("store: %w", err)
	}
	return CreateImageOriginMapping201JSONResponse(out), nil
}

// PatchImageOriginMapping applies a merge-patch.
func (s *Server) PatchImageOriginMapping(
	ctx context.Context, req PatchImageOriginMappingRequestObject,
) (PatchImageOriginMappingResponseObject, error) {
	p := req.Body
	if p.PublicRegistry != nil {
		if err := validatePublicRegistry(*p.PublicRegistry); err != nil {
			return PatchImageOriginMapping400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Bad Request", err.Error())),
			}, nil
		}
	}
	if err := validateNotes(p.Notes); err != nil {
		return PatchImageOriginMapping400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Bad Request", err.Error())),
		}, nil
	}
	caller := callerIDFromContext(ctx)
	out, err := s.store.PatchImageOriginMapping(ctx, req.ImageName,
		ImageOriginMappingPatch{PublicRegistry: p.PublicRegistry, Notes: p.Notes},
		caller)
	switch {
	case errors.Is(err, ErrNotFound):
		return PatchImageOriginMapping404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
		}, nil
	case err != nil:
		return nil, fmt.Errorf("store: %w", err)
	}
	return PatchImageOriginMapping200JSONResponse(out), nil
}

// DeleteImageOriginMapping removes a mapping by image_name.
func (s *Server) DeleteImageOriginMapping(
	ctx context.Context, req DeleteImageOriginMappingRequestObject,
) (DeleteImageOriginMappingResponseObject, error) {
	err := s.store.DeleteImageOriginMapping(ctx, req.ImageName)
	switch {
	case errors.Is(err, ErrNotFound):
		return DeleteImageOriginMapping404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
		}, nil
	case err != nil:
		return nil, fmt.Errorf("store: %w", err)
	}
	return DeleteImageOriginMapping204Response{}, nil
}

// enrichWorkloadsContainersVersions attaches the opt-in containers_versions
// enrichment (ADR-0022/0032) to each workload in place when include selects
// it. The field is only set when something was actually enriched, so an
// included-but-unenrichable workload omits containers_versions rather than
// serialising it as null (the schema is non-nullable).
func (s *Server) enrichWorkloadsContainersVersions(ctx context.Context, include *ListWorkloadsParamsInclude, items []Workload) {
	if include == nil || *include != IncludeContainersVersions {
		return
	}
	for i := range items {
		var containers []map[string]any
		if items[i].Containers != nil {
			containers = *items[i].Containers
		}
		if cv := EnrichContainersVersions(ctx, s.store, containers); cv != nil {
			m := map[string]ContainerVersionInfo(cv)
			items[i].ContainersVersions = &m
		}
	}
}

// clientIP returns the source IP for rate-limiting, audit logging and
// session tracking. Wraps httputil.ClientIP, applying the server-scoped
// trusted-proxy list. Returns the empty string when r.RemoteAddr is
// unparseable, which the audit row treats as "unknown".
func (s *Server) clientIP(r *http.Request) string {
	ip := httputil.ClientIP(r, s.trustedProxies)
	if ip == nil {
		return ""
	}
	return ip.String()
}
