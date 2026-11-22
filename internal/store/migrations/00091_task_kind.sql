-- +goose Up
-- A task's kind: '' for an ordinary task, 'design' for a task that designs an interface its other tasks build against.
-- A task's revision link: the seq of the design task it revises, 0 for none. Each column is added at the end of
-- epic_tasks with a default, so every stored task reads back as an ordinary task that revises none.
ALTER TABLE epic_tasks ADD COLUMN kind TEXT NOT NULL DEFAULT '' CHECK (kind IN ('', 'design'));
ALTER TABLE epic_tasks ADD COLUMN revises INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE epic_tasks DROP COLUMN revises;
ALTER TABLE epic_tasks DROP COLUMN kind;
