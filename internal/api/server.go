package api

import (
	"net/http"
)

// Server implements ServerInterface for the Argos REST API.
// It will be progressively extended as subsystems (store, collector, auth) land.
type Server struct {
	version string
}

// NewServer returns a Server wired with the build version reported on health probes.
func NewServer(version string) *Server {
	return &Server{version: version}
}

var _ ServerInterface = (*Server)(nil)

// GetHealthz reports that the process is alive.
func (s *Server) GetHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Health{Status: Ok, Version: &s.version})
}

// GetReadyz reports that the service can accept traffic.
// v1 has no downstream dependencies; this will check PostgreSQL once the store lands.
func (s *Server) GetReadyz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, Health{Status: Ok, Version: &s.version})
}

// ListClusters is stubbed until the persistence layer lands.
func (s *Server) ListClusters(w http.ResponseWriter, _ *http.Request, _ ListClustersParams) {
	writeNotImplemented(w, "listClusters")
}

// CreateCluster is stubbed until the persistence layer lands.
func (s *Server) CreateCluster(w http.ResponseWriter, _ *http.Request) {
	writeNotImplemented(w, "createCluster")
}

// GetCluster is stubbed until the persistence layer lands.
func (s *Server) GetCluster(w http.ResponseWriter, _ *http.Request, _ ClusterId) {
	writeNotImplemented(w, "getCluster")
}

// UpdateCluster is stubbed until the persistence layer lands.
func (s *Server) UpdateCluster(w http.ResponseWriter, _ *http.Request, _ ClusterId) {
	writeNotImplemented(w, "updateCluster")
}

// DeleteCluster is stubbed until the persistence layer lands.
func (s *Server) DeleteCluster(w http.ResponseWriter, _ *http.Request, _ ClusterId) {
	writeNotImplemented(w, "deleteCluster")
}

func writeNotImplemented(w http.ResponseWriter, op string) {
	writeProblem(w, http.StatusNotImplemented, "Not Implemented", op+": cluster persistence is not yet wired")
}
