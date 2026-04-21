package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/auth"
)

// Server implements ServerInterface for the Argos REST API.
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

var _ ServerInterface = (*Server)(nil)

// GetHealthz reports that the process is alive.
func (s *Server) GetHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Health{Status: Ok, Version: &s.version})
}

// GetReadyz reports whether the service can accept traffic by pinging the store.
func (s *Server) GetReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		slog.Error("readyz: store ping failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "Not Ready", "database not reachable")
		return
	}
	writeJSON(w, http.StatusOK, Health{Status: Ok, Version: &s.version})
}

// ListClusters returns a paged list of clusters.
func (s *Server) ListClusters(w http.ResponseWriter, r *http.Request, params ListClustersParams) {
	// Exact name filter: short-circuit to GetClusterByName and return a
	// single-item list (or empty). Used by the push collector to resolve
	// its cluster record without paginating.
	if params.Name != nil && *params.Name != "" {
		c, err := s.store.GetClusterByName(r.Context(), *params.Name)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeJSON(w, http.StatusOK, ClusterList{Items: []Cluster{}})
				return
			}
			s.writeStoreError(w, "listClusters", err)
			return
		}
		c = withClusterLayer(c)
		writeJSON(w, http.StatusOK, ClusterList{Items: []Cluster{c}})
		return
	}

	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	items, next, err := s.store.ListClusters(r.Context(), limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listClusters", err)
		return
	}

	for i := range items {
		items[i] = withClusterLayer(items[i])
	}
	resp := ClusterList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateCluster registers a new cluster.
func (s *Server) CreateCluster(w http.ResponseWriter, r *http.Request) {
	var body ClusterCreate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Name == "" {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'name' is required")
		return
	}

	c, err := s.store.CreateCluster(r.Context(), body)
	if err != nil {
		s.writeStoreError(w, "createCluster", err)
		return
	}
	c = withClusterLayer(c)

	if c.Id != nil {
		w.Header().Set("Location", "/v1/clusters/"+c.Id.String())
	}
	writeJSON(w, http.StatusCreated, c)
}

// GetCluster fetches a cluster by id.
func (s *Server) GetCluster(w http.ResponseWriter, r *http.Request, id ClusterId) {
	c, err := s.store.GetCluster(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "getCluster", err)
		return
	}
	writeJSON(w, http.StatusOK, withClusterLayer(c))
}

// UpdateCluster applies merge-patch updates to a cluster.
func (s *Server) UpdateCluster(w http.ResponseWriter, r *http.Request, id ClusterId) {
	var body ClusterUpdate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	c, err := s.store.UpdateCluster(r.Context(), id, body)
	if err != nil {
		s.writeStoreError(w, "updateCluster", err)
		return
	}
	writeJSON(w, http.StatusOK, withClusterLayer(c))
}

// DeleteCluster removes a cluster.
func (s *Server) DeleteCluster(w http.ResponseWriter, r *http.Request, id ClusterId) {
	if err := s.store.DeleteCluster(r.Context(), id); err != nil {
		s.writeStoreError(w, "deleteCluster", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListNodes returns a paged list of nodes, optionally filtered by cluster_id.
func (s *Server) ListNodes(w http.ResponseWriter, r *http.Request, params ListNodesParams) {
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	items, next, err := s.store.ListNodes(r.Context(), params.ClusterId, limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listNodes", err)
		return
	}

	for i := range items {
		items[i] = withNodeLayer(items[i])
	}
	resp := NodeList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateNode registers a new node under a cluster.
func (s *Server) CreateNode(w http.ResponseWriter, r *http.Request) {
	var body NodeCreate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Name == "" {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'name' is required")
		return
	}
	if body.ClusterId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'cluster_id' is required")
		return
	}

	n, err := s.store.CreateNode(r.Context(), body)
	if err != nil {
		s.writeStoreError(w, "createNode", err)
		return
	}
	n = withNodeLayer(n)

	if n.Id != nil {
		w.Header().Set("Location", "/v1/nodes/"+n.Id.String())
	}
	writeJSON(w, http.StatusCreated, n)
}

// GetNode fetches a node by id.
func (s *Server) GetNode(w http.ResponseWriter, r *http.Request, id NodeId) {
	n, err := s.store.GetNode(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "getNode", err)
		return
	}
	writeJSON(w, http.StatusOK, withNodeLayer(n))
}

// UpdateNode applies merge-patch updates to a node.
func (s *Server) UpdateNode(w http.ResponseWriter, r *http.Request, id NodeId) {
	var body NodeUpdate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	n, err := s.store.UpdateNode(r.Context(), id, body)
	if err != nil {
		s.writeStoreError(w, "updateNode", err)
		return
	}
	writeJSON(w, http.StatusOK, withNodeLayer(n))
}

// DeleteNode removes a node.
func (s *Server) DeleteNode(w http.ResponseWriter, r *http.Request, id NodeId) {
	if err := s.store.DeleteNode(r.Context(), id); err != nil {
		s.writeStoreError(w, "deleteNode", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListNamespaces returns a paged list of namespaces, optionally filtered by cluster_id.
func (s *Server) ListNamespaces(w http.ResponseWriter, r *http.Request, params ListNamespacesParams) {
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	items, next, err := s.store.ListNamespaces(r.Context(), params.ClusterId, limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listNamespaces", err)
		return
	}

	for i := range items {
		items[i] = withNamespaceLayer(items[i])
	}
	resp := NamespaceList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateNamespace registers a new namespace under a cluster.
func (s *Server) CreateNamespace(w http.ResponseWriter, r *http.Request) {
	var body NamespaceCreate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Name == "" {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'name' is required")
		return
	}
	if body.ClusterId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'cluster_id' is required")
		return
	}

	n, err := s.store.CreateNamespace(r.Context(), body)
	if err != nil {
		s.writeStoreError(w, "createNamespace", err)
		return
	}
	n = withNamespaceLayer(n)

	if n.Id != nil {
		w.Header().Set("Location", "/v1/namespaces/"+n.Id.String())
	}
	writeJSON(w, http.StatusCreated, n)
}

// GetNamespace fetches a namespace by id.
func (s *Server) GetNamespace(w http.ResponseWriter, r *http.Request, id NamespaceId) {
	n, err := s.store.GetNamespace(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "getNamespace", err)
		return
	}
	writeJSON(w, http.StatusOK, withNamespaceLayer(n))
}

// UpdateNamespace applies merge-patch updates.
func (s *Server) UpdateNamespace(w http.ResponseWriter, r *http.Request, id NamespaceId) {
	var body NamespaceUpdate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	n, err := s.store.UpdateNamespace(r.Context(), id, body)
	if err != nil {
		s.writeStoreError(w, "updateNamespace", err)
		return
	}
	writeJSON(w, http.StatusOK, withNamespaceLayer(n))
}

// DeleteNamespace removes a namespace.
func (s *Server) DeleteNamespace(w http.ResponseWriter, r *http.Request, id NamespaceId) {
	if err := s.store.DeleteNamespace(r.Context(), id); err != nil {
		s.writeStoreError(w, "deleteNamespace", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListPods returns a paged list of pods, optionally filtered by namespace_id,
// node_name, and/or container image substring.
func (s *Server) ListPods(w http.ResponseWriter, r *http.Request, params ListPodsParams) {
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}
	filter := PodListFilter{
		NamespaceID:    params.NamespaceId,
		NodeName:       params.NodeName,
		ImageSubstring: params.Image,
	}

	items, next, err := s.store.ListPods(r.Context(), filter, limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listPods", err)
		return
	}

	for i := range items {
		items[i] = withPodLayer(items[i])
	}
	resp := PodList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreatePod registers a new pod under a namespace.
func (s *Server) CreatePod(w http.ResponseWriter, r *http.Request) {
	var body PodCreate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Name == "" {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'name' is required")
		return
	}
	if body.NamespaceId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'namespace_id' is required")
		return
	}

	p, err := s.store.CreatePod(r.Context(), body)
	if err != nil {
		s.writeStoreError(w, "createPod", err)
		return
	}
	p = withPodLayer(p)

	if p.Id != nil {
		w.Header().Set("Location", "/v1/pods/"+p.Id.String())
	}
	writeJSON(w, http.StatusCreated, p)
}

// GetPod fetches a pod by id.
func (s *Server) GetPod(w http.ResponseWriter, r *http.Request, id PodId) {
	p, err := s.store.GetPod(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "getPod", err)
		return
	}
	writeJSON(w, http.StatusOK, withPodLayer(p))
}

// UpdatePod applies merge-patch updates.
func (s *Server) UpdatePod(w http.ResponseWriter, r *http.Request, id PodId) {
	var body PodUpdate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	p, err := s.store.UpdatePod(r.Context(), id, body)
	if err != nil {
		s.writeStoreError(w, "updatePod", err)
		return
	}
	writeJSON(w, http.StatusOK, withPodLayer(p))
}

// DeletePod removes a pod.
func (s *Server) DeletePod(w http.ResponseWriter, r *http.Request, id PodId) {
	if err := s.store.DeletePod(r.Context(), id); err != nil {
		s.writeStoreError(w, "deletePod", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListWorkloads returns a paged list of workloads, optionally filtered by
// namespace_id and/or kind.
func (s *Server) ListWorkloads(w http.ResponseWriter, r *http.Request, params ListWorkloadsParams) {
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}
	if params.Kind != nil && !params.Kind.Valid() {
		writeProblem(w, http.StatusBadRequest, "Invalid filter", "query 'kind' is not a known workload kind")
		return
	}
	filter := WorkloadListFilter{
		NamespaceID:    params.NamespaceId,
		Kind:           params.Kind,
		ImageSubstring: params.Image,
	}

	items, next, err := s.store.ListWorkloads(r.Context(), filter, limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listWorkloads", err)
		return
	}

	for i := range items {
		items[i] = withWorkloadLayer(items[i])
	}
	resp := WorkloadList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateWorkload registers a new workload under a namespace.
func (s *Server) CreateWorkload(w http.ResponseWriter, r *http.Request) {
	var body WorkloadCreate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Name == "" {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'name' is required")
		return
	}
	if body.NamespaceId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'namespace_id' is required")
		return
	}
	if !body.Kind.Valid() {
		writeProblem(w, http.StatusBadRequest, "Invalid field", "field 'kind' must be one of Deployment, StatefulSet, DaemonSet")
		return
	}

	wl, err := s.store.CreateWorkload(r.Context(), body)
	if err != nil {
		s.writeStoreError(w, "createWorkload", err)
		return
	}
	wl = withWorkloadLayer(wl)

	if wl.Id != nil {
		w.Header().Set("Location", "/v1/workloads/"+wl.Id.String())
	}
	writeJSON(w, http.StatusCreated, wl)
}

// GetWorkload fetches a workload by id.
func (s *Server) GetWorkload(w http.ResponseWriter, r *http.Request, id WorkloadId) {
	wl, err := s.store.GetWorkload(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "getWorkload", err)
		return
	}
	writeJSON(w, http.StatusOK, withWorkloadLayer(wl))
}

// UpdateWorkload applies merge-patch updates.
func (s *Server) UpdateWorkload(w http.ResponseWriter, r *http.Request, id WorkloadId) {
	var body WorkloadUpdate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	wl, err := s.store.UpdateWorkload(r.Context(), id, body)
	if err != nil {
		s.writeStoreError(w, "updateWorkload", err)
		return
	}
	writeJSON(w, http.StatusOK, withWorkloadLayer(wl))
}

// DeleteWorkload removes a workload.
func (s *Server) DeleteWorkload(w http.ResponseWriter, r *http.Request, id WorkloadId) {
	if err := s.store.DeleteWorkload(r.Context(), id); err != nil {
		s.writeStoreError(w, "deleteWorkload", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListServices returns a paged list of services, optionally filtered by namespace_id.
func (s *Server) ListServices(w http.ResponseWriter, r *http.Request, params ListServicesParams) {
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	items, next, err := s.store.ListServices(r.Context(), params.NamespaceId, limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listServices", err)
		return
	}

	for i := range items {
		items[i] = withServiceLayer(items[i])
	}
	resp := ServiceList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateService registers a new service under a namespace.
func (s *Server) CreateService(w http.ResponseWriter, r *http.Request) {
	var body ServiceCreate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Name == "" {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'name' is required")
		return
	}
	if body.NamespaceId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'namespace_id' is required")
		return
	}
	if body.Type != nil && !isValidServiceType(*body.Type) {
		writeProblem(w, http.StatusBadRequest, "Invalid field", "field 'type' must be one of ClusterIP, NodePort, LoadBalancer, ExternalName")
		return
	}

	svc, err := s.store.CreateService(r.Context(), body)
	if err != nil {
		s.writeStoreError(w, "createService", err)
		return
	}
	svc = withServiceLayer(svc)

	if svc.Id != nil {
		w.Header().Set("Location", "/v1/services/"+svc.Id.String())
	}
	writeJSON(w, http.StatusCreated, svc)
}

// GetService fetches a service by id.
func (s *Server) GetService(w http.ResponseWriter, r *http.Request, id ServiceId) {
	svc, err := s.store.GetService(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "getService", err)
		return
	}
	writeJSON(w, http.StatusOK, withServiceLayer(svc))
}

// UpdateService applies merge-patch updates.
func (s *Server) UpdateService(w http.ResponseWriter, r *http.Request, id ServiceId) {
	var body ServiceUpdate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Type != nil && !isValidServiceType(*body.Type) {
		writeProblem(w, http.StatusBadRequest, "Invalid field", "field 'type' must be one of ClusterIP, NodePort, LoadBalancer, ExternalName")
		return
	}
	svc, err := s.store.UpdateService(r.Context(), id, body)
	if err != nil {
		s.writeStoreError(w, "updateService", err)
		return
	}
	writeJSON(w, http.StatusOK, withServiceLayer(svc))
}

// DeleteService removes a service.
func (s *Server) DeleteService(w http.ResponseWriter, r *http.Request, id ServiceId) {
	if err := s.store.DeleteService(r.Context(), id); err != nil {
		s.writeStoreError(w, "deleteService", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListIngresses returns a paged list of ingresses, optionally filtered by namespace_id.
func (s *Server) ListIngresses(w http.ResponseWriter, r *http.Request, params ListIngressesParams) {
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	items, next, err := s.store.ListIngresses(r.Context(), params.NamespaceId, limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listIngresses", err)
		return
	}

	for i := range items {
		items[i] = withIngressLayer(items[i])
	}
	resp := IngressList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreateIngress registers a new ingress under a namespace.
func (s *Server) CreateIngress(w http.ResponseWriter, r *http.Request) {
	var body IngressCreate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Name == "" {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'name' is required")
		return
	}
	if body.NamespaceId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'namespace_id' is required")
		return
	}

	ing, err := s.store.CreateIngress(r.Context(), body)
	if err != nil {
		s.writeStoreError(w, "createIngress", err)
		return
	}
	ing = withIngressLayer(ing)

	if ing.Id != nil {
		w.Header().Set("Location", "/v1/ingresses/"+ing.Id.String())
	}
	writeJSON(w, http.StatusCreated, ing)
}

// GetIngress fetches an ingress by id.
func (s *Server) GetIngress(w http.ResponseWriter, r *http.Request, id IngressId) {
	ing, err := s.store.GetIngress(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "getIngress", err)
		return
	}
	writeJSON(w, http.StatusOK, withIngressLayer(ing))
}

// UpdateIngress applies merge-patch updates.
func (s *Server) UpdateIngress(w http.ResponseWriter, r *http.Request, id IngressId) {
	var body IngressUpdate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	ing, err := s.store.UpdateIngress(r.Context(), id, body)
	if err != nil {
		s.writeStoreError(w, "updateIngress", err)
		return
	}
	writeJSON(w, http.StatusOK, withIngressLayer(ing))
}

// DeleteIngress removes an ingress.
func (s *Server) DeleteIngress(w http.ResponseWriter, r *http.Request, id IngressId) {
	if err := s.store.DeleteIngress(r.Context(), id); err != nil {
		s.writeStoreError(w, "deleteIngress", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func isValidServiceType(t ServiceType) bool {
	switch t {
	case ClusterIP, NodePort, LoadBalancer, ExternalName:
		return true
	}
	return false
}

// ListPersistentVolumes returns a paged list of PVs.
func (s *Server) ListPersistentVolumes(w http.ResponseWriter, r *http.Request, params ListPersistentVolumesParams) {
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	items, next, err := s.store.ListPersistentVolumes(r.Context(), params.ClusterId, limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listPersistentVolumes", err)
		return
	}

	for i := range items {
		items[i] = withPersistentVolumeLayer(items[i])
	}
	resp := PersistentVolumeList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreatePersistentVolume registers a new PV under a cluster.
func (s *Server) CreatePersistentVolume(w http.ResponseWriter, r *http.Request) {
	var body PersistentVolumeCreate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Name == "" {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'name' is required")
		return
	}
	if body.ClusterId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'cluster_id' is required")
		return
	}

	pv, err := s.store.CreatePersistentVolume(r.Context(), body)
	if err != nil {
		s.writeStoreError(w, "createPersistentVolume", err)
		return
	}
	pv = withPersistentVolumeLayer(pv)

	if pv.Id != nil {
		w.Header().Set("Location", "/v1/persistentvolumes/"+pv.Id.String())
	}
	writeJSON(w, http.StatusCreated, pv)
}

// GetPersistentVolume fetches a PV by id.
func (s *Server) GetPersistentVolume(w http.ResponseWriter, r *http.Request, id PersistentVolumeId) {
	pv, err := s.store.GetPersistentVolume(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "getPersistentVolume", err)
		return
	}
	writeJSON(w, http.StatusOK, withPersistentVolumeLayer(pv))
}

// UpdatePersistentVolume applies merge-patch updates.
func (s *Server) UpdatePersistentVolume(w http.ResponseWriter, r *http.Request, id PersistentVolumeId) {
	var body PersistentVolumeUpdate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	pv, err := s.store.UpdatePersistentVolume(r.Context(), id, body)
	if err != nil {
		s.writeStoreError(w, "updatePersistentVolume", err)
		return
	}
	writeJSON(w, http.StatusOK, withPersistentVolumeLayer(pv))
}

// DeletePersistentVolume removes a PV.
func (s *Server) DeletePersistentVolume(w http.ResponseWriter, r *http.Request, id PersistentVolumeId) {
	if err := s.store.DeletePersistentVolume(r.Context(), id); err != nil {
		s.writeStoreError(w, "deletePersistentVolume", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListPersistentVolumeClaims returns a paged list of PVCs.
func (s *Server) ListPersistentVolumeClaims(w http.ResponseWriter, r *http.Request, params ListPersistentVolumeClaimsParams) {
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	items, next, err := s.store.ListPersistentVolumeClaims(r.Context(), params.NamespaceId, limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listPersistentVolumeClaims", err)
		return
	}

	for i := range items {
		items[i] = withPersistentVolumeClaimLayer(items[i])
	}
	resp := PersistentVolumeClaimList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

// CreatePersistentVolumeClaim registers a new PVC under a namespace.
func (s *Server) CreatePersistentVolumeClaim(w http.ResponseWriter, r *http.Request) {
	var body PersistentVolumeClaimCreate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Name == "" {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'name' is required")
		return
	}
	if body.NamespaceId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'namespace_id' is required")
		return
	}

	pvc, err := s.store.CreatePersistentVolumeClaim(r.Context(), body)
	if err != nil {
		s.writeStoreError(w, "createPersistentVolumeClaim", err)
		return
	}
	pvc = withPersistentVolumeClaimLayer(pvc)

	if pvc.Id != nil {
		w.Header().Set("Location", "/v1/persistentvolumeclaims/"+pvc.Id.String())
	}
	writeJSON(w, http.StatusCreated, pvc)
}

// GetPersistentVolumeClaim fetches a PVC by id.
func (s *Server) GetPersistentVolumeClaim(w http.ResponseWriter, r *http.Request, id PersistentVolumeClaimId) {
	pvc, err := s.store.GetPersistentVolumeClaim(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "getPersistentVolumeClaim", err)
		return
	}
	writeJSON(w, http.StatusOK, withPersistentVolumeClaimLayer(pvc))
}

// UpdatePersistentVolumeClaim applies merge-patch updates.
func (s *Server) UpdatePersistentVolumeClaim(w http.ResponseWriter, r *http.Request, id PersistentVolumeClaimId) {
	var body PersistentVolumeClaimUpdate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	pvc, err := s.store.UpdatePersistentVolumeClaim(r.Context(), id, body)
	if err != nil {
		s.writeStoreError(w, "updatePersistentVolumeClaim", err)
		return
	}
	writeJSON(w, http.StatusOK, withPersistentVolumeClaimLayer(pvc))
}

// DeletePersistentVolumeClaim removes a PVC.
func (s *Server) DeletePersistentVolumeClaim(w http.ResponseWriter, r *http.Request, id PersistentVolumeClaimId) {
	if err := s.store.DeletePersistentVolumeClaim(r.Context(), id); err != nil {
		s.writeStoreError(w, "deletePersistentVolumeClaim", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeStoreError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not Found", "")
	case errors.Is(err, ErrConflict):
		writeProblem(w, http.StatusConflict, "Conflict", err.Error())
	default:
		slog.Error("handler store error", "op", op, "error", err)
		writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
	}
}

// ── Reconcile handlers (ADR-0009: push collector) ────────────────────

// ReconcileNodes deletes every node of the given cluster whose name is
// not in keep_names.
func (s *Server) ReconcileNodes(w http.ResponseWriter, r *http.Request) {
	var body ReconcileClusterScoped
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.ClusterId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'cluster_id' is required")
		return
	}
	n, err := s.store.DeleteNodesNotIn(r.Context(), body.ClusterId, body.KeepNames)
	if err != nil {
		s.writeStoreError(w, "reconcileNodes", err)
		return
	}
	writeJSON(w, http.StatusOK, ReconcileResult{Deleted: n})
}

// ReconcileNamespaces deletes every namespace of the given cluster whose
// name is not in keep_names.
func (s *Server) ReconcileNamespaces(w http.ResponseWriter, r *http.Request) {
	var body ReconcileClusterScoped
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.ClusterId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'cluster_id' is required")
		return
	}
	n, err := s.store.DeleteNamespacesNotIn(r.Context(), body.ClusterId, body.KeepNames)
	if err != nil {
		s.writeStoreError(w, "reconcileNamespaces", err)
		return
	}
	writeJSON(w, http.StatusOK, ReconcileResult{Deleted: n})
}

// ReconcilePersistentVolumes deletes every PV of the given cluster whose
// name is not in keep_names.
func (s *Server) ReconcilePersistentVolumes(w http.ResponseWriter, r *http.Request) {
	var body ReconcileClusterScoped
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.ClusterId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'cluster_id' is required")
		return
	}
	n, err := s.store.DeletePersistentVolumesNotIn(r.Context(), body.ClusterId, body.KeepNames)
	if err != nil {
		s.writeStoreError(w, "reconcilePersistentVolumes", err)
		return
	}
	writeJSON(w, http.StatusOK, ReconcileResult{Deleted: n})
}

// ReconcilePods deletes every pod of the given namespace whose name is
// not in keep_names.
func (s *Server) ReconcilePods(w http.ResponseWriter, r *http.Request) {
	var body ReconcileNamespaceScoped
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.NamespaceId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'namespace_id' is required")
		return
	}
	n, err := s.store.DeletePodsNotIn(r.Context(), body.NamespaceId, body.KeepNames)
	if err != nil {
		s.writeStoreError(w, "reconcilePods", err)
		return
	}
	writeJSON(w, http.StatusOK, ReconcileResult{Deleted: n})
}

// ReconcileWorkloads deletes every workload of the given namespace whose
// (kind, name) tuple is not in the parallel keep_kinds/keep_names arrays.
func (s *Server) ReconcileWorkloads(w http.ResponseWriter, r *http.Request) {
	var body ReconcileWorkloads
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.NamespaceId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'namespace_id' is required")
		return
	}
	if len(body.KeepKinds) != len(body.KeepNames) {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", "keep_kinds and keep_names must have equal length")
		return
	}
	n, err := s.store.DeleteWorkloadsNotIn(r.Context(), body.NamespaceId, body.KeepKinds, body.KeepNames)
	if err != nil {
		s.writeStoreError(w, "reconcileWorkloads", err)
		return
	}
	writeJSON(w, http.StatusOK, ReconcileResult{Deleted: n})
}

// ReconcileServices deletes every service of the given namespace whose
// name is not in keep_names.
func (s *Server) ReconcileServices(w http.ResponseWriter, r *http.Request) {
	var body ReconcileNamespaceScoped
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.NamespaceId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'namespace_id' is required")
		return
	}
	n, err := s.store.DeleteServicesNotIn(r.Context(), body.NamespaceId, body.KeepNames)
	if err != nil {
		s.writeStoreError(w, "reconcileServices", err)
		return
	}
	writeJSON(w, http.StatusOK, ReconcileResult{Deleted: n})
}

// ReconcileIngresses deletes every ingress of the given namespace whose
// name is not in keep_names.
func (s *Server) ReconcileIngresses(w http.ResponseWriter, r *http.Request) {
	var body ReconcileNamespaceScoped
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.NamespaceId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'namespace_id' is required")
		return
	}
	n, err := s.store.DeleteIngressesNotIn(r.Context(), body.NamespaceId, body.KeepNames)
	if err != nil {
		s.writeStoreError(w, "reconcileIngresses", err)
		return
	}
	writeJSON(w, http.StatusOK, ReconcileResult{Deleted: n})
}

// ReconcilePersistentVolumeClaims deletes every PVC of the given namespace
// whose name is not in keep_names.
func (s *Server) ReconcilePersistentVolumeClaims(w http.ResponseWriter, r *http.Request) {
	var body ReconcileNamespaceScoped
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.NamespaceId == (uuid.UUID{}) {
		writeProblem(w, http.StatusBadRequest, "Missing field", "field 'namespace_id' is required")
		return
	}
	n, err := s.store.DeletePersistentVolumeClaimsNotIn(r.Context(), body.NamespaceId, body.KeepNames)
	if err != nil {
		s.writeStoreError(w, "reconcilePersistentVolumeClaims", err)
		return
	}
	writeJSON(w, http.StatusOK, ReconcileResult{Deleted: n})
}

func decodeJSONBody(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is empty")
		}
		return err
	}
	return nil
}
