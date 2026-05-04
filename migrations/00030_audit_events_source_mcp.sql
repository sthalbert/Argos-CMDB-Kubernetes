-- +goose Up
-- ADR-0014 hardening: the MCP server now emits audit_events rows with
-- source='mcp' for every tool call. Extend the source CHECK constraint to
-- accept the new label; existing 'api' / 'ingest_gw' / 'system' values are
-- preserved.
ALTER TABLE audit_events DROP CONSTRAINT IF EXISTS audit_events_source_check;
ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_source_check
    CHECK (source IN ('api', 'ingest_gw', 'system', 'mcp'));

-- +goose Down
ALTER TABLE audit_events DROP CONSTRAINT IF EXISTS audit_events_source_check;
ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_source_check
    CHECK (source IN ('api', 'ingest_gw', 'system'));
