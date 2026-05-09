-- +goose Up
-- AUTH-VULN-03 / docs/superpowers/specs/2026-05-09-per-account-login-lockout-design.md
--
-- Per-account login lockout. Two columns:
--   - failed_login_count: consecutive failures since last success.
--   - locked_at: timestamp the account auto-locked. NULL = not locked.

ALTER TABLE users
    ADD COLUMN failed_login_count INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN locked_at          TIMESTAMPTZ;

COMMENT ON COLUMN users.failed_login_count IS
    'Consecutive failed password verifications since last success. Reset to 0 on successful login or admin unlock.';

COMMENT ON COLUMN users.locked_at IS
    'When the account auto-locked after exceeding the failed-login threshold. NULL = not locked. Cleared by admin via PATCH /v1/admin/users/{id} unlock=true.';

-- +goose Down
ALTER TABLE users
    DROP COLUMN failed_login_count,
    DROP COLUMN locked_at;
