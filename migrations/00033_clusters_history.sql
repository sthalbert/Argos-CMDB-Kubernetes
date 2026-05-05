-- +goose Up
-- ADR-0021 Phase 2: clusters_history sidecar table.

CREATE TABLE clusters_history (
    -- Surrogate PK for the history row itself.
    history_id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Parent identity. Not a FK: when the parent is hard-deleted by an
    -- operator the historical rows are preserved.
    entity_id           UUID NOT NULL,

    -- Validity window. valid_to NULL = this is the current-state row.
    valid_from          TIMESTAMPTZ NOT NULL,
    valid_to            TIMESTAMPTZ,

    -- Why this row was written.
    change_type         TEXT NOT NULL CHECK (change_type IN ('create', 'update', 'soft_delete', 'restore')),

    -- Actor provenance. NULL actor_id = system/collector-driven.
    actor_id            UUID,
    actor_kind          TEXT CHECK (actor_kind IN ('user', 'token', 'system', 'collector')),

    -- Parent columns verbatim (as of migration 00033).
    name                TEXT NOT NULL,
    display_name        TEXT,
    environment         TEXT,
    provider            TEXT,
    region              TEXT,
    kubernetes_version  TEXT,
    api_endpoint        TEXT,
    labels              JSONB NOT NULL DEFAULT '{}'::jsonb,
    owner               TEXT,
    criticality         TEXT,
    notes               TEXT,
    runbook_url         TEXT,
    annotations         JSONB NOT NULL DEFAULT '{}'::jsonb,
    terminated_at       TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL
);

-- Look up all history rows for a given entity, newest first.
CREATE INDEX clusters_history_entity_idx
    ON clusters_history (entity_id, valid_from DESC);

-- Fast "current state" lookup: at most one row per entity with valid_to IS NULL.
CREATE INDEX clusters_history_current_idx
    ON clusters_history (entity_id) WHERE valid_to IS NULL;

-- Reaper index: scan rows whose valid_to is older than the retention horizon.
CREATE INDEX clusters_history_retention_idx
    ON clusters_history (valid_to)
    WHERE valid_to IS NOT NULL;

-- Idempotent-backfill unique index: guarantees one synthetic 'create' row per entity.
CREATE UNIQUE INDEX clusters_history_create_idx
    ON clusters_history (entity_id) WHERE change_type = 'create';

-- +goose Down
DROP INDEX IF EXISTS clusters_history_create_idx;
DROP INDEX IF EXISTS clusters_history_retention_idx;
DROP INDEX IF EXISTS clusters_history_current_idx;
DROP INDEX IF EXISTS clusters_history_entity_idx;
DROP TABLE IF EXISTS clusters_history;
