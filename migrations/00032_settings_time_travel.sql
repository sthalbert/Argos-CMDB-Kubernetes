-- +goose Up
-- ADR-0021 Phase 2: add time-travel feature flags to the settings row.

ALTER TABLE settings
    ADD COLUMN IF NOT EXISTS time_travel_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS time_travel_retention_days INTEGER NOT NULL DEFAULT 365,
    ADD COLUMN IF NOT EXISTS time_travel_reaper_enabled BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose Down
ALTER TABLE settings
    DROP COLUMN IF EXISTS time_travel_reaper_enabled,
    DROP COLUMN IF EXISTS time_travel_retention_days,
    DROP COLUMN IF EXISTS time_travel_enabled;
