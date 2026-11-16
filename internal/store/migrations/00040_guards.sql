-- +goose Up
ALTER TABLE epic_tasks ADD COLUMN guard TEXT NOT NULL DEFAULT '';

CREATE TABLE epic_task_test_changes (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    position INTEGER NOT NULL,
    path TEXT NOT NULL,
    PRIMARY KEY (epic, task, position)
);

-- +goose Down
DROP TABLE epic_task_test_changes;

ALTER TABLE epic_tasks DROP COLUMN guard;
