-- +goose Up
-- ADR-0021 Phase 2: namespaces_history sidecar table.

CREATE TABLE namespaces_history (
    history_id   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id    UUID NOT NULL,
    valid_from   TIMESTAMPTZ NOT NULL,
    valid_to     TIMESTAMPTZ,
    change_type  TEXT NOT NULL CHECK (change_type IN ('create', 'update', 'soft_delete', 'restore')),
    actor_id     UUID,
    actor_kind   TEXT CHECK (actor_kind IN ('user', 'token', 'system', 'collector')),

    -- Parent columns verbatim (as of migration 00034).
    cluster_id   UUID NOT NULL,
    name         TEXT NOT NULL,
    display_name TEXT,
    phase        TEXT,
    labels       JSONB NOT NULL DEFAULT '{}'::jsonb,
    owner        TEXT,
    criticality  TEXT,
    notes        TEXT,
    runbook_url  TEXT,
    annotations  JSONB NOT NULL DEFAULT '{}'::jsonb,
    terminated_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX namespaces_history_entity_idx
    ON namespaces_history (entity_id, valid_from DESC);
CREATE INDEX namespaces_history_current_idx
    ON namespaces_history (entity_id) WHERE valid_to IS NULL;
CREATE INDEX namespaces_history_retention_idx
    ON namespaces_history (valid_to) WHERE valid_to IS NOT NULL;
CREATE UNIQUE INDEX namespaces_history_create_idx
    ON namespaces_history (entity_id) WHERE change_type = 'create';

-- +goose Down
DROP INDEX IF EXISTS namespaces_history_create_idx;
DROP INDEX IF EXISTS namespaces_history_retention_idx;
DROP INDEX IF EXISTS namespaces_history_current_idx;
DROP INDEX IF EXISTS namespaces_history_entity_idx;
DROP TABLE IF EXISTS namespaces_history;
