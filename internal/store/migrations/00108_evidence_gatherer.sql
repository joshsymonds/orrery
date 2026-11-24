-- +goose Up
-- evidence_runs is rebuilt to take the states waiting and gathering, and to hold the attached capture, the gatherer step
-- holding its brief, and the count of gatherers that ended leaving it unattached. The old table is renamed out of the
-- way, the table is created under its name, the rows are copied across, and the old table is dropped, as 00072 rebuilt
-- epic_tasks. No trigger or view names evidence_runs (00072 dropped the evidence revision triggers), so the rename
-- rewrites nothing.
ALTER TABLE evidence_runs RENAME TO evidence_runs_old;

CREATE TABLE evidence_runs (
    id TEXT PRIMARY KEY,
    epic TEXT NOT NULL,
    item TEXT NOT NULL,
    revision TEXT NOT NULL,
    phase TEXT NOT NULL CHECK (phase IN ('red', 'green')),
    source TEXT NOT NULL CHECK (source IN ('captured', 'supplied')),
    state TEXT NOT NULL CHECK (state IN ('capturing', 'waiting', 'gathering', 'judging', 'ended')),
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
    ended INTEGER NOT NULL DEFAULT 0,
    stderr_hash TEXT NOT NULL DEFAULT '',
    stderr_bytes INTEGER NOT NULL DEFAULT 0,
    capture TEXT NOT NULL DEFAULT '',
    gatherer TEXT NOT NULL DEFAULT '',
    gatherers INTEGER NOT NULL DEFAULT 0
);

INSERT INTO evidence_runs (
    id, epic, item, revision, phase, source, state, spec, grant_id, holder, exit, timed_out, not_captured, log_hash,
    log_bytes, judge_step, judges, verdict, judge_reason, passed, created, ended, stderr_hash, stderr_bytes
)
SELECT
    id, epic, item, revision, phase, source, state, spec, grant_id, holder, exit, timed_out, not_captured, log_hash,
    log_bytes, judge_step, judges, verdict, judge_reason, passed, created, ended, stderr_hash, stderr_bytes
FROM evidence_runs_old;

DROP TABLE evidence_runs_old;

CREATE INDEX evidence_runs_epic_created ON evidence_runs (epic, created);

-- A capture is one command orreryd ran for evidence, kept whole: its argv and the files it keeps, its exit and log, the
-- files it left, and the briefs it answers. A running capture has no exit yet; state moves it to ended when it finishes.
CREATE TABLE evidence_captures (
    id TEXT PRIMARY KEY,
    epic TEXT NOT NULL,
    repository TEXT NOT NULL,
    step TEXT NOT NULL,
    role TEXT NOT NULL,
    runner TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    argv TEXT NOT NULL,
    files TEXT NOT NULL,
    limit_seconds INTEGER NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('running', 'ended')),
    grant_id TEXT NOT NULL DEFAULT '',
    holder TEXT NOT NULL DEFAULT '',
    exit INTEGER NOT NULL DEFAULT 0,
    timed_out INTEGER NOT NULL DEFAULT 0,
    not_captured TEXT NOT NULL DEFAULT '',
    log_hash TEXT NOT NULL DEFAULT '',
    log_bytes INTEGER NOT NULL DEFAULT 0,
    stderr_hash TEXT NOT NULL DEFAULT '',
    stderr_bytes INTEGER NOT NULL DEFAULT 0,
    delivered INTEGER NOT NULL DEFAULT 0,
    created INTEGER NOT NULL,
    ended INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX evidence_captures_repository_commit ON evidence_captures (repository, commit_sha);

CREATE TABLE evidence_capture_files (
    capture TEXT NOT NULL,
    position INTEGER NOT NULL,
    path TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('kept', 'missing', 'over_cap')),
    hash TEXT NOT NULL DEFAULT '',
    blob TEXT NOT NULL DEFAULT '',
    size INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (capture, position)
);

CREATE TABLE evidence_capture_briefs (
    capture TEXT NOT NULL,
    run TEXT NOT NULL,
    PRIMARY KEY (capture, run)
);

-- A gatherer is the step that gathers one epic's evidence for its waiting briefs. seq numbers an epic's gatherers in
-- order, and a step is one gatherer; the partial index lets an epic hold at most one running gatherer at a time.
CREATE TABLE evidence_gatherers (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    step TEXT NOT NULL,
    started INTEGER NOT NULL,
    ended INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, seq)
);

CREATE UNIQUE INDEX evidence_gatherers_step ON evidence_gatherers (step);

CREATE UNIQUE INDEX evidence_gatherers_running ON evidence_gatherers (epic) WHERE ended = 0;

-- +goose Down
-- The gathering states go back to capturing, the state a run holding no capture had before, and the gatherer columns
-- and the capture tables go with the rebuild.
DROP INDEX evidence_gatherers_running;
DROP INDEX evidence_gatherers_step;
DROP TABLE evidence_gatherers;
DROP TABLE evidence_capture_briefs;
DROP TABLE evidence_capture_files;
DROP INDEX evidence_captures_repository_commit;
DROP TABLE evidence_captures;

ALTER TABLE evidence_runs RENAME TO evidence_runs_old;

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
    ended INTEGER NOT NULL DEFAULT 0,
    stderr_hash TEXT NOT NULL DEFAULT '',
    stderr_bytes INTEGER NOT NULL DEFAULT 0
);

INSERT INTO evidence_runs (
    id, epic, item, revision, phase, source, state, spec, grant_id, holder, exit, timed_out, not_captured, log_hash,
    log_bytes, judge_step, judges, verdict, judge_reason, passed, created, ended, stderr_hash, stderr_bytes
)
SELECT
    id, epic, item, revision, phase, source,
    CASE WHEN state IN ('waiting', 'gathering') THEN 'capturing' ELSE state END,
    spec, grant_id, holder, exit, timed_out, not_captured, log_hash,
    log_bytes, judge_step, judges, verdict, judge_reason, passed, created, ended, stderr_hash, stderr_bytes
FROM evidence_runs_old;

DROP TABLE evidence_runs_old;

CREATE INDEX evidence_runs_epic_created ON evidence_runs (epic, created);
