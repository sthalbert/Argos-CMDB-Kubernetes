package main

import (
	"errors"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// loadCollectorConfig
// ---------------------------------------------------------------------------

func TestLoadCollectorConfig_AllRequired(t *testing.T) {
	t.Setenv("LONGUE_VUE_SERVER_URL", "https://argos.test:8080")
	t.Setenv("LONGUE_VUE_API_TOKEN", "argos_pat_xxxx_yyyy")
	t.Setenv("LONGUE_VUE_CLUSTER_NAME", "test-cluster")

	cfg, err := loadCollectorConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.serverURL != "https://argos.test:8080" {
		t.Errorf("serverURL: want https://argos.test:8080, got %q", cfg.serverURL)
	}
	if cfg.token != "argos_pat_xxxx_yyyy" {
		t.Errorf("token: want argos_pat_xxxx_yyyy, got %q", cfg.token)
	}
	if cfg.clusterName != "test-cluster" {
		t.Errorf("clusterName: want test-cluster, got %q", cfg.clusterName)
	}
	if cfg.interval != 5*time.Minute {
		t.Errorf("interval: want 5m, got %v", cfg.interval)
	}
	if cfg.fetchTimeout != 30*time.Second {
		t.Errorf("fetchTimeout: want 30s, got %v", cfg.fetchTimeout)
	}
	if !cfg.reconcile {
		t.Error("reconcile: want true, got false")
	}
}

func TestLoadCollectorConfig_MissingServerURL(t *testing.T) {
	t.Setenv("LONGUE_VUE_SERVER_URL", "")
	t.Setenv("LONGUE_VUE_API_TOKEN", "tok")
	t.Setenv("LONGUE_VUE_CLUSTER_NAME", "c")

	_, err := loadCollectorConfig()
	if !errors.Is(err, errServerURLRequired) {
		t.Errorf("want errServerURLRequired, got %v", err)
	}
}

func TestLoadCollectorConfig_MissingAPIToken(t *testing.T) {
	t.Setenv("LONGUE_VUE_SERVER_URL", "https://x")
	t.Setenv("LONGUE_VUE_API_TOKEN", "")
	t.Setenv("LONGUE_VUE_CLUSTER_NAME", "c")

	_, err := loadCollectorConfig()
	if !errors.Is(err, errAPITokenRequired) {
		t.Errorf("want errAPITokenRequired, got %v", err)
	}
}

func TestLoadCollectorConfig_MissingClusterName(t *testing.T) {
	t.Setenv("LONGUE_VUE_SERVER_URL", "https://x")
	t.Setenv("LONGUE_VUE_API_TOKEN", "tok")
	t.Setenv("LONGUE_VUE_CLUSTER_NAME", "")

	_, err := loadCollectorConfig()
	if !errors.Is(err, errClusterNameRequired) {
		t.Errorf("want errClusterNameRequired, got %v", err)
	}
}

func TestLoadCollectorConfig_CustomValues(t *testing.T) {
	t.Setenv("LONGUE_VUE_SERVER_URL", "https://gw:443/argos")
	t.Setenv("LONGUE_VUE_API_TOKEN", "tok")
	t.Setenv("LONGUE_VUE_CLUSTER_NAME", "zad-prod")
	t.Setenv("LONGUE_VUE_KUBECONFIG", "/etc/kube/config")
	t.Setenv("LONGUE_VUE_COLLECTOR_INTERVAL", "30s")
	t.Setenv("LONGUE_VUE_COLLECTOR_FETCH_TIMEOUT", "1m")
	t.Setenv("LONGUE_VUE_COLLECTOR_RECONCILE", "false")

	cfg, err := loadCollectorConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.kubeconfig != "/etc/kube/config" {
		t.Errorf("kubeconfig: want /etc/kube/config, got %q", cfg.kubeconfig)
	}
	if cfg.interval != 30*time.Second {
		t.Errorf("interval: want 30s, got %v", cfg.interval)
	}
	if cfg.fetchTimeout != 1*time.Minute {
		t.Errorf("fetchTimeout: want 1m, got %v", cfg.fetchTimeout)
	}
	if cfg.reconcile {
		t.Error("reconcile: want false, got true")
	}
}

func TestLoadCollectorConfig_InvalidInterval(t *testing.T) {
	t.Setenv("LONGUE_VUE_SERVER_URL", "https://x")
	t.Setenv("LONGUE_VUE_API_TOKEN", "tok")
	t.Setenv("LONGUE_VUE_CLUSTER_NAME", "c")
	t.Setenv("LONGUE_VUE_COLLECTOR_INTERVAL", "not-a-duration")

	_, err := loadCollectorConfig()
	if err == nil {
		t.Fatal("expected error for invalid interval")
	}
}

func TestLoadCollectorConfig_InvalidReconcile(t *testing.T) {
	t.Setenv("LONGUE_VUE_SERVER_URL", "https://x")
	t.Setenv("LONGUE_VUE_API_TOKEN", "tok")
	t.Setenv("LONGUE_VUE_CLUSTER_NAME", "c")
	t.Setenv("LONGUE_VUE_COLLECTOR_RECONCILE", "not-a-bool")

	_, err := loadCollectorConfig()
	if err == nil {
		t.Fatal("expected error for invalid reconcile")
	}
}

// ---------------------------------------------------------------------------
// parseExtraHeaders
// ---------------------------------------------------------------------------

func TestParseExtraHeaders_Empty(t *testing.T) {
	h := parseExtraHeaders("")
	if h != nil {
		t.Errorf("want nil, got %v", h)
	}
}

func TestParseExtraHeaders_Single(t *testing.T) {
	h := parseExtraHeaders("X-Tenant=prod")
	if len(h) != 1 || h["X-Tenant"] != "prod" {
		t.Errorf("want {X-Tenant:prod}, got %v", h)
	}
}

func TestParseExtraHeaders_Multiple(t *testing.T) {
	h := parseExtraHeaders("X-Tenant=prod, X-Region=eu-west-1")
	if len(h) != 2 {
		t.Fatalf("want 2 headers, got %d", len(h))
	}
	if h["X-Tenant"] != "prod" {
		t.Errorf("X-Tenant: want prod, got %q", h["X-Tenant"])
	}
	if h["X-Region"] != "eu-west-1" {
		t.Errorf("X-Region: want eu-west-1, got %q", h["X-Region"])
	}
}

func TestParseExtraHeaders_SkipsInvalid(t *testing.T) {
	h := parseExtraHeaders("good=val,,=nope,also-bad")
	if len(h) != 1 || h["good"] != "val" {
		t.Errorf("want {good:val}, got %v", h)
	}
}

// ---------------------------------------------------------------------------
// parseDurationEnv / parseBoolEnv
// ---------------------------------------------------------------------------

func TestParseDurationEnv_Default(t *testing.T) {
	t.Setenv("TEST_DUR", "")
	d, err := parseDurationEnv("TEST_DUR", 42*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if d != 42*time.Second {
		t.Errorf("want 42s, got %v", d)
	}
}

func TestParseDurationEnv_Set(t *testing.T) {
	t.Setenv("TEST_DUR", "10m")
	d, err := parseDurationEnv("TEST_DUR", 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if d != 10*time.Minute {
		t.Errorf("want 10m, got %v", d)
	}
}

func TestParseDurationEnv_Invalid(t *testing.T) {
	t.Setenv("TEST_DUR", "xyz")
	_, err := parseDurationEnv("TEST_DUR", 0)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBoolEnv_Default(t *testing.T) {
	t.Setenv("TEST_BOOL", "")
	b, err := parseBoolEnv("TEST_BOOL", true)
	if err != nil {
		t.Fatal(err)
	}
	if !b {
		t.Error("want true")
	}
}

func TestParseBoolEnv_Set(t *testing.T) {
	t.Setenv("TEST_BOOL", "false")
	b, err := parseBoolEnv("TEST_BOOL", true)
	if err != nil {
		t.Fatal(err)
	}
	if b {
		t.Error("want false")
	}
}

func TestParseBoolEnv_Invalid(t *testing.T) {
	t.Setenv("TEST_BOOL", "maybe")
	_, err := parseBoolEnv("TEST_BOOL", false)
	if err == nil {
		t.Fatal("expected error")
	}
}
