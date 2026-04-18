-- +goose Up
CREATE TABLE pods (
    id            UUID PRIMARY KEY,
    namespace_id  UUID NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    phase         TEXT,
    node_name     TEXT,
    pod_ip        TEXT,
    labels        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    UNIQUE (namespace_id, name)
);

CREATE INDEX pods_namespace_id_idx ON pods (namespace_id);
CREATE INDEX pods_created_at_id_idx ON pods (created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS pods_created_at_id_idx;
DROP INDEX IF EXISTS pods_namespace_id_idx;
DROP TABLE IF EXISTS pods;
