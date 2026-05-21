-- +goose Up
-- +goose StatementBegin
ALTER TABLE image_versions_registries
  ADD COLUMN path_prefix           TEXT    NOT NULL DEFAULT '',
  ADD COLUMN is_mirror             BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN auth_username         TEXT,
  ADD COLUMN auth_token_ciphertext BYTEA;

ALTER TABLE image_versions_registries DROP CONSTRAINT image_versions_registries_pkey;
ALTER TABLE image_versions_registries
  ADD PRIMARY KEY (hostname, path_prefix);

ALTER TABLE image_versions_registries
  ADD CONSTRAINT image_versions_registries_token_requires_username
    CHECK (auth_token_ciphertext IS NULL OR auth_username IS NOT NULL);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE image_versions_registries
  DROP CONSTRAINT image_versions_registries_token_requires_username;
ALTER TABLE image_versions_registries DROP CONSTRAINT image_versions_registries_pkey;
ALTER TABLE image_versions_registries ADD PRIMARY KEY (hostname);
ALTER TABLE image_versions_registries
  DROP COLUMN auth_token_ciphertext,
  DROP COLUMN auth_username,
  DROP COLUMN is_mirror,
  DROP COLUMN path_prefix;
-- +goose StatementEnd
