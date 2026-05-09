-- +goose Up
ALTER TABLE settings
  ADD COLUMN IF NOT EXISTS image_versions_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE settings
  DROP COLUMN IF EXISTS image_versions_enabled;
