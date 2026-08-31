package metricsrefresh

// Unit tests for the cluster-heartbeat gauge pass. Mirrors the fake-store
// approach of refresher_flows_test.go: narrow fakes, gauges read back
// through the exported metrics.Registry.

import (
	"context"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

type hbFakeStore struct{ hbs []metrics.ClusterHeartbeat }

func (f *hbFakeStore) DICTCoverageCounts(context.Context) (application, workload, none int, err error) {
	return 0, 0, 0, nil
}

func (f *hbFakeStore) ApplicationMetricCounts(context.Context) ([]metrics.ApplicationCount, int, error) {
	return nil, 0, nil
}

func (f *hbFakeStore) ClusterHeartbeats(context.Context) ([]metrics.ClusterHeartbeat, error) {
	return f.hbs, nil
}

// hbSettingsStore satisfies api.Store via the embedded nil interface and
// overrides only GetSettings (same trick as flowFakeStore).
type hbSettingsStore struct {
	api.Store
	days int
}

func (f *hbSettingsStore) GetSettings(context.Context) (api.Settings, error) {
	return api.Settings{ClusterStaleAfterDays: f.days}, nil
}

func gatherGauge(t *testing.T, name string) []*dto.Metric {
	t.Helper()
	mfs, err := metrics.Registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf.GetMetric()
		}
	}
	return nil
}

func TestRefreshClusterHeartbeats_SetsGaugesAndStaleCount(t *testing.T) {
	old := time.Now().UTC().AddDate(0, 0, -30)
	fresh := time.Now().UTC()
	r := &Refresher{
		store: &hbFakeStore{hbs: []metrics.ClusterHeartbeat{
			{Name: "old-cluster", LastSeenAt: old},
			{Name: "fresh-cluster", LastSeenAt: fresh},
		}},
		flowStore: &hbSettingsStore{days: 7},
		interval:  time.Minute,
	}
	r.refreshClusterHeartbeats(context.Background())

	series := gatherGauge(t, "longue_vue_cluster_last_seen_timestamp_seconds")
	if len(series) != 2 {
		t.Fatalf("want 2 heartbeat series, got %d", len(series))
	}
	staleSeries := gatherGauge(t, "longue_vue_clusters_stale")
	if len(staleSeries) != 1 || staleSeries[0].GetGauge().GetValue() != 1 {
		t.Fatalf("clusters_stale: want value 1, got %+v", staleSeries)
	}
}

func TestRefreshClusterHeartbeats_DisabledZeroesStaleCount(t *testing.T) {
	r := &Refresher{
		store: &hbFakeStore{hbs: []metrics.ClusterHeartbeat{
			{Name: "ancient", LastSeenAt: time.Now().UTC().AddDate(0, 0, -300)},
		}},
		flowStore: &hbSettingsStore{days: 0},
		interval:  time.Minute,
	}
	r.refreshClusterHeartbeats(context.Background())

	staleSeries := gatherGauge(t, "longue_vue_clusters_stale")
	if len(staleSeries) != 1 || staleSeries[0].GetGauge().GetValue() != 0 {
		t.Fatalf("disabled: clusters_stale must be 0, got %+v", staleSeries)
	}
}
