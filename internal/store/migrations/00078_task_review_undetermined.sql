-- +goose Up
-- A review gate that ends with no holds or fails verdict is stored as undetermined, with the reason it ended so, and
-- the task moves on to its next gate. reason is that reason, and empty for a verdict that is holds or fails. SQLite
-- cannot change a CHECK in place, so epic_task_reviews is rebuilt with its rows copied. No trigger names it:
-- 00072_epic_revisions_table.sql dropped the triggers.
ALTER TABLE epic_task_reviews RENAME TO epic_task_reviews_before;
CREATE TABLE epic_task_reviews (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    attempt INTEGER NOT NULL,
    position INTEGER NOT NULL,
    gate TEXT NOT NULL,
    step TEXT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('holds', 'fails', 'undetermined')),
    reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (epic, task, attempt, position)
);
INSERT INTO epic_task_reviews (epic, task, attempt, position, gate, step, verdict)
SELECT epic, task, attempt, position, gate, step, verdict FROM epic_task_reviews_before;
DROP TABLE epic_task_reviews_before;

-- notes is the notes a task's build holds, as a JSON list: the outcomes of its red run and its reproduction, and a
-- judge's silence twice, which do not gate the build. The floor reviewer's prompt carries them.
ALTER TABLE epic_tasks ADD COLUMN notes TEXT NOT NULL DEFAULT '[]';

-- +goose Down
-- An undetermined review has a verdict the earlier CHECK refuses, so its row goes with the reason it stored. The
-- reviews that hold or fail keep their rows, and the table is rebuilt as it was.
DELETE FROM epic_task_reviews WHERE verdict = 'undetermined';
ALTER TABLE epic_tasks DROP COLUMN notes;

ALTER TABLE epic_task_reviews RENAME TO epic_task_reviews_undetermined;
CREATE TABLE epic_task_reviews (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    attempt INTEGER NOT NULL,
    position INTEGER NOT NULL,
    gate TEXT NOT NULL,
    step TEXT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('holds', 'fails')),
    PRIMARY KEY (epic, task, attempt, position)
);
INSERT INTO epic_task_reviews (epic, task, attempt, position, gate, step, verdict)
SELECT epic, task, attempt, position, gate, step, verdict FROM epic_task_reviews_undetermined;
DROP TABLE epic_task_reviews_undetermined;
