-- +goose Up
ALTER TABLE epics ADD COLUMN branch TEXT NOT NULL DEFAULT '' CHECK (branch IN ('', 'creating', 'created'));

ALTER TABLE epic_tasks ADD COLUMN stage TEXT NOT NULL DEFAULT ''
    CHECK (stage IN ('', 'red', 'implement', 'green', 'review', 'verify'));
ALTER TABLE epic_tasks ADD COLUMN base TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_tasks ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_tasks ADD COLUMN rerun INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_tasks ADD COLUMN no_verdict INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_tasks ADD COLUMN held TEXT NOT NULL DEFAULT '' CHECK (held IN ('', 'stopped', 'parked'));
ALTER TABLE epic_tasks ADD COLUMN step TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_tasks ADD COLUMN red_run TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_tasks ADD COLUMN green_run TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_tasks ADD COLUMN revision TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_tasks ADD COLUMN failure TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_tasks ADD COLUMN review TEXT NOT NULL DEFAULT ''
    CHECK (review IN ('', 'unreviewed', 'reviewed'));
ALTER TABLE epic_tasks ADD COLUMN refs TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_tasks ADD COLUMN gate_pos INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_tasks ADD COLUMN finding_pos INTEGER NOT NULL DEFAULT 0;

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

CREATE TABLE epic_task_findings (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    attempt INTEGER NOT NULL,
    review INTEGER NOT NULL,
    position INTEGER NOT NULL,
    id TEXT NOT NULL,
    claim TEXT NOT NULL,
    citation TEXT NOT NULL,
    location TEXT NOT NULL,
    evidence TEXT NOT NULL,
    verify_by TEXT NOT NULL,
    step TEXT NOT NULL DEFAULT '',
    result TEXT NOT NULL DEFAULT '' CHECK (result IN ('', 'confirmed', 'dropped', 'undecided')),
    reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (epic, task, attempt, review, position)
);

CREATE TABLE epic_tells (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    text TEXT NOT NULL,
    at INTEGER NOT NULL,
    told INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, seq)
);

-- +goose Down
DROP TABLE epic_tells;
DROP TABLE epic_task_findings;
DROP TABLE epic_task_reviews;
ALTER TABLE epic_tasks DROP COLUMN finding_pos;
ALTER TABLE epic_tasks DROP COLUMN gate_pos;
ALTER TABLE epic_tasks DROP COLUMN refs;
ALTER TABLE epic_tasks DROP COLUMN review;
ALTER TABLE epic_tasks DROP COLUMN failure;
ALTER TABLE epic_tasks DROP COLUMN revision;
ALTER TABLE epic_tasks DROP COLUMN green_run;
ALTER TABLE epic_tasks DROP COLUMN red_run;
ALTER TABLE epic_tasks DROP COLUMN step;
ALTER TABLE epic_tasks DROP COLUMN held;
ALTER TABLE epic_tasks DROP COLUMN no_verdict;
ALTER TABLE epic_tasks DROP COLUMN rerun;
ALTER TABLE epic_tasks DROP COLUMN attempt;
ALTER TABLE epic_tasks DROP COLUMN base;
ALTER TABLE epic_tasks DROP COLUMN stage;
ALTER TABLE epics DROP COLUMN branch;
