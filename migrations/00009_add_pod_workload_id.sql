-- +goose Up
-- workload_id links a Pod to its top-level controlling Workload (Deployment,
-- StatefulSet, DaemonSet). ON DELETE SET NULL so a deleted Workload doesn't
-- cascade-delete its pods — the pods simply become 'orphaned' and get reaped
-- by the normal pod-reconcile pass when the live cluster no longer lists them.
ALTER TABLE pods
    ADD COLUMN workload_id UUID REFERENCES workloads(id) ON DELETE SET NULL;

CREATE INDEX pods_workload_id_idx ON pods (workload_id);

-- +goose Down
DROP INDEX IF EXISTS pods_workload_id_idx;
ALTER TABLE pods DROP COLUMN IF EXISTS workload_id;
