-- +goose Up
-- Staleness threshold in days for the derived cluster `stale` status.
-- 0 disables the feature. Seeded from LONGUE_VUE_CLUSTER_STALE_AFTER_DAYS,
-- hot-editable via PATCH /v1/admin/settings.
ALTER TABLE settings
  ADD COLUMN IF NOT EXISTS cluster_stale_after_days INTEGER NOT NULL DEFAULT 7;

-- +goose Down
ALTER TABLE settings DROP COLUMN IF EXISTS cluster_stale_after_days;
