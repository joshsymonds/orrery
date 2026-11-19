-- +goose Up
-- The versions of each epic's task specs, one row each, in the order they were stored. A task's epic_tasks row is its
-- version 1, the planner's, and is never a row here, so a task's first stored version is 2. source is what wrote the
-- version: orchestrator or grant in this epic. rescue, approach and carry are reserved for a later epic and are
-- written by nothing now. decision is the id of the epic decision that wrote the version, '' for orreryd's rule
-- grant, and step is the step that wrote it. reason is why.
--
-- deliverable, gate and evidence hold the value the version sets, as epic_tasks holds them, and evidence is the item
-- JSON as epic_tasks.evidence stores it. test_changes is the whole new list of the task's test changes, as a JSON
-- list, and files the task's whole file list after the version, as a JSON list. A NULL field is left unchanged by the
-- version.
--
-- state is applied for a version the task's spec takes, checking while the version's new evidence is judged at the
-- base, and refused for a version that did not take. Only applied is written in this epic, and the planner's read
-- overlays applied versions only. red_run is the red run that judged the version's evidence, '' for none, and refusal
-- is why a refused version was refused. settled is when the version left checking, applied or refused, and 0 while
-- it is checking.
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

-- +goose Down
DROP TABLE epic_task_versions;
