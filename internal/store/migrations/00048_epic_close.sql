-- +goose Up
-- Who closed the epic, the owner or the planner, and "" while it is not closed. A planner close routes the ending report.
ALTER TABLE epics ADD COLUMN closed_by TEXT NOT NULL DEFAULT '';

-- The epic the close named as the one that replaces it, and "" when the close named none.
ALTER TABLE epics ADD COLUMN superseded_by TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE epics DROP COLUMN superseded_by;
ALTER TABLE epics DROP COLUMN closed_by;
