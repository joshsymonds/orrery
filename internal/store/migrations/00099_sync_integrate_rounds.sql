-- +goose Up
-- A round's outcome gains passed and skipped, and its verdict gains blocked, for the rounds a sync's integrator runs: a
-- round passes when its commit passes the checks, is skipped when its step ended engine_restarted, and is blocked when its
-- integrator stopped on a path. SQLite cannot change a column's CHECK in place, so integrate_rounds is rebuilt with its
-- columns in the same order, and every row is copied with all its columns. No index, trigger, view or foreign key names
-- integrate_rounds.
ALTER TABLE integrate_rounds RENAME TO integrate_rounds_before;
CREATE TABLE integrate_rounds (
    epic TEXT NOT NULL,
    path TEXT NOT NULL,
    attempt INTEGER NOT NULL,
    task INTEGER NOT NULL,
    round INTEGER NOT NULL,
    step TEXT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('merged', 'clash', 'ended', 'blocked')),
    head TEXT NOT NULL DEFAULT '',
    explanation TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '' CHECK (outcome IN ('', 'composed', 'failed', 'passed', 'skipped')),
    failure TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, path, attempt, task, round)
);
INSERT INTO integrate_rounds (
    epic, path, attempt, task, round, step, verdict, head, explanation, outcome, failure, created
)
SELECT epic, path, attempt, task, round, step, verdict, head, explanation, outcome, failure, created
FROM integrate_rounds_before;
DROP TABLE integrate_rounds_before;

-- +goose Down
-- The rounds a sync's integrator ran that the checks before this migration do not hold, which are those that passed,
-- were skipped or were blocked, are dropped, and the table is rebuilt with the checks it had.
DELETE FROM integrate_rounds WHERE outcome IN ('passed', 'skipped') OR verdict = 'blocked';
ALTER TABLE integrate_rounds RENAME TO integrate_rounds_sync;
CREATE TABLE integrate_rounds (
    epic TEXT NOT NULL,
    path TEXT NOT NULL,
    attempt INTEGER NOT NULL,
    task INTEGER NOT NULL,
    round INTEGER NOT NULL,
    step TEXT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('merged', 'clash', 'ended')),
    head TEXT NOT NULL DEFAULT '',
    explanation TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '' CHECK (outcome IN ('', 'composed', 'failed')),
    failure TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, path, attempt, task, round)
);
INSERT INTO integrate_rounds (
    epic, path, attempt, task, round, step, verdict, head, explanation, outcome, failure, created
)
SELECT epic, path, attempt, task, round, step, verdict, head, explanation, outcome, failure, created
FROM integrate_rounds_sync;
DROP TABLE integrate_rounds_sync;
