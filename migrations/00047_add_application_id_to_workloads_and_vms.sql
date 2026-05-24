-- +goose Up
-- +goose StatementBegin
ALTER TABLE workloads
    ADD COLUMN application_id UUID REFERENCES applications(id) ON DELETE SET NULL;
CREATE INDEX workloads_application_id_idx ON workloads(application_id);

ALTER TABLE virtual_machines
    ADD COLUMN application_id UUID REFERENCES applications(id) ON DELETE SET NULL;
CREATE INDEX virtual_machines_application_id_idx ON virtual_machines(application_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE virtual_machines DROP COLUMN IF EXISTS application_id;
ALTER TABLE workloads        DROP COLUMN IF EXISTS application_id;
-- +goose StatementEnd
