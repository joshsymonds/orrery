-- +goose Up
-- The planner-class parks of each epic, one row each in the order they were parked. A chain groups the parks that one
-- bound counts together: chain is the seq of the first park of the chain, so a park that starts a chain has chain = seq.
-- subject names what the park is about: a node path, T<seq> for a task, release, or "" for a park with no subject.
CREATE TABLE epic_parks (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    class TEXT NOT NULL,
    subject TEXT NOT NULL,
    chain INTEGER NOT NULL,
    reason TEXT NOT NULL,
    created INTEGER NOT NULL,
    PRIMARY KEY (epic, seq)
);

-- park is the seq of the planner park the epic held when the action was taken or refused, 0 when it held none. An action
-- counts toward the chain of that park.
ALTER TABLE planner_actions ADD COLUMN park INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE planner_actions DROP COLUMN park;
DROP TABLE epic_parks;
