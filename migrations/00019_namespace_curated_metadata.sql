-- +goose Up
-- Curated (human-owned) metadata on namespaces per ADR-0008, propagating
-- the pattern first established on clusters in migration 00018. The
-- collector's per-namespace upsert patches only the fields derived from
-- the Kubernetes API (name, phase, labels); UpdateNamespace's merge-patch
-- semantics leave unset fields alone, so these columns are naturally
-- collector-safe — no collector-side change required.
--
-- DICT (disponibilité / intégrité / confidentialité / traçabilité) is
-- intentionally NOT added here; those columns land in a separate
-- migration (00023) so the application-classification rollout is one
-- atomic schema change across Namespace + Workload.
ALTER TABLE namespaces
    ADD COLUMN owner       TEXT,
    ADD COLUMN criticality TEXT,
    ADD COLUMN notes       TEXT,
    ADD COLUMN runbook_url TEXT,
    ADD COLUMN annotations JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE namespaces
    DROP COLUMN IF EXISTS annotations,
    DROP COLUMN IF EXISTS runbook_url,
    DROP COLUMN IF EXISTS notes,
    DROP COLUMN IF EXISTS criticality,
    DROP COLUMN IF EXISTS owner;
