//go:build live

package registry_test

import (
	"context"
	"os"
	"testing"

	"github.com/sthalbert/longue-vue/internal/imageversions/registry"
)

func skipUnlessLive(t *testing.T) {
	if os.Getenv("LONGUE_VUE_LIVE_TESTS") != "1" {
		t.Skip("set LONGUE_VUE_LIVE_TESTS=1 to enable live smoke tests")
	}
}

func TestLive_DockerHub_Nginx(t *testing.T) {
	skipUnlessLive(t)
	c := registry.NewClient()
	tags, err := c.ListTags(context.Background(), "https://registry-1.docker.io", "library/nginx")
	if err != nil {
		t.Fatalf("dockerhub: %v", err)
	}
	if len(tags) < 10 {
		t.Fatalf("expected many nginx tags, got %d", len(tags))
	}
}

func TestLive_Quay_Prometheus(t *testing.T) {
	skipUnlessLive(t)
	c := registry.NewClient()
	tags, err := c.ListTags(context.Background(), "https://quay.io", "prometheus/prometheus")
	if err != nil {
		t.Fatalf("quay: %v", err)
	}
	if len(tags) < 5 {
		t.Fatalf("expected several prometheus tags, got %d", len(tags))
	}
}
