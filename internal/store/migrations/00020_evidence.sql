-- +goose Up
ALTER TABLE epics ADD COLUMN base TEXT NOT NULL DEFAULT '';

CREATE TABLE epic_gates (
    epic TEXT NOT NULL,
    position INTEGER NOT NULL,
    name TEXT NOT NULL,
    argv TEXT NOT NULL,
    limit_seconds INTEGER NOT NULL,
    needs TEXT NOT NULL,
    agent TEXT NOT NULL,
    must TEXT NOT NULL,
    must_not TEXT NOT NULL,
    PRIMARY KEY (epic, position)
);

CREATE TABLE epic_gate_lists (
    epic TEXT NOT NULL,
    list TEXT NOT NULL CHECK (list IN ('done', 'review')),
    position INTEGER NOT NULL,
    gate TEXT NOT NULL,
    PRIMARY KEY (epic, list, position)
);

ALTER TABLE epic_requirements ADD COLUMN item TEXT NOT NULL DEFAULT '';

CREATE TABLE evidence_runs (
    id TEXT PRIMARY KEY,
    epic TEXT NOT NULL,
    item TEXT NOT NULL,
    revision TEXT NOT NULL,
    phase TEXT NOT NULL CHECK (phase IN ('red', 'green')),
    source TEXT NOT NULL CHECK (source IN ('captured', 'supplied')),
    state TEXT NOT NULL CHECK (state IN ('capturing', 'judging', 'ended')),
    spec TEXT NOT NULL,
    grant_id TEXT NOT NULL DEFAULT '',
    holder TEXT NOT NULL DEFAULT '',
    exit INTEGER NOT NULL DEFAULT 0,
    timed_out INTEGER NOT NULL DEFAULT 0,
    not_captured TEXT NOT NULL DEFAULT '',
    log_hash TEXT NOT NULL DEFAULT '',
    log_bytes INTEGER NOT NULL DEFAULT 0,
    judge_step TEXT NOT NULL DEFAULT '',
    judges INTEGER NOT NULL DEFAULT 0,
    verdict TEXT NOT NULL DEFAULT '' CHECK (verdict IN ('', 'holds', 'fails')),
    judge_reason TEXT NOT NULL DEFAULT '',
    passed INTEGER NOT NULL DEFAULT 0,
    created INTEGER NOT NULL,
    ended INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX evidence_runs_epic_created ON evidence_runs (epic, created);

CREATE TABLE evidence_artifacts (
    run TEXT NOT NULL,
    position INTEGER NOT NULL,
    path TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('kept', 'missing', 'over_cap')),
    hash TEXT NOT NULL DEFAULT '',
    blob TEXT NOT NULL DEFAULT '',
    size INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (run, position)
);

ALTER TABLE epic_tasks ADD COLUMN gate TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_tasks ADD COLUMN evidence TEXT NOT NULL DEFAULT '';
UPDATE epic_tasks SET evidence = json_object('conclusion', task_check, 'judge', '');
ALTER TABLE epic_tasks DROP COLUMN task_check;

ALTER TABLE steps ADD COLUMN loop_gate_from INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE steps DROP COLUMN loop_gate_from;
ALTER TABLE epic_tasks ADD COLUMN task_check TEXT NOT NULL DEFAULT '';
UPDATE epic_tasks SET task_check = COALESCE(json_extract(NULLIF(evidence, ''), '$.conclusion'), '');
ALTER TABLE epic_tasks DROP COLUMN evidence;
ALTER TABLE epic_tasks DROP COLUMN gate;
DROP TABLE evidence_artifacts;
DROP TABLE evidence_runs;
ALTER TABLE epic_requirements DROP COLUMN item;
DROP TABLE epic_gate_lists;
DROP TABLE epic_gates;
ALTER TABLE epics DROP COLUMN base;
