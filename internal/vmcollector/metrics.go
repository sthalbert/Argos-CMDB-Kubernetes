// Metrics for the longue-vue-vm-collector binary (ADR-0015 §IMP-009).
//
// Exposed on a localhost-only listener separate from longue-vue's /metrics —
// the collector is a standalone binary, often deployed where Prometheus
// scrapes it via a sidecar or node-exporter pattern.

package vmcollector

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the vm-collector's private Prometheus registry. Separate
// from longue-vue's so the two binaries can run side by side without
// stepping on each other.
var Registry = prometheus.NewRegistry()

var (
	ticksTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "vm_collector",
		Name:      "ticks_total",
		Help:      "Cumulative collector ticks, labelled by status (success / error).",
	}, []string{"status"})

	tickDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "longue_vue",
		Subsystem: "vm_collector",
		Name:      "tick_duration_seconds",
		Help:      "Collector tick duration in seconds.",
		Buckets:   prometheus.DefBuckets,
	})

	vmsObserved = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Subsystem: "vm_collector",
		Name:      "vms_observed",
		Help:      "VMs returned by the cloud-provider API on the most recent tick (post-filter).",
	})

	vmsSkippedKubernetes = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "vm_collector",
		Name:      "vms_skipped_kubernetes_total",
		Help:      "Cumulative VMs skipped because they are already inventoried as Kubernetes nodes (server-side 409 or local OscK8s tag pre-filter).",
	})

	credentialRefreshes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "longue_vue",
		Subsystem: "vm_collector",
		Name:      "credential_refreshes_total",
		Help:      "Cumulative credential-fetch attempts, labelled by result (success / error).",
	}, []string{"result"})

	lastSuccessTimestamp = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Subsystem: "vm_collector",
		Name:      "last_success_timestamp_seconds",
		Help:      "Unix timestamp of the most recent successful tick.",
	})

	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "longue_vue",
		Subsystem: "vm_collector",
		Name:      "build_info",
		Help:      "Set to 1 for the running longue-vue-vm-collector build; labels carry version.",
	}, []string{"version"})
)

func init() {
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		ticksTotal,
		tickDuration,
		vmsObserved,
		vmsSkippedKubernetes,
		credentialRefreshes,
		lastSuccessTimestamp,
		buildInfo,
	)
}

// MetricsHandler returns the /metrics HTTP handler over the
// vm-collector's private registry.
func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}

// SetBuildInfo sets the build_info gauge to 1 for the given version.
func SetBuildInfo(version string) {
	buildInfo.WithLabelValues(version).Set(1)
}

// ObserveTick records the outcome of one tick. status is "success" or
// "error".
func ObserveTick(status string, duration time.Duration) {
	ticksTotal.WithLabelValues(status).Inc()
	tickDuration.Observe(duration.Seconds())
	if status == "success" {
		lastSuccessTimestamp.Set(float64(time.Now().Unix()))
	}
}

// SetVMsObserved sets the gauge of post-filter VMs from the most
// recent tick.
func SetVMsObserved(n int) {
	vmsObserved.Set(float64(n))
}

// IncSkippedKubernetes increments the kube-skipped counter by n.
func IncSkippedKubernetes(n int) {
	if n <= 0 {
		return
	}
	vmsSkippedKubernetes.Add(float64(n))
}

// ObserveCredentialRefresh records a credential-refresh attempt.
// result is "success" or "error".
func ObserveCredentialRefresh(result string) {
	credentialRefreshes.WithLabelValues(result).Inc()
}
