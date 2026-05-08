package imageversions

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetrics_RegisterAndIncrement(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.TickTotal.WithLabelValues("success").Inc()
	m.ObserveQuery("docker.io", "success", 0.123)
	m.RegistriesEnabledTotal.Set(7)

	if v := testutil.ToFloat64(m.TickTotal.WithLabelValues("success")); v != 1 {
		t.Errorf("tick_total: want 1, got %v", v)
	}
	if v := testutil.ToFloat64(m.QueryTotal.WithLabelValues("docker.io", "success")); v != 1 {
		t.Errorf("query_total: want 1, got %v", v)
	}
	if v := testutil.ToFloat64(m.RegistriesEnabledTotal); v != 7 {
		t.Errorf("registries_enabled: want 7, got %v", v)
	}
}
