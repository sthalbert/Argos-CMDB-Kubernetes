-- +goose Up
-- PersistentVolume is cluster-scoped in Kubernetes: one PV is the logical
-- handle for a chunk of backing storage the cluster can hand out to claims.
-- Capacity is stored as TEXT (e.g. "10Gi") since that's how the Kubernetes
-- API expresses it; parsing to bytes is a caller concern.
--
-- access_modes is a string array (ReadWriteOnce / ReadOnlyMany /
-- ReadWriteMany / ReadWriteOncePod) — a single PV commonly exposes several.
--
-- csi_driver / volume_handle surface the concrete backing storage identity
-- when present (CSI-backed PVs); legacy in-tree drivers leave those null.
--
-- claim_ref_namespace / claim_ref_name mirror the PV's spec.claimRef so we
-- can reconstruct the PV -> PVC relationship even when the claim row hasn't
-- been ingested yet (listing happens per-tick).
CREATE TABLE persistent_volumes (
    id                    UUID PRIMARY KEY,
    cluster_id            UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    name                  TEXT NOT NULL,
    capacity              TEXT,
    access_modes          TEXT[] NOT NULL DEFAULT '{}',
    reclaim_policy        TEXT,
    phase                 TEXT,
    storage_class_name    TEXT,
    csi_driver            TEXT,
    volume_handle         TEXT,
    claim_ref_namespace   TEXT,
    claim_ref_name        TEXT,
    labels                JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at            TIMESTAMPTZ NOT NULL,
    updated_at            TIMESTAMPTZ NOT NULL,
    UNIQUE (cluster_id, name)
);

CREATE INDEX persistent_volumes_cluster_id_idx ON persistent_volumes (cluster_id);
CREATE INDEX persistent_volumes_created_at_id_idx ON persistent_volumes (created_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS persistent_volumes_created_at_id_idx;
DROP INDEX IF EXISTS persistent_volumes_cluster_id_idx;
DROP TABLE IF EXISTS persistent_volumes;
