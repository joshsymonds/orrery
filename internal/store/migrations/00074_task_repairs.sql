-- +goose Up
-- The files orchestrator decisions refused on each task, one row per file, keyed by the epic, the task and the file. A
-- decision refuses each file its implementer's needs_context return named that the task does not own and the decision's
-- version does not add, so the implementer cannot have it by rule again: a refused file is granted only by a later
-- decision whose version adds it, and that decision clears the row. decision is the id of the epic decision that
-- refused the file, which keeps it when the file is refused again, and created is when the row was written.
CREATE TABLE epic_task_refused (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    path TEXT NOT NULL,
    decision TEXT NOT NULL,
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, task, path)
);

-- moved_by and source each gain send_back, the value a later task of this epic writes for a send-back: a rung's
-- moved_by and a version's source. SQLite cannot change a CHECK in place, so each table is rebuilt with its columns in
-- the same order and its rows copied as they are. Neither table has an index, trigger or foreign key that names it:
-- 00072_epic_revisions_table.sql dropped the triggers.
ALTER TABLE epic_task_rungs RENAME TO epic_task_rungs_before;
CREATE TABLE epic_task_rungs (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    rung INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('attempt', 'rescue', 'orchestrator', 'approach', 'gap')),
    moved_by TEXT NOT NULL DEFAULT ''
        CHECK (moved_by IN ('', 'unit', 'finding', 'fail_decision', 'cannot_meet', 'fail_task', 'send_back')),
    cause TEXT NOT NULL DEFAULT '',
    gate TEXT NOT NULL DEFAULT '',
    assertion TEXT NOT NULL DEFAULT '',
    step TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, task, rung)
);
INSERT INTO epic_task_rungs (epic, task, rung, kind, moved_by, cause, gate, assertion, step, created)
SELECT epic, task, rung, kind, moved_by, cause, gate, assertion, step, created FROM epic_task_rungs_before;
DROP TABLE epic_task_rungs_before;

ALTER TABLE epic_task_versions RENAME TO epic_task_versions_before;
CREATE TABLE epic_task_versions (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    version INTEGER NOT NULL,
    source TEXT NOT NULL
        CHECK (source IN ('orchestrator', 'grant', 'rescue', 'approach', 'carry', 'send_back')),
    decision TEXT NOT NULL DEFAULT '',
    step TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL,
    deliverable TEXT,
    files TEXT,
    test_changes TEXT,
    gate TEXT,
    evidence TEXT,
    state TEXT NOT NULL DEFAULT 'applied' CHECK (state IN ('checking', 'applied', 'refused')),
    red_run TEXT NOT NULL DEFAULT '',
    refusal TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    settled INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, task, version)
);
INSERT INTO epic_task_versions (
    epic, task, version, source, decision, step, reason, deliverable, files, test_changes, gate, evidence, state,
    red_run, refusal, created, settled
)
SELECT epic, task, version, source, decision, step, reason, deliverable, files, test_changes, gate, evidence, state,
    red_run, refusal, created, settled
FROM epic_task_versions_before;
DROP TABLE epic_task_versions_before;

-- +goose Down
-- The rows the narrower CHECKs refuse are removed first: the rungs a send_back moved the task onto, with their
-- outcomes, and the versions a send_back stored. The refused files go with the table.
DELETE FROM epic_task_rung_outcomes WHERE (epic, task, rung) IN (
    SELECT epic, task, rung FROM epic_task_rungs WHERE moved_by = 'send_back'
);
DELETE FROM epic_task_rungs WHERE moved_by = 'send_back';
DELETE FROM epic_task_versions WHERE source = 'send_back';
DROP TABLE epic_task_refused;

ALTER TABLE epic_task_versions RENAME TO epic_task_versions_send_back;
CREATE TABLE epic_task_versions (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    version INTEGER NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('orchestrator', 'grant', 'rescue', 'approach', 'carry')),
    decision TEXT NOT NULL DEFAULT '',
    step TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL,
    deliverable TEXT,
    files TEXT,
    test_changes TEXT,
    gate TEXT,
    evidence TEXT,
    state TEXT NOT NULL DEFAULT 'applied' CHECK (state IN ('checking', 'applied', 'refused')),
    red_run TEXT NOT NULL DEFAULT '',
    refusal TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    settled INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, task, version)
);
INSERT INTO epic_task_versions (
    epic, task, version, source, decision, step, reason, deliverable, files, test_changes, gate, evidence, state,
    red_run, refusal, created, settled
)
SELECT epic, task, version, source, decision, step, reason, deliverable, files, test_changes, gate, evidence, state,
    red_run, refusal, created, settled
FROM epic_task_versions_send_back;
DROP TABLE epic_task_versions_send_back;

ALTER TABLE epic_task_rungs RENAME TO epic_task_rungs_send_back;
CREATE TABLE epic_task_rungs (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    rung INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('attempt', 'rescue', 'orchestrator', 'approach', 'gap')),
    moved_by TEXT NOT NULL DEFAULT ''
        CHECK (moved_by IN ('', 'unit', 'finding', 'fail_decision', 'cannot_meet', 'fail_task')),
    cause TEXT NOT NULL DEFAULT '',
    gate TEXT NOT NULL DEFAULT '',
    assertion TEXT NOT NULL DEFAULT '',
    step TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, task, rung)
);
INSERT INTO epic_task_rungs (epic, task, rung, kind, moved_by, cause, gate, assertion, step, created)
SELECT epic, task, rung, kind, moved_by, cause, gate, assertion, step, created FROM epic_task_rungs_send_back;
DROP TABLE epic_task_rungs_send_back;
