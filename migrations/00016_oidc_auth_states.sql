-- +goose Up
-- Per-request auth state for the OIDC authorization-code flow (ADR-0007
-- follow-up). Each outbound redirect mints a random `state`, a PKCE
-- code_verifier, and a nonce, and stashes them here keyed on `state`.
-- The inbound callback consumes the row (select + delete in one tx),
-- verifies code_verifier + nonce against the IdP's response, and on
-- success mints a regular session.
--
-- One-shot: successful consume deletes the row. Expired rows are swept
-- on every read (WHERE expires_at > NOW()); a periodic janitor can be
-- added if volume ever justifies it.
CREATE TABLE oidc_auth_states (
    state         TEXT PRIMARY KEY,
    code_verifier TEXT NOT NULL,
    nonce         TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX oidc_auth_states_expires_idx ON oidc_auth_states (expires_at);

-- +goose Down
DROP TABLE IF EXISTS oidc_auth_states;
