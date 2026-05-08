package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// GetPodEnrichedResponse wraps the generated GetPod200JSONResponse and appends
// a containers_versions field to the JSON output without modifying the
// generated structs. It implements GetPodResponseObject.
type GetPodEnrichedResponse struct {
	Pod                Pod
	ContainersVersions ContainersVersions
}

// VisitGetPodResponse writes the wrapped Pod plus the containers_versions
// sibling field as JSON to w.
//
//nolint:gocritic // r mirrors the codegen *Response type signature (hugeParam expected)
func (r GetPodEnrichedResponse) VisitGetPodResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(struct {
		Pod
		ContainersVersions ContainersVersions `json:"containers_versions,omitempty"`
	}{
		Pod:                r.Pod,
		ContainersVersions: r.ContainersVersions,
	}); err != nil {
		return fmt.Errorf("encode pod response: %w", err)
	}
	return nil
}

// GetWorkloadEnrichedResponse wraps the generated GetWorkload200JSONResponse
// and appends a containers_versions field to the JSON output without modifying
// the generated structs. It implements GetWorkloadResponseObject.
type GetWorkloadEnrichedResponse struct {
	Workload           Workload
	ContainersVersions ContainersVersions
}

// VisitGetWorkloadResponse writes the wrapped Workload plus the
// containers_versions sibling field as JSON to w.
//
//nolint:gocritic // r mirrors the codegen *Response type signature (hugeParam expected)
func (r GetWorkloadEnrichedResponse) VisitGetWorkloadResponse(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(struct {
		Workload
		ContainersVersions ContainersVersions `json:"containers_versions,omitempty"`
	}{
		Workload:           r.Workload,
		ContainersVersions: r.ContainersVersions,
	}); err != nil {
		return fmt.Errorf("encode workload response: %w", err)
	}
	return nil
}
