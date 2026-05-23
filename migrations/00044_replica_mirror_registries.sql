-- +goose Up
-- +goose StatementBegin
ALTER TABLE image_versions_registries
  ADD COLUMN replicates_from_hostname TEXT;

ALTER TABLE image_versions_registries
  ADD CONSTRAINT image_versions_registries_replica_is_mirror
    CHECK (replicates_from_hostname IS NULL OR is_mirror = TRUE);

ALTER TABLE image_versions_registries
  ADD CONSTRAINT image_versions_registries_replica_not_self
    CHECK (replicates_from_hostname IS NULL
        OR replicates_from_hostname <> hostname);

CREATE TABLE image_origin_resolutions (
  mirror_image_repo TEXT        NOT NULL,
  variant           TEXT        NOT NULL,
  origin_image_repo TEXT,
  via_hostname      TEXT,
  resolved_at       TIMESTAMPTZ NOT NULL,
  last_error        TEXT,
  last_error_at     TIMESTAMPTZ,
  PRIMARY KEY (mirror_image_repo, variant),
  CONSTRAINT image_origin_resolutions_origin_via_pair
    CHECK ((origin_image_repo IS NULL) = (via_hostname IS NULL))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS image_origin_resolutions;

ALTER TABLE image_versions_registries
  DROP CONSTRAINT IF EXISTS image_versions_registries_replica_not_self;
ALTER TABLE image_versions_registries
  DROP CONSTRAINT IF EXISTS image_versions_registries_replica_is_mirror;
ALTER TABLE image_versions_registries
  DROP COLUMN IF EXISTS replicates_from_hostname;
-- +goose StatementEnd
