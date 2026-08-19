-- +goose Up
-- +goose StatementBegin

-- cluster_policies: one row per collected Kyverno ClusterPolicy or namespaced Policy.
-- ClusterPolicy rows have namespace_id = NULL; Policy rows reference their namespace.
CREATE TABLE cluster_policies (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id          UUID NOT NULL REFERENCES clusters(id)       ON DELETE CASCADE,
    namespace_id        UUID          REFERENCES namespaces(id)     ON DELETE CASCADE,
    name                TEXT NOT NULL,
    resource_type       TEXT NOT NULL,
    scope               TEXT NOT NULL,
    description         TEXT,
    category            TEXT,
    severity            TEXT,
    action              TEXT,
    failure_policy      TEXT,
    background          BOOLEAN,
    rule_types          TEXT[],
    rules_count         INTEGER,
    target_resources    TEXT[],
    key_exclusions      TEXT[],
    ready               BOOLEAN,
    annotations         JSONB,
    spec_raw            JSONB NOT NULL,
    reconcile_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- COALESCE-based expression unique index so ON CONFLICT can use it as an
-- arbiter. A plain UNIQUE(cluster_id, namespace_id, name) fails for
-- cluster-scoped rows because NULL ≠ NULL in PostgreSQL. Partial indexes
-- (with WHERE) cannot serve as ON CONFLICT arbiters, but expression
-- indexes can — the COALESCE turns NULL into a sentinel UUID so the
-- index covers both scopes in one index and ON CONFLICT (cluster_id,
-- COALESCE(namespace_id, ...), name) matches the index definition.
CREATE UNIQUE INDEX uq_cluster_policies_scope
    ON cluster_policies (cluster_id, COALESCE(namespace_id, '00000000-0000-0000-0000-000000000000'), name);

CREATE INDEX cluster_policies_cluster_id_idx   ON cluster_policies(cluster_id);
CREATE INDEX cluster_policies_namespace_id_idx ON cluster_policies(namespace_id);
CREATE INDEX cluster_policies_resource_type_idx ON cluster_policies(resource_type);
CREATE INDEX cluster_policies_action_idx       ON cluster_policies(action);
CREATE INDEX cluster_policies_severity_idx     ON cluster_policies(severity);

-- policy_reports: one row per collected PolicyReport or ClusterPolicyReport.
CREATE TABLE policy_reports (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cluster_id          UUID NOT NULL REFERENCES clusters(id)       ON DELETE CASCADE,
    namespace_id        UUID          REFERENCES namespaces(id)     ON DELETE CASCADE,
    name                TEXT NOT NULL,
    scope_kind          TEXT,
    scope_name          TEXT,
    summary_pass        INTEGER NOT NULL DEFAULT 0,
    summary_fail        INTEGER NOT NULL DEFAULT 0,
    summary_warn        INTEGER NOT NULL DEFAULT 0,
    summary_error       INTEGER NOT NULL DEFAULT 0,
    summary_skip        INTEGER NOT NULL DEFAULT 0,
    results_raw         JSONB NOT NULL DEFAULT '[]'::jsonb,
    reconcile_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Same COALESCE pattern as cluster_policies: cluster-scoped reports have
-- namespace_id = NULL, which defeats a plain UNIQUE constraint.
CREATE UNIQUE INDEX uq_policy_reports_scope
    ON policy_reports (cluster_id, COALESCE(namespace_id, '00000000-0000-0000-0000-000000000000'), name);

CREATE INDEX policy_reports_cluster_id_idx   ON policy_reports(cluster_id);
CREATE INDEX policy_reports_namespace_id_idx ON policy_reports(namespace_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS policy_reports;
DROP TABLE IF EXISTS cluster_policies;
-- +goose StatementEnd
