-- +goose Up
CREATE TABLE services (
    id            UUID PRIMARY KEY,
    namespace_id  UUID NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    type          TEXT,
    cluster_ip    TEXT,
    selector      JSONB NOT NULL DEFAULT '{}'::jsonb,
    ports         JSONB NOT NULL DEFAULT '[]'::jsonb,
    labels        JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    UNIQUE (namespace_id, name)
);

CREATE INDEX services_namespace_id_idx ON services (namespace_id);
CREATE INDEX services_created_at_id_idx ON services (created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS services_created_at_id_idx;
DROP INDEX IF EXISTS services_namespace_id_idx;
DROP TABLE IF EXISTS services;
