-- +goose Up
-- The rounds an integrate run's integrator ran for a conflicting task, one row per round, keyed by the run's path and
-- attempt, the task's seq and the round's number. A round's verdict is merged, clash or ended, and its outcome is ''
-- until the round is composed or failed. head is the commit the round left the candidate on, explanation what the
-- integrator said, and failure why a failed round failed. Each text column is '' when the round had none. Nothing
-- deletes a row, so each round stays as it was recorded: a repeat of a round writes nothing.
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

-- +goose Down
DROP TABLE integrate_rounds;
