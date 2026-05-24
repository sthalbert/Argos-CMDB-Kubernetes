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

	"github.com/sthalbert/longue-vue/internal/metrics"
)

// Store is the narrow slice of the application store the refresher needs.
type Store interface {
	// DICTCoverageCounts returns workloads bucketed by effective-DICT source
	// (ADR-0029 §6).
	DICTCoverageCounts(ctx context.Context) (application, workload, none int, err error)
}

// Refresher periodically recomputes store-derived gauges.
type Refresher struct {
	store    Store
	interval time.Duration
}

// New builds a Refresher. interval defaults to 60s when non-positive.
func New(store Store, interval time.Duration) *Refresher {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &Refresher{store: store, interval: interval}
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
	app, wl, none, err := r.store.DICTCoverageCounts(ctx)
	if err != nil {
		slog.Warn("metrics refresher: dict coverage query failed", slog.Any("error", err))
		return
	}
	metrics.SetDICTCoverage(app, wl, none)
}
