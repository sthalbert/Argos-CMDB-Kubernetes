-- +goose Up
-- Curated (human-owned) metadata on clusters per the ADR-0006 follow-up.
-- The collector never writes these columns — they belong to whoever
-- registered the cluster and the editors who annotate it afterwards.
--
-- `environment`, `region`, `provider` were already user-curated even
-- before this migration (the collector does not derive them from the
-- Kubernetes API), so they stay where they are. This migration adds
-- the fields an operator actually needs to triage an incident:
--   * owner        — team / on-call handle responsible for the cluster
--   * criticality  — free-text tier ("critical", "high", …)
--   * notes        — long-form prose
--   * runbook_url  — link to the team's runbook
--   * annotations  — free-form k/v for anything not worth a column
--
-- UpdateCluster's merge-patch semantics already leave unset fields
-- alone, so the collector's KubernetesVersion-only patch doesn't clobber
-- these — no collector-side change required.
ALTER TABLE clusters
    ADD COLUMN owner       TEXT,
    ADD COLUMN criticality TEXT,
    ADD COLUMN notes       TEXT,
    ADD COLUMN runbook_url TEXT,
    ADD COLUMN annotations JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE clusters
    DROP COLUMN IF EXISTS annotations,
    DROP COLUMN IF EXISTS runbook_url,
    DROP COLUMN IF EXISTS notes,
    DROP COLUMN IF EXISTS criticality,
    DROP COLUMN IF EXISTS owner;
