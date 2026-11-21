-- +goose Up
-- A task's reopen generation: 0 until its first send_back, and one more for each send_back after. A send_back reopens
-- a task at attempt 1 and restarts its ladder at rung 1, so the reviews, findings, rungs and rung outcomes of its new
-- build would collide with those of its earlier build. Each of the four tables keys its rows by the generation they
-- were written under, so a send_back deletes none of them: the earlier generations stay readable, and the engine, the
-- planner and the API read the task's current generation alone. A task's generation is the count of its send_back
-- versions, which a send_back writes in the same write as it moves the generation.
ALTER TABLE epic_tasks ADD COLUMN generation INTEGER NOT NULL DEFAULT 0;
UPDATE epic_tasks SET generation = (
    SELECT COUNT(*) FROM epic_task_versions AS versions
    WHERE versions.epic = epic_tasks.epic AND versions.task = epic_tasks.seq AND versions.source = 'send_back'
);

-- Each table is rebuilt with generation in its primary key, its other columns as they were, and its rows copied. A row
-- belongs to the generation in force when it was written. A rung row was written at its created second and an outcome
-- at its ended second, so each is given the count of its task's send_back versions created at or before that second: a
-- send_back writes its version and its rung 1 at the same second, and that rung is the new generation's. A review or
-- finding row holds no time, so each is given generation 0. Before this migration a send_back deleted its task's rungs
-- and outcomes, so every one stored is its task's current generation's. No trigger or index names these tables:
-- 00072_epic_revisions_table.sql dropped the triggers.
ALTER TABLE epic_task_reviews RENAME TO epic_task_reviews_before;
CREATE TABLE epic_task_reviews (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    generation INTEGER NOT NULL,
    attempt INTEGER NOT NULL,
    position INTEGER NOT NULL,
    gate TEXT NOT NULL,
    step TEXT NOT NULL,
    verdict TEXT NOT NULL CHECK (verdict IN ('holds', 'fails', 'undetermined')),
    reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (epic, task, generation, attempt, position)
);
INSERT INTO epic_task_reviews (epic, task, generation, attempt, position, gate, step, verdict, reason)
SELECT epic, task, 0, attempt, position, gate, step, verdict, reason FROM epic_task_reviews_before;
DROP TABLE epic_task_reviews_before;

ALTER TABLE epic_task_findings RENAME TO epic_task_findings_before;
CREATE TABLE epic_task_findings (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    generation INTEGER NOT NULL,
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
    unverified INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, task, generation, attempt, review, position)
);
INSERT INTO epic_task_findings (
    epic, task, generation, attempt, review, position, id, claim, citation, location, evidence, verify_by, step,
    result, reason, unverified
)
SELECT epic, task, 0, attempt, review, position, id, claim, citation, location, evidence, verify_by, step, result,
    reason, unverified
FROM epic_task_findings_before;
DROP TABLE epic_task_findings_before;

ALTER TABLE epic_task_rungs RENAME TO epic_task_rungs_before;
CREATE TABLE epic_task_rungs (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    generation INTEGER NOT NULL,
    rung INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('attempt', 'rescue', 'orchestrator', 'approach', 'gap')),
    moved_by TEXT NOT NULL DEFAULT ''
        CHECK (moved_by IN ('', 'unit', 'finding', 'fail_decision', 'cannot_meet', 'fail_task', 'send_back')),
    cause TEXT NOT NULL DEFAULT '',
    gate TEXT NOT NULL DEFAULT '',
    assertion TEXT NOT NULL DEFAULT '',
    step TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, task, generation, rung)
);
INSERT INTO epic_task_rungs (epic, task, generation, rung, kind, moved_by, cause, gate, assertion, step, created)
SELECT rungs.epic, rungs.task, (
    SELECT COUNT(*) FROM epic_task_versions AS versions
    WHERE versions.epic = rungs.epic AND versions.task = rungs.task AND versions.source = 'send_back'
        AND versions.created <= rungs.created
), rungs.rung, rungs.kind, rungs.moved_by, rungs.cause, rungs.gate, rungs.assertion, rungs.step, rungs.created
FROM epic_task_rungs_before AS rungs;
DROP TABLE epic_task_rungs_before;

ALTER TABLE epic_task_rung_outcomes RENAME TO epic_task_rung_outcomes_before;
CREATE TABLE epic_task_rung_outcomes (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    generation INTEGER NOT NULL,
    rung INTEGER NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('failed', 'accepted', 'gap')),
    reason TEXT NOT NULL DEFAULT '',
    ended INTEGER NOT NULL,
    PRIMARY KEY (epic, task, generation, rung)
);
INSERT INTO epic_task_rung_outcomes (epic, task, generation, rung, outcome, reason, ended)
SELECT outcomes.epic, outcomes.task, (
    SELECT COUNT(*) FROM epic_task_versions AS versions
    WHERE versions.epic = outcomes.epic AND versions.task = outcomes.task AND versions.source = 'send_back'
        AND versions.created <= outcomes.ended
), outcomes.rung, outcomes.outcome, outcomes.reason, outcomes.ended
FROM epic_task_rung_outcomes_before AS outcomes;
DROP TABLE epic_task_rung_outcomes_before;

-- +goose Down
-- The four tables are rebuilt in the shapes their earlier migrations gave them, keyed with no generation. That key
-- holds one generation of a task, so each keeps the rows of its task's current generation, the rows the engine reads,
-- and the earlier generations' rows go with the column. findings is rebuilt as 00021_task_build.sql made it with the
-- column 00035_reviewers.sql added, so its stored schema is the one those migrations left.
ALTER TABLE epic_task_reviews RENAME TO epic_task_reviews_generation;
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
INSERT INTO epic_task_reviews (epic, task, attempt, position, gate, step, verdict, reason)
SELECT epic, task, attempt, position, gate, step, verdict, reason FROM epic_task_reviews_generation AS reviews
WHERE generation = COALESCE(
    (SELECT tasks.generation FROM epic_tasks AS tasks WHERE tasks.epic = reviews.epic AND tasks.seq = reviews.task), 0
);
DROP TABLE epic_task_reviews_generation;

ALTER TABLE epic_task_findings RENAME TO epic_task_findings_generation;
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
ALTER TABLE epic_task_findings ADD COLUMN unverified INTEGER NOT NULL DEFAULT 0;
INSERT INTO epic_task_findings (
    epic, task, attempt, review, position, id, claim, citation, location, evidence, verify_by, step, result, reason,
    unverified
)
SELECT epic, task, attempt, review, position, id, claim, citation, location, evidence, verify_by, step, result,
    reason, unverified
FROM epic_task_findings_generation AS findings
WHERE generation = COALESCE(
    (SELECT tasks.generation FROM epic_tasks AS tasks WHERE tasks.epic = findings.epic AND tasks.seq = findings.task),
    0
);
DROP TABLE epic_task_findings_generation;

ALTER TABLE epic_task_rungs RENAME TO epic_task_rungs_generation;
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
SELECT epic, task, rung, kind, moved_by, cause, gate, assertion, step, created FROM epic_task_rungs_generation AS rungs
WHERE generation = COALESCE(
    (SELECT tasks.generation FROM epic_tasks AS tasks WHERE tasks.epic = rungs.epic AND tasks.seq = rungs.task), 0
);
DROP TABLE epic_task_rungs_generation;

ALTER TABLE epic_task_rung_outcomes RENAME TO epic_task_rung_outcomes_generation;
CREATE TABLE epic_task_rung_outcomes (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    rung INTEGER NOT NULL,
    outcome TEXT NOT NULL CHECK (outcome IN ('failed', 'accepted', 'gap')),
    reason TEXT NOT NULL DEFAULT '',
    ended INTEGER NOT NULL,
    PRIMARY KEY (epic, task, rung)
);
INSERT INTO epic_task_rung_outcomes (epic, task, rung, outcome, reason, ended)
SELECT epic, task, rung, outcome, reason, ended FROM epic_task_rung_outcomes_generation AS outcomes
WHERE generation = COALESCE(
    (SELECT tasks.generation FROM epic_tasks AS tasks WHERE tasks.epic = outcomes.epic AND tasks.seq = outcomes.task),
    0
);
DROP TABLE epic_task_rung_outcomes_generation;

ALTER TABLE epic_tasks DROP COLUMN generation;
