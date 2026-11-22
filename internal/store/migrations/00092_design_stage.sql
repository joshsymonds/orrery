-- +goose Up
-- A build's stage gains design: the design judge's step, which runs after a design task's last review gate. SQLite
-- cannot change a column's CHECK in place, so epic_tasks is rebuilt with its columns in the same order, and every row is
-- copied with all its columns. No index, trigger, view or foreign key names epic_tasks.
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
        CHECK (stage IN ('', 'red', 'implement', 'orchestrate', 'green', 'review', 'verify', 'design')),
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
    gate_changes TEXT NOT NULL DEFAULT '[]',
    rebriefs TEXT NOT NULL DEFAULT '',
    notes TEXT NOT NULL DEFAULT '[]',
    generation INTEGER NOT NULL DEFAULT 0,
    depends_on TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT '' CHECK (kind IN ('', 'design')),
    revises INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, seq)
);
INSERT INTO epic_tasks (
    epic, seq, turn, title, deliverable, status, gate, evidence, stage, base, attempt, rerun, no_verdict, held, step,
    red_run, green_run, revision, failure, review, refs, gate_pos, finding_pos, batch, guard, needs_files, decisions,
    after_tasks, question, gate_changes, rebriefs, notes, generation, depends_on, kind, revises
)
SELECT epic, seq, turn, title, deliverable, status, gate, evidence, stage, base, attempt, rerun, no_verdict, held, step,
    red_run, green_run, revision, failure, review, refs, gate_pos, finding_pos, batch, guard, needs_files, decisions,
    after_tasks, question, gate_changes, rebriefs, notes, generation, depends_on, kind, revises
FROM epic_tasks_before;
DROP TABLE epic_tasks_before;

-- +goose Down
-- A build at design goes back to review, since the checks before this migration allow no design stage. Past its last
-- review gate, review accepts the task as it did before the design judge.
ALTER TABLE epic_tasks RENAME TO epic_tasks_design;
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
    gate_changes TEXT NOT NULL DEFAULT '[]',
    rebriefs TEXT NOT NULL DEFAULT '',
    notes TEXT NOT NULL DEFAULT '[]',
    generation INTEGER NOT NULL DEFAULT 0,
    depends_on TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL DEFAULT '' CHECK (kind IN ('', 'design')),
    revises INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, seq)
);
INSERT INTO epic_tasks (
    epic, seq, turn, title, deliverable, status, gate, evidence, stage, base, attempt, rerun, no_verdict, held, step,
    red_run, green_run, revision, failure, review, refs, gate_pos, finding_pos, batch, guard, needs_files, decisions,
    after_tasks, question, gate_changes, rebriefs, notes, generation, depends_on, kind, revises
)
SELECT epic, seq, turn, title, deliverable, status, gate, evidence, CASE stage WHEN 'design' THEN 'review' ELSE stage END,
    base, attempt, rerun, no_verdict, held, step, red_run, green_run, revision, failure, review, refs, gate_pos,
    finding_pos, batch, guard, needs_files, decisions, after_tasks, question, gate_changes, rebriefs, notes, generation,
    depends_on, kind, revises
FROM epic_tasks_design;
DROP TABLE epic_tasks_design;
