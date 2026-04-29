-- +goose Up
-- ADR-0019: operators record what's running on each platform VM (Vault,
-- DNS, Cyberwatch, …) so the EOL enricher can scan declared product
-- versions and so the list view can filter by application.
--
-- applications is a JSONB array of objects:
--   [{
--     "product":  "vault",                   -- normalized: lower-kebab
--     "version":  "1.15.4",
--     "name":     "vault-prod-eu",           -- optional, operator label
--     "notes":    "...",                     -- optional
--     "added_at": "2026-04-29T10:00:00Z",    -- server-stamped
--     "added_by": "alice@example.com"        -- server-stamped (caller email/login or token name)
--   }, ...]
--
-- The GIN jsonb_path_ops index supports `applications @> '[{"product":"vault"}]'`
-- containment queries used by the list filter; jsonb_path_ops is ~2× smaller
-- and faster for `@>` than the default ops class.
--
-- The two btree indexes accelerate the new list filters:
--   - LOWER(name) for case-insensitive name substring search
--   - image_id for exact image filtering (substring search falls back to seq scan)

ALTER TABLE virtual_machines
    ADD COLUMN applications JSONB NOT NULL DEFAULT '[]'::jsonb;

CREATE INDEX virtual_machines_applications_gin_idx
    ON virtual_machines USING GIN (applications jsonb_path_ops);

CREATE INDEX virtual_machines_name_lower_idx
    ON virtual_machines (LOWER(name));

CREATE INDEX virtual_machines_image_id_idx
    ON virtual_machines (image_id);

-- +goose Down
DROP INDEX IF EXISTS virtual_machines_image_id_idx;
DROP INDEX IF EXISTS virtual_machines_name_lower_idx;
DROP INDEX IF EXISTS virtual_machines_applications_gin_idx;
ALTER TABLE virtual_machines DROP COLUMN IF EXISTS applications;
