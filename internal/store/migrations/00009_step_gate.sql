-- +goose Up
ALTER TABLE steps ADD COLUMN gate_name TEXT NOT NULL DEFAULT '';
ALTER TABLE steps ADD COLUMN gate_source TEXT NOT NULL DEFAULT '';
ALTER TABLE steps ADD COLUMN gate_needs TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE steps DROP COLUMN gate_needs;
ALTER TABLE steps DROP COLUMN gate_source;
ALTER TABLE steps DROP COLUMN gate_name;
