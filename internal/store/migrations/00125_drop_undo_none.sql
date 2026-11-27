-- +goose Up
-- A stopped release resumes at its first unfinished step whatever that step's undo says, so no planner action names
-- undo-none positions and planner_actions keeps no undo_none column. An epic parked release_undo_none was parked at a
-- release step, which is the class a release that stopped at a step with any undo takes, so it reads release_step.
UPDATE epics SET park_class = 'release_step' WHERE park_class = 'release_undo_none';
ALTER TABLE planner_actions DROP COLUMN undo_none;

-- +goose Down
-- The column comes back empty, as an action stored before this migration names no positions. An epic parked
-- release_step stays so: no release parks release_undo_none any more, so the Down does not restore that class.
ALTER TABLE planner_actions ADD COLUMN undo_none TEXT NOT NULL DEFAULT '[]';
