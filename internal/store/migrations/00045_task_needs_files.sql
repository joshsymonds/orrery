-- +goose Up
-- The files a failed task's implementer named as needed beyond its owned list, as a JSON array of paths, [] while none
-- are named. The planner reads them under the failed task as files a later task may need.
ALTER TABLE epic_tasks ADD COLUMN needs_files TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE epic_tasks DROP COLUMN needs_files;
