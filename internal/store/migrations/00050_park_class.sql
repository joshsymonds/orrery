-- +goose Up
-- The class of the epic's park, which says who answers it: an owner class is answered by the owner's question, a planner
-- class wakes the planner. "" while the epic is not parked, and for a park stored before this migration, which the owner
-- answered.
ALTER TABLE epics ADD COLUMN park_class TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE epics DROP COLUMN park_class;
