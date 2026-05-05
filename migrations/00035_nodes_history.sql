-- +goose Up
-- ADR-0021 Phase 2: nodes_history sidecar table.

CREATE TABLE nodes_history (
    history_id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_id                    UUID NOT NULL,
    valid_from                   TIMESTAMPTZ NOT NULL,
    valid_to                     TIMESTAMPTZ,
    change_type                  TEXT NOT NULL CHECK (change_type IN ('create', 'update', 'soft_delete', 'restore')),
    actor_id                     UUID,
    actor_kind                   TEXT CHECK (actor_kind IN ('user', 'token', 'system', 'collector')),

    -- Parent columns verbatim (as of migration 00035).
    cluster_id                   UUID NOT NULL,
    name                         TEXT NOT NULL,
    display_name                 TEXT,
    role                         TEXT,
    kubelet_version              TEXT,
    kube_proxy_version           TEXT,
    container_runtime_version    TEXT,
    os_image                     TEXT,
    operating_system             TEXT,
    kernel_version               TEXT,
    architecture                 TEXT,
    internal_ip                  TEXT,
    external_ip                  TEXT,
    pod_cidr                     TEXT,
    provider_id                  TEXT,
    instance_type                TEXT,
    zone                         TEXT,
    capacity_cpu                 TEXT,
    capacity_memory              TEXT,
    capacity_pods                TEXT,
    capacity_ephemeral_storage   TEXT,
    allocatable_cpu              TEXT,
    allocatable_memory           TEXT,
    allocatable_pods             TEXT,
    allocatable_ephemeral_storage TEXT,
    conditions                   JSONB NOT NULL DEFAULT '[]'::jsonb,
    taints                       JSONB NOT NULL DEFAULT '[]'::jsonb,
    unschedulable                BOOLEAN NOT NULL DEFAULT FALSE,
    ready                        BOOLEAN NOT NULL DEFAULT FALSE,
    labels                       JSONB NOT NULL DEFAULT '{}'::jsonb,
    owner                        TEXT,
    criticality                  TEXT,
    notes                        TEXT,
    runbook_url                  TEXT,
    annotations                  JSONB NOT NULL DEFAULT '{}'::jsonb,
    hardware_model               TEXT,
    terminated_at                TIMESTAMPTZ,
    created_at                   TIMESTAMPTZ NOT NULL,
    updated_at                   TIMESTAMPTZ NOT NULL
);

CREATE INDEX nodes_history_entity_idx
    ON nodes_history (entity_id, valid_from DESC);
CREATE INDEX nodes_history_current_idx
    ON nodes_history (entity_id) WHERE valid_to IS NULL;
CREATE INDEX nodes_history_retention_idx
    ON nodes_history (valid_to) WHERE valid_to IS NOT NULL;
CREATE UNIQUE INDEX nodes_history_create_idx
    ON nodes_history (entity_id) WHERE change_type = 'create';

-- +goose Down
DROP INDEX IF EXISTS nodes_history_create_idx;
DROP INDEX IF EXISTS nodes_history_retention_idx;
DROP INDEX IF EXISTS nodes_history_current_idx;
DROP INDEX IF EXISTS nodes_history_entity_idx;
DROP TABLE IF EXISTS nodes_history;
