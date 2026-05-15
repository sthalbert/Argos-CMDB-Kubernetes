package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestObserveExtract(t *testing.T) {
	ObserveExtract("search", "csv", "ok", 12)
	ObserveExtract("search", "csv", "ok", 8)
	got := testutil.ToFloat64(extractsTotal.WithLabelValues("search", "csv", "ok"))
	if got != 2 {
		t.Fatalf("extractsTotal: want 2, got %v", got)
	}
	got = testutil.ToFloat64(extractRowsTotal.WithLabelValues("search"))
	if got != 20 {
		t.Fatalf("extractRowsTotal: want 20, got %v", got)
	}
}

func TestExtractMetricNames(t *testing.T) {
	for _, m := range []string{"longue_vue_extracts_total", "longue_vue_extract_rows_total"} {
		if !strings.Contains(m, "longue_vue") {
			t.Errorf("metric name missing namespace: %s", m)
		}
		if !strings.HasSuffix(m, "_total") {
			t.Errorf("metric name not _total-suffixed: %s", m)
		}
	}
}
