-- +goose Up
-- ADR-0021 Phase 1: soft-delete foundation. Each of clusters, namespaces,
-- nodes, workloads gains a nullable terminated_at TIMESTAMPTZ. List
-- endpoints filter it out by default; the collector reconcile path
-- soft-deletes via UPDATE rather than DELETE so history of which
-- entities existed and when they were reaped survives a tick. Mirrors
-- the existing virtual_machines.terminated_at pattern from ADR-0015 §2.

ALTER TABLE clusters   ADD COLUMN IF NOT EXISTS terminated_at TIMESTAMPTZ;
ALTER TABLE namespaces ADD COLUMN IF NOT EXISTS terminated_at TIMESTAMPTZ;
ALTER TABLE nodes      ADD COLUMN IF NOT EXISTS terminated_at TIMESTAMPTZ;
ALTER TABLE workloads  ADD COLUMN IF NOT EXISTS terminated_at TIMESTAMPTZ;

-- Partial indexes — only terminated rows are indexed, keeping the
-- live-row query path index-free for terminated_at and the index
-- itself tiny in steady state. Mirror of virtual_machines_terminated_at_idx.
CREATE INDEX IF NOT EXISTS clusters_terminated_at_idx
    ON clusters (terminated_at) WHERE terminated_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS namespaces_terminated_at_idx
    ON namespaces (terminated_at) WHERE terminated_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS nodes_terminated_at_idx
    ON nodes (terminated_at) WHERE terminated_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS workloads_terminated_at_idx
    ON workloads (terminated_at) WHERE terminated_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS workloads_terminated_at_idx;
DROP INDEX IF EXISTS nodes_terminated_at_idx;
DROP INDEX IF EXISTS namespaces_terminated_at_idx;
DROP INDEX IF EXISTS clusters_terminated_at_idx;

ALTER TABLE workloads  DROP COLUMN IF EXISTS terminated_at;
ALTER TABLE nodes      DROP COLUMN IF EXISTS terminated_at;
ALTER TABLE namespaces DROP COLUMN IF EXISTS terminated_at;
ALTER TABLE clusters   DROP COLUMN IF EXISTS terminated_at;
