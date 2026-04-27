-- +goose Up
-- The Outscale (and most cloud-provider) ReadVms response carries the
-- AMI id only — e.g. "ami-75374985". Operators want to see the human
-- name ("ubuntu-22.04-2024-09", "rocky-9.3-2024-08") in the inventory
-- without having to hit the cloud-provider console. The collector
-- resolves names via a batch ReadImages call once per tick and writes
-- the result here. NULL when the AMI was deleted or unreachable.

ALTER TABLE virtual_machines ADD COLUMN image_name TEXT;

-- +goose Down
ALTER TABLE virtual_machines DROP COLUMN IF EXISTS image_name;
