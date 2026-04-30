-- +goose Up
-- Cloud accounts (ADR-0015) — one row per (cloud provider, account credentials).
-- Stores AK plaintext (a public identifier) and SK ciphertext (AES-256-GCM with
-- master key from LONGUE_VUE_SECRETS_MASTER_KEY, AAD bound to the row id).
--
-- Status workflow:
--   pending_credentials → admin has not yet supplied AK/SK
--   active              → credentials present; collector ticking successfully
--   error               → collector reported an error on its last tick
--   disabled            → admin disabled the account; credentials fetch returns 403
--
-- Name is operator-editable. FKs on virtual_machines and api_tokens key on id,
-- so renaming an account never breaks references.

CREATE TABLE cloud_accounts (
    id                   UUID PRIMARY KEY,
    provider             TEXT NOT NULL,
    name                 TEXT NOT NULL,
    region               TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'pending_credentials',
    -- credentials: NULL until admin sets them; AK is plaintext (public ID),
    -- SK is encrypted with AES-256-GCM, AAD = id.
    access_key           TEXT,
    secret_key_encrypted BYTEA,
    secret_key_nonce     BYTEA,
    secret_key_kid       TEXT,
    -- collector heartbeat
    last_seen_at         TIMESTAMPTZ,
    last_error           TEXT,
    last_error_at        TIMESTAMPTZ,
    -- curated metadata
    owner                TEXT,
    criticality          TEXT,
    notes                TEXT,
    runbook_url          TEXT,
    annotations          JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- lifecycle
    created_at           TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL,
    disabled_at          TIMESTAMPTZ,
    CONSTRAINT cloud_accounts_status_check CHECK (
        status IN ('pending_credentials', 'active', 'error', 'disabled')
    ),
    CONSTRAINT cloud_accounts_provider_nonempty CHECK (length(provider) > 0),
    CONSTRAINT cloud_accounts_name_nonempty CHECK (length(name) > 0),
    UNIQUE (provider, name)
);

CREATE INDEX cloud_accounts_status_idx ON cloud_accounts (status);
CREATE INDEX cloud_accounts_provider_idx ON cloud_accounts (provider);

-- +goose Down
DROP INDEX IF EXISTS cloud_accounts_provider_idx;
DROP INDEX IF EXISTS cloud_accounts_status_idx;
DROP TABLE IF EXISTS cloud_accounts;
