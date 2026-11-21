-- +goose Up
-- An amendment records the maker that made it: the owner, a conversation that steers the epic, orreryd for a call of a
-- step that is not the epic's, or the planner, through an amend_evidence action. SQLite cannot alter a CHECK in place, so
-- the table is rebuilt with every column 00052_evidence_amendments.sql gave it and its rows copied, each keeping its
-- amender: no row is backfilled. The index is created again once the old table, which holds its name, is dropped. No
-- revision trigger is made: 00072_epic_revisions_table.sql dropped them, and the store's pre-update hook counts the
-- table's writes.
ALTER TABLE epic_amendments RENAME TO epic_amendments_before;
CREATE TABLE epic_amendments (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    requirement TEXT NOT NULL,
    amender TEXT NOT NULL CHECK (amender IN ('owner', 'planner', 'conversation', 'orreryd')),
    turn INTEGER NOT NULL DEFAULT 0,
    position INTEGER NOT NULL DEFAULT 0,
    prior_item TEXT NOT NULL,
    item TEXT NOT NULL,
    reason TEXT NOT NULL,
    base TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('checking', 'capturing', 'applied', 'refused')),
    red_attempt INTEGER NOT NULL DEFAULT 0,
    red_run TEXT NOT NULL,
    stored_base TEXT NOT NULL DEFAULT '',
    green_attempt INTEGER NOT NULL DEFAULT 0,
    green_run TEXT NOT NULL DEFAULT '',
    repro_check TEXT NOT NULL DEFAULT '',
    refusal TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    settled INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, seq)
);
INSERT INTO epic_amendments (
    epic, seq, requirement, amender, turn, position, prior_item, item, reason, base, state, red_attempt, red_run,
    stored_base, green_attempt, green_run, repro_check, refusal, created, settled
)
SELECT
    epic, seq, requirement, amender, turn, position, prior_item, item, reason, base, state, red_attempt, red_run,
    stored_base, green_attempt, green_run, repro_check, refusal, created, settled
FROM epic_amendments_before;
DROP TABLE epic_amendments_before;

-- The open amendments are read on every pass, so they are indexed by state in the order they are driven.
CREATE INDEX epic_amendments_state ON epic_amendments (state, epic, seq);

-- +goose Down
-- The table is rebuilt with the two-value amender CHECK 00052_evidence_amendments.sql gave it. An amendment by a
-- conversation or by orreryd has no place in that CHECK, so its rows are dropped, as 00083_release_slot_outside.sql drops
-- its outside rows.
ALTER TABLE epic_amendments RENAME TO epic_amendments_makers;
CREATE TABLE epic_amendments (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    requirement TEXT NOT NULL,
    amender TEXT NOT NULL CHECK (amender IN ('owner', 'planner')),
    turn INTEGER NOT NULL DEFAULT 0,
    position INTEGER NOT NULL DEFAULT 0,
    prior_item TEXT NOT NULL,
    item TEXT NOT NULL,
    reason TEXT NOT NULL,
    base TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('checking', 'capturing', 'applied', 'refused')),
    red_attempt INTEGER NOT NULL DEFAULT 0,
    red_run TEXT NOT NULL,
    stored_base TEXT NOT NULL DEFAULT '',
    green_attempt INTEGER NOT NULL DEFAULT 0,
    green_run TEXT NOT NULL DEFAULT '',
    repro_check TEXT NOT NULL DEFAULT '',
    refusal TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    settled INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, seq)
);
INSERT INTO epic_amendments (
    epic, seq, requirement, amender, turn, position, prior_item, item, reason, base, state, red_attempt, red_run,
    stored_base, green_attempt, green_run, repro_check, refusal, created, settled
)
SELECT
    epic, seq, requirement, amender, turn, position, prior_item, item, reason, base, state, red_attempt, red_run,
    stored_base, green_attempt, green_run, repro_check, refusal, created, settled
FROM epic_amendments_makers WHERE amender IN ('owner', 'planner');
DROP TABLE epic_amendments_makers;
CREATE INDEX epic_amendments_state ON epic_amendments (state, epic, seq);
