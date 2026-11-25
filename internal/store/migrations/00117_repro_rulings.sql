-- +goose Up
-- The rulings of the judges of a test item's reproduction that ran and exited 0 without timing out, at the requirement
-- level: an integrate run's reproduction and an evidence amendment's. One row per judge, keyed by the epic, the
-- reproduction's check id and the judge's number from 0. step is the judge's step, and '' for a judge that did not
-- start. verdict is holds when the judge ruled that the reproduction does not contradict red, fails when it ruled that
-- it does, and '' for no verdict. reason is the judge's reason, how its step ended, or why it did not start. Nothing
-- deletes a row, so each ruling stays as it was recorded: a repeat of a ruling writes nothing.
CREATE TABLE repro_rulings (
    epic TEXT NOT NULL,
    repro_check TEXT NOT NULL,
    judge INTEGER NOT NULL,
    step TEXT NOT NULL DEFAULT '',
    verdict TEXT NOT NULL DEFAULT '' CHECK (verdict IN ('', 'holds', 'fails')),
    reason TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, repro_check, judge)
);

-- +goose Down
DROP TABLE repro_rulings;
