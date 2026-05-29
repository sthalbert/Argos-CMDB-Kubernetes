package api

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// tagNginxLatest is the shared "latest nginx tag" fixture literal (goconst).
const tagNginxLatest = "1.27.4"

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return id
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return ts
}

func TestWorkloadToEolaggInput(t *testing.T) {
	cluster := "prod-eu"
	id := mustUUID(t, "11111111-1111-1111-1111-111111111111")
	latest := tagNginxLatest
	status := ContainerVersionInfoEolStatus("eol")
	checked := mustTime(t, "2026-05-15T00:00:00Z")

	w := Workload{
		Id:          &id,
		Name:        "api",
		ClusterName: &cluster,
		Containers: &ContainerList{
			{"name": "web", "image": "nginx:1.25.3"},
			{"name": "side", "image": "busybox:latest"}, // not enriched -> skipped
		},
		ContainersVersions: &map[string]ContainerVersionInfo{
			"web": {LatestTag: &latest, EolStatus: &status, LastCheckedAt: &checked},
		},
	}

	in := workloadToEolaggInput(&w)
	if in.Name != "api" || in.Cluster != cluster {
		t.Fatalf("entity fields: %+v", in)
	}
	if len(in.Images) != 1 {
		t.Fatalf("want 1 enriched image, got %d", len(in.Images))
	}
	img := in.Images[0]
	if img.Repo != "docker.io/library/nginx" || img.Cycle != "1.25" || img.LatestTag != "1.27.4" || img.EOLStatus != "eol" {
		t.Errorf("image = %+v", img)
	}
}

func TestWorkloadToEolaggInput_NoEnrichedContainers(t *testing.T) {
	id := mustUUID(t, "22222222-2222-2222-2222-222222222222")
	w := Workload{
		Id:   &id,
		Name: "api",
		Containers: &ContainerList{
			{"name": "web", "image": "nginx:1.25.3"},
		},
		// ContainersVersions nil / empty -> nothing enriched
	}
	in := workloadToEolaggInput(&w)
	if len(in.Images) != 0 {
		t.Fatalf("want 0 images for unenriched workload, got %d", len(in.Images))
	}
}

func TestWorkloadToEolaggInput_NilLatestTag(t *testing.T) {
	id := mustUUID(t, "33333333-3333-3333-3333-333333333333")
	status := ContainerVersionInfoEolStatus("approaching_eol")
	w := Workload{
		Id:   &id,
		Name: "api",
		Containers: &ContainerList{
			{"name": "web", "image": "nginx:1.25.3"},
		},
		ContainersVersions: &map[string]ContainerVersionInfo{
			"web": {EolStatus: &status}, // LatestTag nil
		},
	}
	in := workloadToEolaggInput(&w)
	if len(in.Images) != 1 {
		t.Fatalf("want 1 image, got %d", len(in.Images))
	}
	if in.Images[0].LatestTag != "" {
		t.Errorf("want empty LatestTag, got %q", in.Images[0].LatestTag)
	}
	if in.Images[0].EOLStatus != "approaching_eol" {
		t.Errorf("want approaching_eol, got %q", in.Images[0].EOLStatus)
	}
}
