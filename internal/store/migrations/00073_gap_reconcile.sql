-- +goose Up
-- reviewer is the reviewer a rerun_review action names, whose verdict in the epic's review the action asks to run again,
-- and is '' for every other kind of action.
ALTER TABLE planner_actions ADD COLUMN reviewer TEXT NOT NULL DEFAULT '';

-- The gaps one planner turn declared it cannot resolve without the owner, one row each in the order the turn declared
-- them. epic is the epic's id, turn the seq of the turn that declared the gap, and position the declaration's 1-based
-- place in the turn's return. gap is the gap's id, reason is why the planner cannot resolve it, and recommended is what
-- the planner recommends the owner do: release or correct. A row is written with its turn and never changed.
CREATE TABLE planner_unresolvable (
    epic TEXT NOT NULL,
    turn INTEGER NOT NULL,
    position INTEGER NOT NULL,
    gap TEXT NOT NULL,
    reason TEXT NOT NULL,
    recommended TEXT NOT NULL CHECK (recommended IN ('release', 'correct')),
    PRIMARY KEY (epic, turn, position)
);

-- Each planner turn a gap was given, one row each: gap is the gap's id, turn the seq of the turn it was given to, reason
-- why, and created when the row was written. A gap has at most two rows: the store refuses to record a third turn for
-- a gap, so a gap that keeps failing is not given a third planner turn.
CREATE TABLE epic_gap_turns (
    epic TEXT NOT NULL,
    gap TEXT NOT NULL,
    turn INTEGER NOT NULL,
    reason TEXT NOT NULL,
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, gap, turn)
);

-- Each rerun_review action an epic's planner took: reviewer is the reviewer whose verdict the review runs again for, and
-- turn and position are the action's turn and 1-based position in the turn's return. created is when the action was
-- carried out. A row is written with its action and never changed.
CREATE TABLE epic_review_reruns (
    epic TEXT NOT NULL,
    reviewer TEXT NOT NULL,
    turn INTEGER NOT NULL,
    position INTEGER NOT NULL,
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, turn, position)
);

-- +goose Down
DROP TABLE epic_review_reruns;
DROP TABLE epic_gap_turns;
DROP TABLE planner_unresolvable;
ALTER TABLE planner_actions DROP COLUMN reviewer;
