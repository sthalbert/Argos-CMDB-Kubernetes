package api

import (
	"strings"

	"golang.org/x/mod/semver"

	"github.com/sthalbert/longue-vue/internal/eolagg"
)

// workloadToEolaggInput adapts a Workload (with its containers_versions
// enrichment already populated) into the eolagg.WorkloadInput shape used by
// the global EOL dashboard (ADR-0032). Only enriched containers (a non-nil
// eol_status) contribute an image; the deployed tag is parsed here to derive
// the repo and the major.minor cycle.
func workloadToEolaggInput(w *Workload) eolagg.WorkloadInput {
	in := eolagg.WorkloadInput{Name: w.Name}
	if w.Id != nil {
		in.ID = w.Id.String()
	}
	if w.ClusterName != nil {
		in.Cluster = *w.ClusterName
	}
	if w.Containers == nil || w.ContainersVersions == nil {
		return in
	}
	in.Images = enrichedWorkloadImages(*w.Containers, *w.ContainersVersions)
	return in
}

// enrichedWorkloadImages turns a workload's containers + their enrichment into
// the flat eolagg.WorkloadImage slice. Only containers with a populated
// eol_status and a parseable image reference contribute a row.
func enrichedWorkloadImages(containers ContainerList, versions map[string]ContainerVersionInfo) []eolagg.WorkloadImage {
	var images []eolagg.WorkloadImage
	for _, c := range containers {
		name, _ := c["name"].(string)
		img, _ := c["image"].(string)
		if name == "" || img == "" {
			continue
		}
		cv, ok := versions[name]
		if !ok || cv.EolStatus == nil {
			continue
		}
		if image, ok := containerToWorkloadImage(img, cv); ok {
			images = append(images, image)
		}
	}
	return images
}

// containerToWorkloadImage parses a deployed image string + its enriched
// container-version info into an eolagg.WorkloadImage. Returns ok=false when
// the image reference or tag is not parseable.
func containerToWorkloadImage(img string, cv ContainerVersionInfo) (eolagg.WorkloadImage, bool) {
	ref, err := parseContainerRef(img)
	if err != nil {
		return eolagg.WorkloadImage{}, false
	}
	cur, err := parseContainerTag(ref.tag)
	if err != nil {
		return eolagg.WorkloadImage{}, false
	}
	image := eolagg.WorkloadImage{
		Repo:      ref.imageRepo,
		Cycle:     strings.TrimPrefix(semver.MajorMinor(cur.version.raw), "v"),
		EOLStatus: string(*cv.EolStatus),
	}
	if cv.LatestTag != nil {
		image.LatestTag = *cv.LatestTag
	}
	if cv.LastCheckedAt != nil {
		image.CheckedAt = cv.LastCheckedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return image, true
}
