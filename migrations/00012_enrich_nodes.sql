-- +goose Up
-- Bring the Node row in line with Mercator's logical-server model plus
-- Kubernetes-specific context needed for incident response: role,
-- networking, cloud identity, the full OS stack, capacity + allocatable
-- resource pairs, conditions, taints, and the scheduling toggles.
--
-- All fields are collector-owned — the polling loop overwrites them every
-- tick. Mercator's curated `description`, `attributes`, `install_date`,
-- `patching_frequency` fields belong in the upcoming curated-metadata
-- table per ADR-0006 and are intentionally absent here.
--
-- Resource values are kept as TEXT since Kubernetes serialises them as
-- quantities ("16Gi", "4", "107951341314"); parsing to bytes is a reader
-- concern. conditions and taints are JSONB arrays of structured objects
-- so collectors don't have to collapse multi-dimensional state into
-- strings.
ALTER TABLE nodes
    ADD COLUMN role                         TEXT,
    ADD COLUMN kernel_version               TEXT,
    ADD COLUMN operating_system             TEXT,
    ADD COLUMN container_runtime_version    TEXT,
    ADD COLUMN kube_proxy_version           TEXT,
    ADD COLUMN internal_ip                  TEXT,
    ADD COLUMN external_ip                  TEXT,
    ADD COLUMN pod_cidr                     TEXT,
    ADD COLUMN provider_id                  TEXT,
    ADD COLUMN instance_type                TEXT,
    ADD COLUMN zone                         TEXT,
    ADD COLUMN capacity_cpu                 TEXT,
    ADD COLUMN capacity_memory              TEXT,
    ADD COLUMN capacity_pods                TEXT,
    ADD COLUMN capacity_ephemeral_storage   TEXT,
    ADD COLUMN allocatable_cpu              TEXT,
    ADD COLUMN allocatable_memory           TEXT,
    ADD COLUMN allocatable_pods             TEXT,
    ADD COLUMN allocatable_ephemeral_storage TEXT,
    ADD COLUMN conditions                   JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN taints                       JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN unschedulable                BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN ready                        BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE nodes
    DROP COLUMN IF EXISTS ready,
    DROP COLUMN IF EXISTS unschedulable,
    DROP COLUMN IF EXISTS taints,
    DROP COLUMN IF EXISTS conditions,
    DROP COLUMN IF EXISTS allocatable_ephemeral_storage,
    DROP COLUMN IF EXISTS allocatable_pods,
    DROP COLUMN IF EXISTS allocatable_memory,
    DROP COLUMN IF EXISTS allocatable_cpu,
    DROP COLUMN IF EXISTS capacity_ephemeral_storage,
    DROP COLUMN IF EXISTS capacity_pods,
    DROP COLUMN IF EXISTS capacity_memory,
    DROP COLUMN IF EXISTS capacity_cpu,
    DROP COLUMN IF EXISTS zone,
    DROP COLUMN IF EXISTS instance_type,
    DROP COLUMN IF EXISTS provider_id,
    DROP COLUMN IF EXISTS pod_cidr,
    DROP COLUMN IF EXISTS external_ip,
    DROP COLUMN IF EXISTS internal_ip,
    DROP COLUMN IF EXISTS kube_proxy_version,
    DROP COLUMN IF EXISTS container_runtime_version,
    DROP COLUMN IF EXISTS operating_system,
    DROP COLUMN IF EXISTS kernel_version,
    DROP COLUMN IF EXISTS role;
