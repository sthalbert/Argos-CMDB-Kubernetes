// Package metricsrefresh hosts the periodic goroutine that recomputes
// store-derived Prometheus gauges (those that can't be incremented inline on
// a request path). It mirrors the EOL enricher's ticker pattern (ADR-0012)
// and lives in its own package so internal/metrics stays a leaf with no
// dependency on the store.
package metricsrefresh

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/flowmatrix"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

// Store is the narrow slice of the application store the refresher needs.
type Store interface {
	// DICTCoverageCounts returns workloads bucketed by effective-DICT source
	// (ADR-0029 §6).
	DICTCoverageCounts(ctx context.Context) (application, workload, none int, err error)
	// ApplicationMetricCounts returns applications bucketed by
	// (has_block, has_dict) plus the total application_blocks count
	// (ADR-0029 §9).
	ApplicationMetricCounts(ctx context.Context) (buckets []metrics.ApplicationCount, blocks int, err error)
	// ClusterHeartbeats returns each live cluster's name + last_seen_at
	// for the heartbeat gauges.
	ClusterHeartbeats(ctx context.Context) ([]metrics.ClusterHeartbeat, error)
}

// Refresher periodically recomputes store-derived gauges.
type Refresher struct {
	store    Store
	interval time.Duration
	// flowStore is the full application store, used for the flow-matrix gauge
	// pass. It's optional: when the concrete store doesn't satisfy api.Store
	// (e.g. a narrow test fake) the flow pass is skipped. In production the
	// *store.PG passed to New satisfies both Store and api.Store.
	flowStore api.Store
}

// New builds a Refresher. interval defaults to 60s when non-positive. When the
// concrete store also satisfies api.Store, the flow-matrix gauge pass is wired
// in; otherwise it is silently skipped.
func New(store Store, interval time.Duration) *Refresher {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	r := &Refresher{store: store, interval: interval}
	if fs, ok := store.(api.Store); ok {
		r.flowStore = fs
	}
	return r
}

// Run refreshes once immediately, then on each tick until ctx is cancelled.
// Errors are logged and swallowed so the ticker keeps running.
func (r *Refresher) Run(ctx context.Context) error {
	slog.Info("metrics refresher started", slog.String("interval", r.interval.String()))
	r.refresh(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("metrics refresher stopped")
			return fmt.Errorf("metrics refresher: %w", ctx.Err())
		case <-ticker.C:
			r.refresh(ctx)
		}
	}
}

func (r *Refresher) refresh(ctx context.Context) {
	if app, wl, none, err := r.store.DICTCoverageCounts(ctx); err != nil {
		slog.Warn("metrics refresher: dict coverage query failed", slog.Any("error", err))
	} else {
		metrics.SetDICTCoverage(app, wl, none)
	}

	if buckets, blocks, err := r.store.ApplicationMetricCounts(ctx); err != nil {
		slog.Warn("metrics refresher: application counts query failed", slog.Any("error", err))
	} else {
		metrics.SetApplicationCounts(buckets)
		metrics.SetApplicationBlocksTotal(blocks)
	}

	r.refreshClusterHeartbeats(ctx)
	r.refreshFlows(ctx)
}

// refreshFlows recomputes the flow-matrix gauges from the read-time synthesis.
// Best-effort throughout: gated on flow_matrix_enabled, only covers clusters
// with >=1 reference row, and skips any cluster whose inputs fail to load. A
// failure here never aborts the surrounding refresh tick.
func (r *Refresher) refreshFlows(ctx context.Context) {
	if r.flowStore == nil {
		return
	}
	st, err := r.flowStore.GetSettings(ctx)
	if err != nil {
		slog.Warn("metrics refresher: flow settings query failed", slog.Any("error", err))
		return
	}
	if !st.FlowMatrixEnabled {
		// Toggle off: clear any series left from a previous enabled window.
		metrics.ResetFlowGauges()
		return
	}

	clusters, err := r.flowStore.ListClustersWithFlowReferences(ctx)
	if err != nil {
		slog.Warn("metrics refresher: flow reference clusters query failed", slog.Any("error", err))
		return
	}

	metrics.ResetFlowGauges()
	for _, cid := range clusters {
		inputs, warnings, lerr := api.LoadFlowInputsForMetrics(ctx, r.flowStore, cid)
		if lerr != nil {
			slog.Warn("metrics refresher: load flow inputs failed",
				slog.String("cluster", cid.String()), slog.Any("error", lerr))
			continue
		}
		syn := flowmatrix.Synthesize(inputs)
		perim := map[string]int{}
		inter := map[string]int{}
		for i := range syn.Perimeter {
			perim[string(syn.Perimeter[i].State)]++
		}
		for i := range syn.Internal {
			inter[string(syn.Internal[i].State)]++
		}
		metrics.SetFlowStates(cid.String(), "perimeter", perim)
		metrics.SetFlowStates(cid.String(), "internal", inter)
		metrics.SetFlowReferenceRows(cid.String(), len(inputs.References))
		metrics.SetFlowDanglingRefs(cid.String(), len(warnings))
	}
}

// refreshClusterHeartbeats exports the per-cluster heartbeat gauges and
// the stale-count gauge. The count needs the cluster_stale_after_days
// setting, read through flowStore (nil only for narrow test fakes — the
// per-cluster timestamps are still exported in that case).
func (r *Refresher) refreshClusterHeartbeats(ctx context.Context) {
	hbs, err := r.store.ClusterHeartbeats(ctx)
	if err != nil {
		slog.Warn("metrics refresher: cluster heartbeats query failed", slog.Any("error", err))
		return
	}
	metrics.SetClusterHeartbeats(hbs)

	if r.flowStore == nil {
		return
	}
	st, err := r.flowStore.GetSettings(ctx)
	if err != nil {
		slog.Warn("metrics refresher: settings query failed for stale count", slog.Any("error", err))
		return
	}
	if st.ClusterStaleAfterDays <= 0 {
		metrics.SetClustersStale(0)
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -st.ClusterStaleAfterDays)
	n := 0
	for _, hb := range hbs {
		if hb.LastSeenAt.Before(cutoff) {
			n++
		}
	}
	metrics.SetClustersStale(n)
}
