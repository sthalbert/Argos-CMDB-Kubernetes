-- +goose Up
-- Auth substrate per ADR-0007: four tables for human users, external
-- identities (OIDC), session cookies, and admin-issued machine tokens.
--
-- FK policy choices:
--   user_identities.user_id         ON DELETE CASCADE   — identities vanish with their user
--   sessions.user_id                ON DELETE CASCADE   — sessions vanish with their user
--   api_tokens.created_by_user_id   ON DELETE RESTRICT  — tokens survive
--     the user who minted them; an admin departure shouldn't silently
--     kill every CI pipeline. Revocation is an explicit action.
--
-- Passwords: argon2id, stored as the full PHC-encoded string (`$argon2id$v=19$...`)
-- so the parameters travel with the hash and can be bumped cluster-wide
-- without re-hashing on read.
--
-- Tokens: prefix column holds the first 8 characters of the plaintext for
-- O(1) lookup; the full plaintext's argon2id hash is the auth material.
-- Plaintext is shown once at creation time and never persisted.

CREATE TABLE users (
    id                    UUID PRIMARY KEY,
    username              TEXT NOT NULL,
    -- CITEXT would be cleaner but needs an extension; LOWER() on the
    -- unique index keeps everything in-schema and portable.
    password_hash         TEXT NOT NULL,
    role                  TEXT NOT NULL,
    must_change_password  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at            TIMESTAMPTZ NOT NULL,
    updated_at            TIMESTAMPTZ NOT NULL,
    last_login_at         TIMESTAMPTZ,
    disabled_at           TIMESTAMPTZ,
    CONSTRAINT users_role_check CHECK (role IN ('admin', 'editor', 'auditor', 'viewer'))
);

CREATE UNIQUE INDEX users_username_lower_uniq ON users (LOWER(username));
CREATE INDEX users_role_active_idx ON users (role) WHERE disabled_at IS NULL;

CREATE TABLE user_identities (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issuer     TEXT NOT NULL,
    subject    TEXT NOT NULL,
    email      TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    UNIQUE (issuer, subject)
);

CREATE INDEX user_identities_user_id_idx ON user_identities (user_id);

CREATE TABLE sessions (
    -- id is also the cookie value (32 random bytes, base64-url-encoded =
    -- 43 chars). Storing it directly saves a separate token column; the
    -- id itself is the secret.
    id            TEXT PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL,
    last_used_at  TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    user_agent    TEXT,
    source_ip     TEXT
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE api_tokens (
    id                    UUID PRIMARY KEY,
    name                  TEXT NOT NULL,
    -- prefix is the first 8 base62 chars of the plaintext, stored in the
    -- clear so middleware can SELECT the row in O(1); the full plaintext
    -- is the authentication material and only its argon2id hash persists.
    prefix                TEXT NOT NULL UNIQUE,
    hash                  TEXT NOT NULL,
    scopes                TEXT[] NOT NULL,
    created_by_user_id    UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at            TIMESTAMPTZ NOT NULL,
    last_used_at          TIMESTAMPTZ,
    expires_at            TIMESTAMPTZ,
    revoked_at            TIMESTAMPTZ,
    CONSTRAINT api_tokens_scopes_nonempty CHECK (cardinality(scopes) > 0)
);

CREATE INDEX api_tokens_active_idx ON api_tokens (prefix)
    WHERE revoked_at IS NULL;
CREATE INDEX api_tokens_created_by_idx ON api_tokens (created_by_user_id);

-- +goose Down
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS user_identities;
DROP TABLE IF EXISTS users;
