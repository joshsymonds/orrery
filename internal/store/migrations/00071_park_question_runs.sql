-- +goose Up
-- The parked node run that a park question of an epic names, for each park question an owner answers that a node run's
-- park opened: the question's seq and the path of the run. Answering such a question with retry reruns that run at its
-- next attempt, and RetryEpic reruns the run of each open question that has a row, as it reruns the Met at base run of
-- 00044_met_at_base_question.sql. A row is written with its question and never changed or removed.
CREATE TABLE epic_park_question_runs (
    epic TEXT NOT NULL,
    question INTEGER NOT NULL,
    path TEXT NOT NULL,
    PRIMARY KEY (epic, question)
);

-- +goose StatementBegin
CREATE TRIGGER epic_park_question_runs_revision_insert AFTER INSERT ON epic_park_question_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = NEW.epic;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_park_question_runs_revision_update AFTER UPDATE ON epic_park_question_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id IN (OLD.epic, NEW.epic);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER epic_park_question_runs_revision_delete AFTER DELETE ON epic_park_question_runs
BEGIN
    UPDATE epics SET revision = revision + 1 WHERE id = OLD.epic;
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER epic_park_question_runs_revision_delete;
DROP TRIGGER epic_park_question_runs_revision_update;
DROP TRIGGER epic_park_question_runs_revision_insert;
DROP TABLE epic_park_question_runs;
