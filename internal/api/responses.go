package api

import (
	"encoding/json"
	"net/http"
)

// GetPodEnrichedResponse wraps the generated GetPod200JSONResponse and appends
// a containers_versions field to the JSON output without modifying the
// generated structs. It implements GetPodResponseObject.
type GetPodEnrichedResponse struct {
	Pod                Pod
	ContainersVersions ContainersVersions
}

func (r GetPodEnrichedResponse) VisitGetPodResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	return json.NewEncoder(w).Encode(struct {
		Pod
		ContainersVersions ContainersVersions `json:"containers_versions,omitempty"`
	}{
		Pod:                r.Pod,
		ContainersVersions: r.ContainersVersions,
	})
}

// GetWorkloadEnrichedResponse wraps the generated GetWorkload200JSONResponse
// and appends a containers_versions field to the JSON output without modifying
// the generated structs. It implements GetWorkloadResponseObject.
type GetWorkloadEnrichedResponse struct {
	Workload           Workload
	ContainersVersions ContainersVersions
}

func (r GetWorkloadEnrichedResponse) VisitGetWorkloadResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	return json.NewEncoder(w).Encode(struct {
		Workload
		ContainersVersions ContainersVersions `json:"containers_versions,omitempty"`
	}{
		Workload:           r.Workload,
		ContainersVersions: r.ContainersVersions,
	})
}
