-- +goose Up
CREATE TABLE image_versions (
    image_repo       TEXT NOT NULL,
    variant          TEXT NOT NULL DEFAULT '',
    registry         TEXT NOT NULL,
    latest_tag       TEXT,
    annotation       JSONB NOT NULL,
    source           TEXT NOT NULL,
    last_checked_at  TIMESTAMPTZ NOT NULL,
    last_error       TEXT,
    last_error_at    TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (image_repo, variant)
);

CREATE INDEX idx_image_versions_registry ON image_versions(registry);
CREATE INDEX idx_image_versions_last_checked ON image_versions(last_checked_at);

-- +goose Down
DROP TABLE image_versions;
