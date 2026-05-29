package eolagg

import "sort"

// WorkloadInput is a workload's already-enriched image set, adapted by the
// api layer from a Workload's containers + containers_versions. The eolagg
// package stays free of an internal/api dependency, so the api layer does
// the image parsing and passes flat per-image rows here (ADR-0032).
type WorkloadInput struct {
	ID      string
	Name    string
	Cluster string
	Images  []WorkloadImage
}

// WorkloadImage is one enriched container image of a workload. EOLStatus is
// the precomputed traffic-light status (supported | approaching_eol | eol).
// Cycle is the deployed major.minor (e.g. "1.25").
type WorkloadImage struct {
	Repo      string
	Cycle     string
	LatestTag string
	EOLStatus string
	CheckedAt string
}

// FlattenWorkloads emits one Row per distinct image repo across the given
// workloads. When a workload runs the same repo in two containers, the
// worst (highest-severity) status wins. Output is sorted by product, then
// entity name — mirroring Flatten's stable ordering for the dashboard.
func FlattenWorkloads(workloads []WorkloadInput) []Row {
	out := make([]Row, 0)
	for _, w := range workloads {
		byRepo := make(map[string]Row, len(w.Images))
		for _, img := range w.Images {
			if img.Repo == "" || img.EOLStatus == "" {
				continue
			}
			cand := Row{
				EntityType:      "workload",
				EntityID:        w.ID,
				EntityName:      w.Name,
				Cluster:         w.Cluster,
				Product:         img.Repo,
				Cycle:           img.Cycle,
				Status:          img.EOLStatus,
				EOLDate:         "",
				Latest:          img.LatestTag,
				LatestAvailable: img.LatestTag,
				Support:         "",
				CheckedAt:       img.CheckedAt,
			}
			if prev, ok := byRepo[img.Repo]; !ok || statusRank(cand.Status) > statusRank(prev.Status) {
				byRepo[img.Repo] = cand
			}
		}
		for repo := range byRepo {
			out = append(out, byRepo[repo])
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Product != out[j].Product {
			return out[i].Product < out[j].Product
		}
		return out[i].EntityName < out[j].EntityName
	})
	return out
}
