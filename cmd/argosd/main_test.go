package main

import "testing"

func TestLoadCollectorClustersJSONPrecedence(t *testing.T) {
	// Having both ARGOS_COLLECTOR_CLUSTERS and the legacy single-cluster vars
	// set, the JSON must win so the operator's migration to multi-cluster
	// doesn't get silently shadowed by stale single-cluster env vars.
	t.Setenv("ARGOS_COLLECTOR_CLUSTERS", `[{"name":"prod","kubeconfig":"/etc/kube/prod.yaml"}]`)
	t.Setenv("ARGOS_CLUSTER_NAME", "legacy")
	t.Setenv("ARGOS_KUBECONFIG", "/etc/kube/legacy.yaml")

	got, err := loadCollectorClusters()
	if err != nil {
		t.Fatalf("loadCollectorClusters: %v", err)
	}
	if len(got) != 1 || got[0].Name != "prod" || got[0].Kubeconfig != "/etc/kube/prod.yaml" {
		t.Errorf("got=%+v, want exactly [{prod, /etc/kube/prod.yaml}]", got)
	}
}

func TestLoadCollectorClustersLegacyFallback(t *testing.T) {
	t.Setenv("ARGOS_COLLECTOR_CLUSTERS", "")
	t.Setenv("ARGOS_CLUSTER_NAME", "dev")
	t.Setenv("ARGOS_KUBECONFIG", "/home/me/.kube/config")

	got, err := loadCollectorClusters()
	if err != nil {
		t.Fatalf("loadCollectorClusters: %v", err)
	}
	if len(got) != 1 || got[0].Name != "dev" || got[0].Kubeconfig != "/home/me/.kube/config" {
		t.Errorf("got=%+v, want [{dev, /home/me/.kube/config}]", got)
	}
}

func TestLoadCollectorClustersLegacyFallbackEmptyKubeconfig(t *testing.T) {
	// Empty ARGOS_KUBECONFIG is legal — it means "use in-cluster config".
	t.Setenv("ARGOS_COLLECTOR_CLUSTERS", "")
	t.Setenv("ARGOS_CLUSTER_NAME", "in-cluster")
	t.Setenv("ARGOS_KUBECONFIG", "")

	got, err := loadCollectorClusters()
	if err != nil {
		t.Fatalf("loadCollectorClusters: %v", err)
	}
	if len(got) != 1 || got[0].Name != "in-cluster" || got[0].Kubeconfig != "" {
		t.Errorf("got=%+v", got)
	}
}

func TestLoadCollectorClustersNoEnv(t *testing.T) {
	t.Setenv("ARGOS_COLLECTOR_CLUSTERS", "")
	t.Setenv("ARGOS_CLUSTER_NAME", "")
	t.Setenv("ARGOS_KUBECONFIG", "")

	if _, err := loadCollectorClusters(); err == nil {
		t.Fatal("expected an error when no cluster env is set")
	}
}

func TestLoadCollectorClustersMultiCluster(t *testing.T) {
	t.Setenv("ARGOS_COLLECTOR_CLUSTERS", `[
		{"name":"prod","kubeconfig":"/etc/kube/prod.yaml"},
		{"name":"staging","kubeconfig":"/etc/kube/staging.yaml"},
		{"name":"in-cluster","kubeconfig":""}
	]`)

	got, err := loadCollectorClusters()
	if err != nil {
		t.Fatalf("loadCollectorClusters: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	if got[2].Name != "in-cluster" || got[2].Kubeconfig != "" {
		t.Errorf("in-cluster entry = %+v", got[2])
	}
}

func TestLoadCollectorClustersMalformedJSON(t *testing.T) {
	t.Setenv("ARGOS_COLLECTOR_CLUSTERS", `[{"name":"prod"`) // truncated

	if _, err := loadCollectorClusters(); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadCollectorClustersEmptyJSONArray(t *testing.T) {
	t.Setenv("ARGOS_COLLECTOR_CLUSTERS", `[]`)
	t.Setenv("ARGOS_CLUSTER_NAME", "legacy") // legacy must not be used: JSON presence wins even if empty.

	if _, err := loadCollectorClusters(); err == nil {
		t.Fatal("expected error on empty JSON array")
	}
}

func TestLoadCollectorClustersEmptyName(t *testing.T) {
	t.Setenv("ARGOS_COLLECTOR_CLUSTERS", `[{"name":"","kubeconfig":"/x"}]`)

	if _, err := loadCollectorClusters(); err == nil {
		t.Fatal("expected error on empty cluster name")
	}
}

func TestLoadCollectorClustersDuplicateName(t *testing.T) {
	t.Setenv("ARGOS_COLLECTOR_CLUSTERS", `[
		{"name":"prod","kubeconfig":"/a"},
		{"name":"prod","kubeconfig":"/b"}
	]`)

	if _, err := loadCollectorClusters(); err == nil {
		t.Fatal("expected error on duplicate cluster name")
	}
}
