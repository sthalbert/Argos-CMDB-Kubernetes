-- +goose Up
-- PersistentVolumeClaim is namespace-scoped: it's the workload-side request
-- for storage, later bound to a PersistentVolume.
--
-- bound_volume_id is a nullable FK to persistent_volumes with ON DELETE SET
-- NULL. A Pending PVC hasn't bound yet; a PV deleted mid-tick shouldn't
-- cascade-delete the claim (the next collector pass reconciles state).
-- volume_name carries the raw PV name from spec.volumeName even when the
-- corresponding PV row hasn't been ingested yet — cheaper to read than the
-- FK when the reader only wants the string.
--
-- requested_storage stores the requests.storage value verbatim (e.g. "5Gi").
CREATE TABLE persistent_volume_claims (
    id                  UUID PRIMARY KEY,
    namespace_id        UUID NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    phase               TEXT,
    storage_class_name  TEXT,
    volume_name         TEXT,
    bound_volume_id     UUID REFERENCES persistent_volumes(id) ON DELETE SET NULL,
    access_modes        TEXT[] NOT NULL DEFAULT '{}',
    requested_storage   TEXT,
    labels              JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL,
    UNIQUE (namespace_id, name)
);

CREATE INDEX persistent_volume_claims_namespace_id_idx ON persistent_volume_claims (namespace_id);
CREATE INDEX persistent_volume_claims_bound_volume_id_idx ON persistent_volume_claims (bound_volume_id);
CREATE INDEX persistent_volume_claims_created_at_id_idx ON persistent_volume_claims (created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS persistent_volume_claims_created_at_id_idx;
DROP INDEX IF EXISTS persistent_volume_claims_bound_volume_id_idx;
DROP INDEX IF EXISTS persistent_volume_claims_namespace_id_idx;
DROP TABLE IF EXISTS persistent_volume_claims;
