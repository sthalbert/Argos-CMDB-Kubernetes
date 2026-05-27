-- +goose Up
-- +goose StatementBegin
CREATE TABLE image_origin_mappings (
  image_name      TEXT PRIMARY KEY,
  public_registry TEXT NOT NULL,
  notes           TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_by      TEXT,
  updated_by      TEXT,
  CONSTRAINT image_origin_mappings_image_name_shape
    CHECK (image_name <> ''
           AND image_name NOT LIKE '%://%'
           AND image_name NOT LIKE '% %'
           AND position(':' IN image_name) = 0
           AND position('@' IN image_name) = 0
           AND length(image_name) <= 512),
  CONSTRAINT image_origin_mappings_public_registry_shape
    CHECK (public_registry <> ''
           AND position('/' IN public_registry) = 0
           AND length(public_registry) <= 253)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS image_origin_mappings;
-- +goose StatementEnd
