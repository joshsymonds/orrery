-- +goose Up
-- The capturing state goes. A run stored capturing holds no capture, so each one moves to waiting, with no capture,
-- no gatherer and no gatherers: a captured run becomes an open brief for its epic's gatherer, and a supplied run waits
-- until the engine records its capture. evidence_runs is rebuilt without capturing in its state check, as 00108 rebuilt
-- it: the old table is renamed out of the way, the table is created under its name, the rows are copied across, and the
-- old table is dropped. No trigger or view names evidence_runs, so the rename rewrites nothing.
ALTER TABLE evidence_runs RENAME TO evidence_runs_old;

CREATE TABLE evidence_runs (
    id TEXT PRIMARY KEY,
    epic TEXT NOT NULL,
    item TEXT NOT NULL,
    revision TEXT NOT NULL,
    phase TEXT NOT NULL CHECK (phase IN ('red', 'green')),
    source TEXT NOT NULL CHECK (source IN ('captured', 'supplied')),
    state TEXT NOT NULL CHECK (state IN ('waiting', 'gathering', 'judging', 'ended')),
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
    log_bytes, judge_step, judges, verdict, judge_reason, passed, created, ended, stderr_hash, stderr_bytes, capture,
    gatherer, gatherers
)
SELECT
    id, epic, item, revision, phase, source,
    CASE WHEN state = 'capturing' THEN 'waiting' ELSE state END,
    spec, grant_id, holder, exit, timed_out, not_captured, log_hash,
    log_bytes, judge_step, judges, verdict, judge_reason, passed, created, ended, stderr_hash, stderr_bytes,
    CASE WHEN state = 'capturing' THEN '' ELSE capture END,
    CASE WHEN state = 'capturing' THEN '' ELSE gatherer END,
    CASE WHEN state = 'capturing' THEN 0 ELSE gatherers END
FROM evidence_runs_old;

DROP TABLE evidence_runs_old;

CREATE INDEX evidence_runs_epic_created ON evidence_runs (epic, created);

-- +goose Down
-- evidence_runs is rebuilt as 00108 left it, with capturing in its state check. Every row keeps its state: waiting and
-- gathering are states 00108 already allows, and the capture and gatherer columns stay as they are.
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
    log_bytes, judge_step, judges, verdict, judge_reason, passed, created, ended, stderr_hash, stderr_bytes, capture,
    gatherer, gatherers
)
SELECT
    id, epic, item, revision, phase, source, state, spec, grant_id, holder, exit, timed_out, not_captured, log_hash,
    log_bytes, judge_step, judges, verdict, judge_reason, passed, created, ended, stderr_hash, stderr_bytes, capture,
    gatherer, gatherers
FROM evidence_runs_old;

DROP TABLE evidence_runs_old;

CREATE INDEX evidence_runs_epic_created ON evidence_runs (epic, created);
