-- +goose Up
-- The orchestrator's decisions on a task's build: how many orreryd carried out, the tasks the build waits for an
-- integrate batch to take before its implementer runs again, as their seqs joined with commas and '' for a build that
-- never waited, and the seq of the owner question the build waits on, 0 while it waits on none. The build's stage gains
-- orchestrate, and its hold gains waiting and asked. SQLite cannot change a column's CHECK in place, so epic_tasks is
-- rebuilt with its columns in the same order; no index, trigger or foreign key names it.
ALTER TABLE epic_tasks RENAME TO epic_tasks_before;
CREATE TABLE epic_tasks (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    turn INTEGER NOT NULL,
    title TEXT NOT NULL,
    deliverable TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    gate TEXT NOT NULL DEFAULT '',
    evidence TEXT NOT NULL DEFAULT '',
    stage TEXT NOT NULL DEFAULT ''
        CHECK (stage IN ('', 'red', 'implement', 'orchestrate', 'green', 'review', 'verify')),
    base TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0,
    rerun INTEGER NOT NULL DEFAULT 0,
    no_verdict INTEGER NOT NULL DEFAULT 0,
    held TEXT NOT NULL DEFAULT '' CHECK (held IN ('', 'stopped', 'parked', 'waiting', 'asked')),
    step TEXT NOT NULL DEFAULT '',
    red_run TEXT NOT NULL DEFAULT '',
    green_run TEXT NOT NULL DEFAULT '',
    revision TEXT NOT NULL DEFAULT '',
    failure TEXT NOT NULL DEFAULT '',
    review TEXT NOT NULL DEFAULT '' CHECK (review IN ('', 'unreviewed', 'reviewed')),
    refs TEXT NOT NULL DEFAULT '',
    gate_pos INTEGER NOT NULL DEFAULT 0,
    finding_pos INTEGER NOT NULL DEFAULT 0,
    batch TEXT NOT NULL DEFAULT '',
    guard TEXT NOT NULL DEFAULT '',
    needs_files TEXT NOT NULL DEFAULT '[]',
    decisions INTEGER NOT NULL DEFAULT 0,
    after_tasks TEXT NOT NULL DEFAULT '',
    question INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, seq)
);
INSERT INTO epic_tasks (
    epic, seq, turn, title, deliverable, status, gate, evidence, stage, base, attempt, rerun, no_verdict, held, step,
    red_run, green_run, revision, failure, review, refs, gate_pos, finding_pos, batch, guard, needs_files
)
SELECT epic, seq, turn, title, deliverable, status, gate, evidence, stage, base, attempt, rerun, no_verdict, held, step,
    red_run, green_run, revision, failure, review, refs, gate_pos, finding_pos, batch, guard, needs_files
FROM epic_tasks_before;
DROP TABLE epic_tasks_before;

-- +goose Down
-- A build at orchestrate goes back to implement, and a build held waiting or asked holds nothing, since the checks
-- before this migration allow neither.
ALTER TABLE epic_tasks RENAME TO epic_tasks_requests;
CREATE TABLE epic_tasks (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    turn INTEGER NOT NULL,
    title TEXT NOT NULL,
    deliverable TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    gate TEXT NOT NULL DEFAULT '',
    evidence TEXT NOT NULL DEFAULT '',
    stage TEXT NOT NULL DEFAULT '' CHECK (stage IN ('', 'red', 'implement', 'green', 'review', 'verify')),
    base TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0,
    rerun INTEGER NOT NULL DEFAULT 0,
    no_verdict INTEGER NOT NULL DEFAULT 0,
    held TEXT NOT NULL DEFAULT '' CHECK (held IN ('', 'stopped', 'parked')),
    step TEXT NOT NULL DEFAULT '',
    red_run TEXT NOT NULL DEFAULT '',
    green_run TEXT NOT NULL DEFAULT '',
    revision TEXT NOT NULL DEFAULT '',
    failure TEXT NOT NULL DEFAULT '',
    review TEXT NOT NULL DEFAULT '' CHECK (review IN ('', 'unreviewed', 'reviewed')),
    refs TEXT NOT NULL DEFAULT '',
    gate_pos INTEGER NOT NULL DEFAULT 0,
    finding_pos INTEGER NOT NULL DEFAULT 0,
    batch TEXT NOT NULL DEFAULT '',
    guard TEXT NOT NULL DEFAULT '',
    needs_files TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (epic, seq)
);
INSERT INTO epic_tasks (
    epic, seq, turn, title, deliverable, status, gate, evidence, stage, base, attempt, rerun, no_verdict, held, step,
    red_run, green_run, revision, failure, review, refs, gate_pos, finding_pos, batch, guard, needs_files
)
SELECT epic, seq, turn, title, deliverable, status, gate, evidence,
    CASE stage WHEN 'orchestrate' THEN 'implement' ELSE stage END, base, attempt, rerun, no_verdict,
    CASE WHEN held IN ('waiting', 'asked') THEN '' ELSE held END, step,
    red_run, green_run, revision, failure, review, refs, gate_pos, finding_pos, batch, guard, needs_files
FROM epic_tasks_requests;
DROP TABLE epic_tasks_requests;
