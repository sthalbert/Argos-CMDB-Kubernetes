package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
)

// Server implements ServerInterface for the Argos REST API.
type Server struct {
	version string
	store   Store
}

// NewServer wires the handlers with a persistence backend and the build
// version reported on health probes.
func NewServer(version string, store Store) *Server {
	return &Server{version: version, store: store}
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

// ListPods returns a paged list of pods, optionally filtered by namespace_id.
func (s *Server) ListPods(w http.ResponseWriter, r *http.Request, params ListPodsParams) {
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}

	items, next, err := s.store.ListPods(r.Context(), params.NamespaceId, limit, cursor)
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
