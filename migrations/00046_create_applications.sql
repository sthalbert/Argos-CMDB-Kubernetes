-- +goose Up
-- +goose StatementBegin
CREATE TABLE applications (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 TEXT NOT NULL UNIQUE,
    display_name         TEXT,
    description          TEXT,
    application_block_id UUID REFERENCES application_blocks(id) ON DELETE SET NULL,

    owner        TEXT,
    criticality  TEXT,
    notes        TEXT,
    runbook_url  TEXT,
    annotations  JSONB NOT NULL DEFAULT '{}'::jsonb,

    sec_disponibilite    SMALLINT CHECK (sec_disponibilite    BETWEEN 0 AND 4),
    sec_integrite        SMALLINT CHECK (sec_integrite        BETWEEN 0 AND 4),
    sec_confidentialite  SMALLINT CHECK (sec_confidentialite  BETWEEN 0 AND 4),
    sec_tracabilite      SMALLINT CHECK (sec_tracabilite      BETWEEN 0 AND 4),
    sec_notes            TEXT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX applications_block_idx      ON applications(application_block_id);
CREATE INDEX applications_name_lower_idx ON applications(LOWER(name));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS applications;
-- +goose StatementEnd
