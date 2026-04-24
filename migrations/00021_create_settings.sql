-- +goose Up
-- Runtime settings table. Single-row design: one row with id=1 holds
-- all feature toggles as a flat set of columns. Created on first
-- migration; the enricher and admin API read/write it at runtime.
CREATE TABLE settings (
    id             INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    eol_enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the single row so SELECT never returns empty.
INSERT INTO settings (id) VALUES (1);

-- +goose Down
DROP TABLE IF EXISTS settings;
