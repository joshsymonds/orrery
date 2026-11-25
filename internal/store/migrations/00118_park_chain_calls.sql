-- +goose Up
-- Each planner park row records who answers it under its chain's bound, and the epic's heads when it was stored. state
-- is planner for a park the planner answers, bound for a park stored at the bound of its chain that waits on the
-- planner's call whether it recurs or is new, and owner for a park sent to the owner as retry_bound. stored_base and
-- candidate are the epic's stored base and candidate when the park was stored, so a later park can tell whether the
-- epic's head moved since. A row stored before this migration is a park the planner answered, with no heads recorded.
ALTER TABLE epic_parks ADD COLUMN state TEXT NOT NULL DEFAULT 'planner' CHECK (state IN ('planner', 'bound', 'owner'));
ALTER TABLE epic_parks ADD COLUMN stored_base TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_parks ADD COLUMN candidate TEXT NOT NULL DEFAULT '';

-- +goose Down
-- A bound or owner park reads as one the planner answered at the version before, which kept no row for an owner park,
-- so the rows stay and lose only their state and heads.
ALTER TABLE epic_parks DROP COLUMN candidate;
ALTER TABLE epic_parks DROP COLUMN stored_base;
ALTER TABLE epic_parks DROP COLUMN state;
