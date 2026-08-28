-- +goose Up
-- Cluster staleness heartbeat (see docs/adr entry added in this change).
-- last_seen_at is refreshed by every successful collector tick via
-- EnsureCluster (in-process pull, or push mode through POST /v1/clusters).
-- A cluster whose heartbeat is older than settings.cluster_stale_after_days
-- is surfaced as stale — derived at read time, never materialized.
-- Backfilled to now() so every existing row gets a full grace window;
-- live clusters refresh on their next tick (default 5 min).
ALTER TABLE clusters
  ADD COLUMN IF NOT EXISTS last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- +goose Down
ALTER TABLE clusters DROP COLUMN IF EXISTS last_seen_at;
