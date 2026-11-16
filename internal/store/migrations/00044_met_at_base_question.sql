-- +goose Up
-- The seq of the owner's decision that dropped a Requirement as already met at the epic's base, 0 while it is not dropped.
ALTER TABLE epic_requirements ADD COLUMN dropped_by INTEGER NOT NULL DEFAULT 0;

-- What the park question of an evidence run that judged Requirements holding at base needs when it is answered: the
-- run's path and attempt, and the Requirements the run found met, as a JSON array of ids.
CREATE TABLE epic_met_at_base (
    epic TEXT NOT NULL,
    question INTEGER NOT NULL,
    path TEXT NOT NULL,
    attempt INTEGER NOT NULL,
    requirements TEXT NOT NULL,
    PRIMARY KEY (epic, question)
);

-- +goose Down
DROP TABLE epic_met_at_base;
ALTER TABLE epic_requirements DROP COLUMN dropped_by;
