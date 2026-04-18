-- +goose Up
CREATE TABLE workloads (
    id              UUID PRIMARY KEY,
    namespace_id    UUID NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,
    name            TEXT NOT NULL,
    replicas        INTEGER,
    ready_replicas  INTEGER,
    labels          JSONB NOT NULL DEFAULT '{}'::jsonb,
    spec            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (namespace_id, kind, name)
);

CREATE INDEX workloads_namespace_id_idx ON workloads (namespace_id);
CREATE INDEX workloads_kind_namespace_idx ON workloads (kind, namespace_id);
CREATE INDEX workloads_created_at_id_idx ON workloads (created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS workloads_created_at_id_idx;
DROP INDEX IF EXISTS workloads_kind_namespace_idx;
DROP INDEX IF EXISTS workloads_namespace_id_idx;
DROP TABLE IF EXISTS workloads;
