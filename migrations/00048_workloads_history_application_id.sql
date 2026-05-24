-- +goose Up
-- ADR-0029 Phase 2: capture the curator-only workload→application link in
-- history. The column is intentionally a bare UUID with no FK to
-- applications(id) — history rows must outlive the parent application so
-- a "what was this workload linked to last Tuesday?" query still works
-- after the application is deleted (which sets workloads.application_id
-- to NULL via the parent FK, but does not retroactively rewrite history).
-- +goose StatementBegin
ALTER TABLE workloads_history
    ADD COLUMN application_id UUID;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE workloads_history
    DROP COLUMN IF EXISTS application_id;
-- +goose StatementEnd
