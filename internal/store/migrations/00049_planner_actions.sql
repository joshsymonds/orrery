-- +goose Up
-- The actions a planner turn returned, one row each, keyed by the epic, the turn and the action's 1-based position in the
-- return. An action is pending when its turn completes, taken when the engine carries it out, and refused when the engine
-- refuses it, with the refusal stored. undo_none is a JSON array of the undo-none positions the action names.
CREATE TABLE planner_actions (
    epic TEXT NOT NULL,
    turn INTEGER NOT NULL,
    position INTEGER NOT NULL,
    kind TEXT NOT NULL,
    node TEXT NOT NULL DEFAULT '',
    task INTEGER NOT NULL DEFAULT 0,
    close_kind TEXT NOT NULL DEFAULT '',
    by_epic TEXT NOT NULL DEFAULT '',
    undo_none TEXT NOT NULL DEFAULT '[]',
    reason TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'taken', 'refused')),
    refusal TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (epic, turn, position)
);

-- The pending actions are read on every pass, so they are indexed by state in the order they are carried out.
CREATE INDEX planner_actions_state ON planner_actions (state, epic, turn, position);

-- +goose Down
DROP INDEX planner_actions_state;
DROP TABLE planner_actions;
