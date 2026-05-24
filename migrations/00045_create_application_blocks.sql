-- +goose Up
-- +goose StatementBegin
CREATE TABLE application_blocks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL UNIQUE,
    display_name TEXT,
    description  TEXT,
    owner        TEXT,
    notes        TEXT,
    annotations  JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX application_blocks_name_lower_idx ON application_blocks (LOWER(name));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS application_blocks;
-- +goose StatementEnd
