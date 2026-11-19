-- +goose Up
-- The rungs of each epic's task ladder, one row each, in the order the task climbed them. A failing task climbs a
-- ladder instead of being replaced, and a rung is the task build's Attempt number: rungs 1 and 2 are kind attempt, 3 is
-- rescue, 4 is orchestrator, 5 and 6 are approach, and 7 is gap. A rung's row is written once and never changed, and
-- history only appends: a rung is added and none is removed.
--
-- epic is the epic's id, task the seq of the task the rung belongs to, and rung the rung's number. kind is the kind of
-- the rung, one of the kinds the rung numbers above name. moved_by is what kind of failure moved the task onto the rung:
-- unit, finding, fail_decision, cannot_meet or fail_task. cause is the failure's text, step the step the failure came
-- from, and moved_by, cause and step are all '' for rung 1, which no failure moved. gate and assertion are the review
-- gate and the claim of the confirmed finding that moved the task, and are '' when no finding did. created is when the
-- rung was written.
CREATE TABLE epic_task_rungs (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    rung INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('attempt', 'rescue', 'orchestrator', 'approach', 'gap')),
    moved_by TEXT NOT NULL DEFAULT '' CHECK (moved_by IN ('', 'unit', 'finding', 'fail_decision', 'cannot_meet', 'fail_task')),
    cause TEXT NOT NULL DEFAULT '',
    gate TEXT NOT NULL DEFAULT '',
    assertion TEXT NOT NULL DEFAULT '',
    step TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, task, rung)
);

-- The outcome of a rung: one row for a rung that has ended, and none for a rung still open. The primary key is the
-- rung's own, so a rung's outcome is written once, in this row. outcome is how the rung ended: failed, accepted or gap.
-- reason is why, '' when none was given, and ended is when the outcome was written.
CREATE TABLE epic_task_rung_outcomes (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    rung INTEGER NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('failed', 'accepted', 'gap')),
    reason TEXT NOT NULL DEFAULT '',
    ended INTEGER NOT NULL,
    PRIMARY KEY (epic, task, rung)
);

-- +goose Down
DROP TABLE epic_task_rung_outcomes;
DROP TABLE epic_task_rungs;
