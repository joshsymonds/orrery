-- +goose Up
ALTER TABLE epics ADD COLUMN summary TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE epics DROP COLUMN summary;
