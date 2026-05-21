package imageversions

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sthalbert/longue-vue/internal/imageversions/mirrorresolve"
)

// Metrics bundles the Prometheus collectors for the image versions enricher.
// Constructed once at startup and passed into the enricher and the registry client.
type Metrics struct {
	TickTotal              *prometheus.CounterVec
	TickDuration           prometheus.Histogram
	QueryTotal             *prometheus.CounterVec
	QueryDuration          *prometheus.HistogramVec
	KnownTotal             prometheus.Gauge
	WithErrorTotal         prometheus.Gauge
	LastTickTimestamp      prometheus.Gauge
	RegistriesEnabledTotal prometheus.Gauge
	MirrorResolveTotal     *prometheus.CounterVec
	MirrorResolveDuration  prometheus.Histogram
}

// NewMetrics constructs and registers the metric collectors.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		TickTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "imageversions_tick_total", Help: "Image-versions enricher ticks completed."},
			[]string{"status"},
		),
		TickDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "imageversions_tick_duration_seconds",
				Help:    "Tick wall time in seconds.",
				Buckets: prometheus.DefBuckets,
			},
		),
		QueryTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "imageversions_query_total", Help: "Registry tag-list queries."},
			[]string{"registry", "status"},
		),
		QueryDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "imageversions_query_duration_seconds",
				Help:    "Per-registry query latency in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"registry"},
		),
		KnownTotal: prometheus.NewGauge(
			prometheus.GaugeOpts{Name: "imageversions_known_total", Help: "Rows in image_versions."},
		),
		WithErrorTotal: prometheus.NewGauge(
			prometheus.GaugeOpts{Name: "imageversions_with_error_total", Help: "Rows in image_versions with last_error set."},
		),
		LastTickTimestamp: prometheus.NewGauge(
			prometheus.GaugeOpts{Name: "imageversions_last_tick_timestamp_seconds", Help: "Timestamp of the last completed tick (Unix seconds)."},
		),
		RegistriesEnabledTotal: prometheus.NewGauge(
			prometheus.GaugeOpts{Name: "imageversions_registries_enabled", Help: "Count of enabled rows in image_versions_registries."},
		),
		MirrorResolveTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "imageversions_mirror_resolve_total", Help: "Mirror-resolve attempts by result."},
			[]string{"result"},
		),
		MirrorResolveDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "imageversions_mirror_resolve_duration_seconds",
				Help:    "Mirror-resolve wall time in seconds.",
				Buckets: prometheus.DefBuckets,
			},
		),
	}
	reg.MustRegister(
		m.TickTotal, m.TickDuration, m.QueryTotal, m.QueryDuration,
		m.KnownTotal, m.WithErrorTotal, m.LastTickTimestamp, m.RegistriesEnabledTotal,
		m.MirrorResolveTotal, m.MirrorResolveDuration,
	)
	return m
}

// observerAdapter bridges mirrorresolve.Observer to *Metrics.
type observerAdapter struct{ m *Metrics }

// ObserveResolve implements mirrorresolve.Observer.
func (o observerAdapter) ObserveResolve(result string, d time.Duration) {
	if o.m == nil {
		return
	}
	o.m.MirrorResolveTotal.WithLabelValues(result).Inc()
	o.m.MirrorResolveDuration.Observe(d.Seconds())
}

// NewObserver returns a mirrorresolve.Observer that records into m. Safe
// with a nil *Metrics (the resulting Observer is a no-op).
func NewObserver(m *Metrics) mirrorresolve.Observer { return observerAdapter{m: m} }

// ObserveQuery is the implementation of the registry.QueryObserver interface.
func (m *Metrics) ObserveQuery(registryHost, status string, durationSeconds float64) {
	if m == nil {
		return
	}
	m.QueryTotal.WithLabelValues(registryHost, status).Inc()
	m.QueryDuration.WithLabelValues(registryHost).Observe(durationSeconds)
}
