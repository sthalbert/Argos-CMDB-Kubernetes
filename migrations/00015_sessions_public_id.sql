-- +goose Up
-- Add a server-generated UUID to sessions so admins can address a row
-- without ever seeing the cookie value. The cookie value stays as the
-- primary key (used by the middleware for lookup); the new `public_id`
-- is what the admin-facing list / revoke endpoints expose.
--
-- Matters for security: if an admin (or a database reader) could see
-- another user's cookie value, they could impersonate that user. The
-- public_id has no authentication weight — it's purely a handle.
ALTER TABLE sessions
    ADD COLUMN public_id UUID;

-- Backfill existing rows (none in production yet — this is the first
-- post-ADR-0007 migration — but be safe).
UPDATE sessions SET public_id = gen_random_uuid() WHERE public_id IS NULL;

ALTER TABLE sessions
    ALTER COLUMN public_id SET NOT NULL,
    ADD CONSTRAINT sessions_public_id_uniq UNIQUE (public_id);

-- +goose Down
ALTER TABLE sessions
    DROP CONSTRAINT IF EXISTS sessions_public_id_uniq,
    DROP COLUMN IF EXISTS public_id;
