package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestInstrumentHandlerCountsRequestsByStatusClass(t *testing.T) {
	// Minimal mux with two registered patterns so req.Pattern is populated.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /notfound", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("GET /boom", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	h := InstrumentHandler(mux)

	for _, path := range []string{"/ok", "/notfound", "/boom"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}

	if got := testutil.ToFloat64(httpRequestsTotal.WithLabelValues("GET", "GET /ok", "2xx")); got != 1 {
		t.Errorf("/ok counter = %v, want 1", got)
	}
	// 404 is promoted out of the class bucket into its own label per statusClass.
	if got := testutil.ToFloat64(httpRequestsTotal.WithLabelValues("GET", "GET /notfound", "404")); got != 1 {
		t.Errorf("/notfound counter = %v, want 1", got)
	}
	if got := testutil.ToFloat64(httpRequestsTotal.WithLabelValues("GET", "GET /boom", "5xx")); got != 1 {
		t.Errorf("/boom counter = %v, want 1", got)
	}
}

func TestStatusClassFolding(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{200, "2xx"},
		{201, "2xx"},
		{302, "3xx"},
		{400, "4xx"},
		{401, "401"},
		{403, "403"},
		{404, "404"},
		{409, "409"},
		{418, "4xx"},
		{500, "5xx"},
		{503, "5xx"},
	}
	for _, tt := range tests {
		if got := statusClass(tt.code); got != tt.want {
			t.Errorf("statusClass(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestHandlerExposesRegisteredMetrics(t *testing.T) {
	// Increment a few counters so there's something to scrape.
	ObserveUpserts("smoke-cluster", "pods", 3)
	ObserveError("smoke-cluster", "pods", "upsert")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`argos_collector_upserted_total{cluster="smoke-cluster",resource="pods"}`,
		`argos_collector_errors_total{cluster="smoke-cluster",phase="upsert",resource="pods"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q\nbody: %s", want, body)
		}
	}
}
