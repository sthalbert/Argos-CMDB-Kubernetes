-- +goose Up
-- +goose StatementBegin
ALTER TABLE settings
    ADD COLUMN IF NOT EXISTS policies_enabled BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE settings DROP COLUMN IF EXISTS policies_enabled;
-- +goose StatementEnd
