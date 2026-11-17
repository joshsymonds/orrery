-- +goose Up
-- The amendments of each epic's Requirement evidence items, one row each in the order they were asked for. amender is
-- owner or planner; a planner amendment keeps the turn and 1-based position of the action that asked for it, and an
-- owner amendment keeps 0 for both. prior_item is the item the Requirement held when the amendment was asked for, and
-- item the item it asks for, each as the JSON an item is stored in. base is the epic's base, where red_run, the red run
-- of item at attempt red_attempt, is checked.
--
-- state is checking while the red run of the new item at the base is judged, refused when the amendment is refused, which
-- leaves the old item in place, capturing once the new item is applied and its green run green_run, with the
-- reproduction check repro_check for a test item, runs at stored_base, the epic's stored base when it was applied, at
-- attempt green_attempt, and applied once the amendment is done. refusal holds the reason of a refused amendment. settled is when it left checking,
-- applied or refused, and 0 while it is checking.
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

-- The open amendments are read on every pass, so they are indexed by state in the order they are driven.
CREATE INDEX epic_amendments_state ON epic_amendments (state, epic, seq);

-- An amend_evidence action names the Requirement it amends and the item it asks for, as the JSON an item is stored in;
-- every other kind keeps both empty.
ALTER TABLE planner_actions ADD COLUMN requirement TEXT NOT NULL DEFAULT '';
ALTER TABLE planner_actions ADD COLUMN item TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE planner_actions DROP COLUMN item;
ALTER TABLE planner_actions DROP COLUMN requirement;
DROP INDEX epic_amendments_state;
DROP TABLE epic_amendments;
