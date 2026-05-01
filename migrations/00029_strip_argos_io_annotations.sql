-- +goose Up
-- +goose StatementBegin

-- Strip every legacy `argos.io/*` key from annotations JSONB columns.
-- The EOL enricher (next tick) repopulates the new `longue-vue.io/eol.*`
-- keys; user-curated keys like `argos.io/ignore` must be re-applied on the
-- source resources by operators per ADR-0020. This migration is irreversible
-- because the `argos.io/*` keys cannot be reconstructed from the surviving
-- columns.

UPDATE clusters
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE namespaces
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE nodes
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE virtual_machines
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE cloud_accounts
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Down is intentionally a no-op: stripped argos.io/* keys cannot be
-- reconstructed. To re-test the migration on a fresh DB, re-run the up.
SELECT 1;
-- +goose StatementEnd
