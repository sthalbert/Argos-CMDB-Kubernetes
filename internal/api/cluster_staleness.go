package api

import (
	"context"
	"log/slog"
	"time"
)

// Derived cluster staleness (heartbeat-based). The stale flag is computed
// per response from clusters.last_seen_at and the cluster_stale_after_days
// admin setting — never persisted. Mirrors the withClusterLayer decoration
// pattern in layers.go.

// clusterStaleCutoff resolves the staleness cutoff. enabled=false when the
// feature is off (threshold <= 0) or settings cannot be read (fail-open:
// nothing is reported stale rather than everything).
func (s *Server) clusterStaleCutoff(ctx context.Context) (cutoff time.Time, enabled bool) {
	st, err := s.store.GetSettings(ctx)
	if err != nil {
		slog.Warn("cluster staleness: settings read failed; treating as disabled",
			slog.Any("error", err))
		return time.Time{}, false
	}
	if st.ClusterStaleAfterDays <= 0 {
		return time.Time{}, false
	}
	return time.Now().UTC().AddDate(0, 0, -st.ClusterStaleAfterDays), true
}

func withClusterStaleness(c Cluster, cutoff time.Time, enabled bool) Cluster { //nolint:gocritic // intentional value copy for immutable decoration
	stale := enabled && c.LastSeenAt != nil && c.LastSeenAt.Before(cutoff)
	c.Stale = &stale
	return c
}
