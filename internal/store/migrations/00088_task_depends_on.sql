-- +goose Up
-- A task's dependencies: the seqs of the tasks it depends on, as decimals joined with commas in the order the planner
-- gave them, and '' for a task with none, the encoding after_tasks uses. A rung moved by a dependency records the move
-- as 'dependency' in its moved_by, so epic_task_rungs is rebuilt with that value in its CHECK. Each of its columns and
-- its key are kept as they were, and its rows are copied as they are: a rung keeps its generation. No index, trigger
-- or foreign key names the table, since 00072_epic_revisions_table.sql dropped its triggers.
ALTER TABLE epic_tasks ADD COLUMN depends_on TEXT NOT NULL DEFAULT '';

ALTER TABLE epic_task_rungs RENAME TO epic_task_rungs_before;
CREATE TABLE epic_task_rungs (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    generation INTEGER NOT NULL,
    rung INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('attempt', 'rescue', 'orchestrator', 'approach', 'gap')),
    moved_by TEXT NOT NULL DEFAULT ''
        CHECK (moved_by IN ('', 'unit', 'finding', 'fail_decision', 'cannot_meet', 'fail_task', 'send_back',
            'dependency')),
    cause TEXT NOT NULL DEFAULT '',
    gate TEXT NOT NULL DEFAULT '',
    assertion TEXT NOT NULL DEFAULT '',
    step TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, task, generation, rung)
);
INSERT INTO epic_task_rungs (epic, task, generation, rung, kind, moved_by, cause, gate, assertion, step, created)
SELECT epic, task, generation, rung, kind, moved_by, cause, gate, assertion, step, created
FROM epic_task_rungs_before;
DROP TABLE epic_task_rungs_before;

-- +goose Down
-- The column is dropped, and epic_task_rungs is rebuilt with the moved_by CHECK 00087_task_generation.sql gave it,
-- which has no 'dependency'. A rung a dependency moved is kept as a rung a failed task moved, 'fail_task'.
ALTER TABLE epic_tasks DROP COLUMN depends_on;

ALTER TABLE epic_task_rungs RENAME TO epic_task_rungs_dependency;
CREATE TABLE epic_task_rungs (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    generation INTEGER NOT NULL,
    rung INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('attempt', 'rescue', 'orchestrator', 'approach', 'gap')),
    moved_by TEXT NOT NULL DEFAULT ''
        CHECK (moved_by IN ('', 'unit', 'finding', 'fail_decision', 'cannot_meet', 'fail_task', 'send_back')),
    cause TEXT NOT NULL DEFAULT '',
    gate TEXT NOT NULL DEFAULT '',
    assertion TEXT NOT NULL DEFAULT '',
    step TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, task, generation, rung)
);
INSERT INTO epic_task_rungs (epic, task, generation, rung, kind, moved_by, cause, gate, assertion, step, created)
SELECT epic, task, generation, rung, kind, CASE WHEN moved_by = 'dependency' THEN 'fail_task' ELSE moved_by END,
    cause, gate, assertion, step, created
FROM epic_task_rungs_dependency;
DROP TABLE epic_task_rungs_dependency;
