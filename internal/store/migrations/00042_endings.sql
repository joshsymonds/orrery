-- +goose Up
ALTER TABLE epics ADD COLUMN phase_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE epics DROP COLUMN phase_at;
