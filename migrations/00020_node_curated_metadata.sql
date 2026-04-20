-- +goose Up
-- Curated (human-owned) metadata on nodes per ADR-0008, extending the
-- pattern established on clusters (00018) and namespaces (00019).
--
-- In addition to the five curated columns, nodes gain `hardware_model`
-- — a free-form TEXT field for bare-metal installs to record a server
-- model (e.g. "Dell PowerEdge R640") alongside the existing
-- cloud-shaped `instance_type` (which stays the well-known-label-derived
-- value populated by the collector on GKE / EKS / AKS). This closes the
-- "model" requirement of SNC §8.1.a for on-prem deployments.
--
-- The collector's UPSERT on nodes has an elaborate DO UPDATE SET clause
-- covering every Mercator-aligned field; neither the curated columns
-- nor `hardware_model` are listed there, so operator edits survive
-- every poll — verified by the PG integration test.
--
-- DICT (disponibilité / intégrité / confidentialité / traçabilité) is
-- NOT added to nodes. Per ADR-0008, DICT lives on the Application
-- abstraction (Namespace and Workload), not on infrastructure.
ALTER TABLE nodes
    ADD COLUMN owner          TEXT,
    ADD COLUMN criticality    TEXT,
    ADD COLUMN notes          TEXT,
    ADD COLUMN runbook_url    TEXT,
    ADD COLUMN annotations    JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN hardware_model TEXT;

-- +goose Down
ALTER TABLE nodes
    DROP COLUMN IF EXISTS hardware_model,
    DROP COLUMN IF EXISTS annotations,
    DROP COLUMN IF EXISTS runbook_url,
    DROP COLUMN IF EXISTS notes,
    DROP COLUMN IF EXISTS criticality,
    DROP COLUMN IF EXISTS owner;
