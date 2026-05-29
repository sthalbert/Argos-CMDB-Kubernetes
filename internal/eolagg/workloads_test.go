package eolagg

import "testing"

func TestFlattenWorkloads_Basic(t *testing.T) {
	in := []WorkloadInput{{
		ID: "w1", Name: "api", Cluster: "prod-eu",
		Images: []WorkloadImage{
			{Repo: "docker.io/library/nginx", Cycle: "1.25", LatestTag: "1.27.4", EOLStatus: "eol", CheckedAt: "2026-05-15T00:00:00Z"},
			{Repo: "docker.io/library/redis", Cycle: "7.2", LatestTag: "7.2.5", EOLStatus: "supported", CheckedAt: "2026-05-15T00:00:00Z"},
		},
	}}
	rows := FlattenWorkloads(in)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	// sorted by product: nginx before redis
	if rows[0].Product != "docker.io/library/nginx" || rows[0].Status != "eol" {
		t.Errorf("row0 = %+v", rows[0])
	}
	if rows[0].EntityType != "workload" || rows[0].EntityName != "api" || rows[0].Cluster != "prod-eu" {
		t.Errorf("row0 entity fields = %+v", rows[0])
	}
	if rows[0].Cycle != "1.25" || rows[0].Latest != "1.27.4" || rows[0].LatestAvailable != "1.27.4" {
		t.Errorf("row0 version fields = %+v", rows[0])
	}
	if rows[0].EOLDate != "" {
		t.Errorf("row0 EOLDate should be empty, got %q", rows[0].EOLDate)
	}
}

func TestFlattenWorkloads_DedupWorstTier(t *testing.T) {
	in := []WorkloadInput{{
		ID: "w1", Name: "api", Cluster: "prod-eu",
		Images: []WorkloadImage{
			{Repo: "docker.io/library/nginx", Cycle: "1.26", LatestTag: "1.27.4", EOLStatus: "approaching_eol", CheckedAt: "t"},
			{Repo: "docker.io/library/nginx", Cycle: "1.25", LatestTag: "1.27.4", EOLStatus: "eol", CheckedAt: "t"},
		},
	}}
	rows := FlattenWorkloads(in)
	if len(rows) != 1 {
		t.Fatalf("want 1 deduped row, got %d", len(rows))
	}
	if rows[0].Status != "eol" {
		t.Errorf("want worst tier eol, got %q", rows[0].Status)
	}
}

func TestFlattenWorkloads_Empty(t *testing.T) {
	if rows := FlattenWorkloads(nil); len(rows) != 0 {
		t.Fatalf("want 0 rows, got %d", len(rows))
	}
}
