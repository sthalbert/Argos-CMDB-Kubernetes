package api

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

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
	latest := "1.27.4"
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

	in := workloadToEolaggInput(w)
	if in.Name != "api" || in.Cluster != "prod-eu" {
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
