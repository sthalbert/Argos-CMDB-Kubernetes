package eolagg

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type fixture struct {
	Clusters []struct {
		ID          string            `json:"id"`
		Name        string            `json:"name"`
		DisplayName string            `json:"display_name"`
		Annotations map[string]string `json:"annotations"`
	} `json:"clusters"`
	Nodes []struct {
		ID          string            `json:"id"`
		Name        string            `json:"name"`
		ClusterID   string            `json:"cluster_id"`
		Annotations map[string]string `json:"annotations"`
	} `json:"nodes"`
	VMs []struct {
		ID          string            `json:"id"`
		Name        string            `json:"name"`
		DisplayName string            `json:"display_name"`
		Annotations map[string]string `json:"annotations"`
	} `json:"vms"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/fixtures.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return f
}

func TestFlatten_Empty(t *testing.T) {
	if rows := Flatten(nil, nil, nil); len(rows) != 0 {
		t.Fatalf("expected zero rows, got %d", len(rows))
	}
}

func TestFlatten_Fixtures(t *testing.T) {
	f := loadFixture(t)
	clusters := make([]ClusterInput, len(f.Clusters))
	for i, c := range f.Clusters {
		clusters[i] = ClusterInput{ID: c.ID, Name: c.Name, DisplayName: c.DisplayName, Annotations: c.Annotations}
	}
	nodes := make([]NodeInput, len(f.Nodes))
	for i, n := range f.Nodes {
		nodes[i] = NodeInput{ID: n.ID, Name: n.Name, ClusterID: n.ClusterID, Annotations: n.Annotations}
	}
	vms := make([]VMInput, len(f.VMs))
	for i, v := range f.VMs {
		vms[i] = VMInput{ID: v.ID, Name: v.Name, DisplayName: v.DisplayName, Annotations: v.Annotations}
	}

	rows := Flatten(clusters, nodes, vms)

	want := []Row{
		{
			EntityType: "cluster", EntityID: "c1", EntityName: "Production EU", Cluster: "Production EU",
			Product: "kubernetes", Cycle: "1.28", Status: "eol",
			EOLDate: "2024-10-28", Latest: "1.28.15", LatestAvailable: "1.32.3",
			CheckedAt: "2026-05-15T00:00:00Z",
		},
		{
			EntityType: "node", EntityID: "n1", EntityName: "worker-01", Cluster: "Production EU",
			Product: "containerd", Cycle: "1.7", Status: "approaching_eol",
			EOLDate: "2026-08-15", CheckedAt: "2026-05-15T00:00:00Z",
		},
		{
			EntityType: "node", EntityID: "n1", EntityName: "worker-01", Cluster: "Production EU",
			Product: "ubuntu", Cycle: "22.04", Status: "supported",
			Latest: "22.04.4", CheckedAt: "2026-05-15T00:00:00Z",
		},
		{
			EntityType: "vm", EntityID: "v1", EntityName: "Bastion (prod-eu)", Cluster: "",
			Product: "cyberwatch", Cycle: "12.4", Status: "unknown",
			CheckedAt: "2026-05-15T00:00:00Z",
		},
		{
			EntityType: "vm", EntityID: "v1", EntityName: "Bastion (prod-eu)", Cluster: "",
			Product: "vault", Cycle: "1.13", Status: "eol",
			EOLDate: "2024-12-09", Latest: "1.13.13", LatestAvailable: "1.18.2",
			CheckedAt: "2026-05-15T00:00:00Z",
		},
	}

	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("Flatten mismatch\n got: %#v\nwant: %#v", rows, want)
	}
}

func TestFlatten_MalformedAnnotation(t *testing.T) {
	clusters := []ClusterInput{{
		ID: "c1", Name: "prod", DisplayName: "Prod",
		Annotations: map[string]string{
			"longue-vue.io/eol.kubernetes": "{not json",
		},
	}}
	rows := Flatten(clusters, nil, nil)
	if len(rows) != 0 {
		t.Fatalf("malformed annotation must be skipped, got %d rows", len(rows))
	}
}

func TestFlatten_SkipsNonEolAnnotations(t *testing.T) {
	clusters := []ClusterInput{{
		ID: "c1", Name: "prod", DisplayName: "Prod",
		Annotations: map[string]string{
			"my-team.io/owner": "alice",
		},
	}}
	if rows := Flatten(clusters, nil, nil); len(rows) != 0 {
		t.Fatalf("non-EOL annotation must be skipped, got %d rows", len(rows))
	}
}
