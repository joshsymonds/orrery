-- +goose Up
ALTER TABLE epics ADD COLUMN stored_base TEXT NOT NULL DEFAULT '';
UPDATE epics SET stored_base = base;
ALTER TABLE epics ADD COLUMN candidate TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_tasks ADD COLUMN batch TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE epic_tasks DROP COLUMN batch;
ALTER TABLE epics DROP COLUMN candidate;
ALTER TABLE epics DROP COLUMN stored_base;
