-- +goose Up
ALTER TABLE epics ADD COLUMN paused INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epics ADD COLUMN parked TEXT NOT NULL DEFAULT '';

CREATE TABLE epic_triggers (
    epic TEXT NOT NULL,
    id TEXT NOT NULL,
    cause TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'claimed', 'consumed')),
    turn INTEGER NOT NULL DEFAULT 0,
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, id)
);

CREATE TABLE epic_turns (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    step TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL CHECK (state IN ('running', 'succeeded', 'failed', 'free')),
    reason TEXT NOT NULL DEFAULT '',
    seen INTEGER NOT NULL,
    outline TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '',
    started INTEGER NOT NULL,
    ended INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, seq)
);

CREATE UNIQUE INDEX epic_turns_one_running ON epic_turns (epic) WHERE state = 'running';

CREATE TABLE epic_turn_decisions (
    epic TEXT NOT NULL,
    turn INTEGER NOT NULL,
    position INTEGER NOT NULL,
    text TEXT NOT NULL,
    PRIMARY KEY (epic, turn, position)
);

CREATE TABLE epic_tasks (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    turn INTEGER NOT NULL,
    title TEXT NOT NULL,
    deliverable TEXT NOT NULL,
    task_check TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open',
    PRIMARY KEY (epic, seq)
);

CREATE TABLE epic_task_files (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    position INTEGER NOT NULL,
    path TEXT NOT NULL,
    PRIMARY KEY (epic, task, position)
);

CREATE TABLE epic_task_requirements (
    epic TEXT NOT NULL,
    task INTEGER NOT NULL,
    position INTEGER NOT NULL,
    requirement TEXT NOT NULL,
    PRIMARY KEY (epic, task, position)
);

CREATE TABLE epic_questions (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    turn INTEGER NOT NULL,
    text TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'open',
    PRIMARY KEY (epic, seq)
);

CREATE TABLE engine_rate_limits (
    session_id TEXT NOT NULL PRIMARY KEY,
    status TEXT NOT NULL,
    kind TEXT NOT NULL,
    resets_at INTEGER NOT NULL,
    at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE engine_rate_limits;
DROP TABLE epic_questions;
DROP TABLE epic_task_requirements;
DROP TABLE epic_task_files;
DROP TABLE epic_tasks;
DROP TABLE epic_turn_decisions;
DROP INDEX epic_turns_one_running;
DROP TABLE epic_turns;
DROP TABLE epic_triggers;
ALTER TABLE epics DROP COLUMN parked;
ALTER TABLE epics DROP COLUMN paused;
