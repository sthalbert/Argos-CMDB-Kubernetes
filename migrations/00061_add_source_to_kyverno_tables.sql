-- +goose Up
-- +goose StatementBegin

-- Add source discriminator: 'collector' (default, set by in-process K8s
-- pull) vs 'api' (set by POST /v1/cluster-policies and
-- POST /v1/policy-reports). The collector sweep
-- (DeleteClusterScopedPoliciesNotIn /
-- DeleteClusterPoliciesByNamespace / DeleteClusterScopedPolicyReportsNotIn /
-- DeletePolicyReportsByNamespace) only deletes rows where
-- source = 'collector', so API-created rows survive reconcile ticks.
-- ADR-0043 §POS-012.

ALTER TABLE cluster_policies ADD COLUMN source TEXT NOT NULL DEFAULT 'collector'
    CHECK (source IN ('collector', 'api'));
ALTER TABLE policy_reports  ADD COLUMN source TEXT NOT NULL DEFAULT 'collector'
    CHECK (source IN ('collector', 'api'));

-- Partial index so the sweep query (WHERE cluster_id=$1 AND source='collector')
-- only scans collector rows.
CREATE INDEX cluster_policies_sweep_idx
    ON cluster_policies(cluster_id) WHERE source = 'collector';
CREATE INDEX policy_reports_sweep_idx
    ON policy_reports(cluster_id) WHERE source = 'collector';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS policy_reports_sweep_idx;
DROP INDEX IF EXISTS cluster_policies_sweep_idx;
ALTER TABLE policy_reports  DROP COLUMN source;
ALTER TABLE cluster_policies DROP COLUMN source;
-- +goose StatementEnd
