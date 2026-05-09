-- +goose Up
CREATE TABLE image_versions_registries (
    hostname             TEXT PRIMARY KEY,
    rate_limit_per_sec   NUMERIC(6,2) NOT NULL CHECK (rate_limit_per_sec > 0),
    enabled              BOOLEAN NOT NULL DEFAULT TRUE,
    notes                TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO image_versions_registries (hostname, rate_limit_per_sec) VALUES
  ('docker.io',        1.0),
  ('ghcr.io',          5.0),
  ('quay.io',          5.0),
  ('gcr.io',           5.0),
  ('*-docker.pkg.dev', 5.0),
  ('registry.k8s.io',  5.0),
  ('public.ecr.aws',   5.0);

-- +goose Down
DROP TABLE IF EXISTS image_versions_registries;
