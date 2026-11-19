-- +goose Up
-- The files a change_approach action adds to its task and removes from it, each a JSON list of paths, and '[]' for an
-- action that names none, which is every action of another kind. The recarve's files are checked against the other
-- tasks of the epic when the action is carried out, and the version it stores carries them.
ALTER TABLE planner_actions ADD COLUMN files TEXT NOT NULL DEFAULT '[]';
ALTER TABLE planner_actions ADD COLUMN remove_files TEXT NOT NULL DEFAULT '[]';

-- rebriefs holds the first line of each failure an orchestrator's re_brief decision answered on the task, each followed
-- by a newline, in the order the decisions were carried out, and '' for none. A re-brief on a first line the column holds
-- is refused, so a task gets one re-brief per failure, and the column is kept across climbs.
ALTER TABLE epic_tasks ADD COLUMN rebriefs TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE epic_tasks DROP COLUMN rebriefs;
ALTER TABLE planner_actions DROP COLUMN remove_files;
ALTER TABLE planner_actions DROP COLUMN files;
