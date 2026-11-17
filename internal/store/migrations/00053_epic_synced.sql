-- +goose Up
-- The commit of origin's main that the epic's stored base last merged in a sync, '' while no sync has passed. The
-- epic's review and its owned checks read the epic's own change from it, so the change they read leaves origin's out.
ALTER TABLE epics ADD COLUMN synced TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE epics DROP COLUMN synced;
