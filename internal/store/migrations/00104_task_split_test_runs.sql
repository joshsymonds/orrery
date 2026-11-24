-- +goose Up
-- A split task is one the planner splits into descendants: split_from is the seq of the task a task was split from, 0 for
-- none, so a descendant names its origin. A split action keeps its descendants as JSON in split_tasks, the same way the
-- action keeps its files, so the action reads back as it was returned. The default leaves every existing row as it was.
ALTER TABLE epic_tasks ADD COLUMN split_from INTEGER NOT NULL DEFAULT 0;
ALTER TABLE planner_actions ADD COLUMN split_tasks TEXT NOT NULL DEFAULT '[]';

-- A test run is one run of a task's test command, keyed like epic_task_findings: the attempt and review it belongs to,
-- and its position among them. Its generation is the task's reopen generation when it was written, as findings' is, so
-- a send_back leaves the runs of earlier generations stored under theirs. command and revision are what the run was
-- given. step and output are its result: a run whose step is '' has no result yet.
CREATE TABLE epic_task_test_runs (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    generation INTEGER NOT NULL,
    attempt INTEGER NOT NULL,
    review INTEGER NOT NULL,
    position INTEGER NOT NULL,
    command TEXT NOT NULL,
    revision TEXT NOT NULL,
    step TEXT NOT NULL DEFAULT '',
    output TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (epic, task, generation, attempt, review, position)
);

-- +goose Down
-- The table and both columns go, with the runs and split links they held. Nothing else reads them.
DROP TABLE epic_task_test_runs;
ALTER TABLE planner_actions DROP COLUMN split_tasks;
ALTER TABLE epic_tasks DROP COLUMN split_from;
