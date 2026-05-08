-- +goose Up
ALTER TABLE settings
  ADD COLUMN image_versions_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE settings
  DROP COLUMN image_versions_enabled;
