-- +goose Up
-- Append-only audit trail per the ADR-0007 "who did what" requirement.
-- Every state-changing API call (non-GET + all /v1/admin/* reads) emits
-- one row here with actor identity, target, verb, outcome, and source.
-- Rows are never updated in place; a retention job can purge by
-- occurred_at once an operator sets one.
--
-- Design notes:
--   * actor_id is nullable so pre-auth failures (bad creds, expired
--     cookie) can still be recorded without a users row to point at.
--   * actor_kind distinguishes human sessions from bearer tokens so
--     the UI can render them differently.
--   * resource_id is TEXT, not UUID — audit events cover heterogeneous
--     kinds (users, tokens, clusters, workloads...) and some have
--     non-UUID natural keys.
--   * details JSONB holds action-specific payload (e.g. which fields
--     changed on an update). Always an object or null; never a scalar.
CREATE TABLE audit_events (
    id             UUID PRIMARY KEY,
    occurred_at    TIMESTAMPTZ NOT NULL,
    actor_id       UUID,
    actor_kind     TEXT NOT NULL,
    actor_username TEXT,
    actor_role     TEXT,
    action         TEXT NOT NULL,
    resource_type  TEXT,
    resource_id    TEXT,
    http_method    TEXT NOT NULL,
    http_path      TEXT NOT NULL,
    http_status    INTEGER NOT NULL,
    source_ip      TEXT,
    user_agent     TEXT,
    details        JSONB,
    CONSTRAINT audit_events_actor_kind_check CHECK (
        actor_kind IN ('user', 'token', 'anonymous', 'system')
    )
);

-- Newest-first list scan is the default UI view.
CREATE INDEX audit_events_occurred_idx ON audit_events (occurred_at DESC);
-- "Show me everything user X touched" and "show me everything done to
-- resource Y" are the two filters the UI exposes.
CREATE INDEX audit_events_actor_idx ON audit_events (actor_id) WHERE actor_id IS NOT NULL;
CREATE INDEX audit_events_resource_idx ON audit_events (resource_type, resource_id)
    WHERE resource_type IS NOT NULL;

-- +goose Down
DROP TABLE IF EXISTS audit_events;
