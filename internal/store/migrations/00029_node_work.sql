-- +goose Up
ALTER TABLE epic_node_runs ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_node_runs ADD COLUMN revision TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_node_runs ADD COLUMN refs TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_node_runs ADD COLUMN refs_checked INTEGER NOT NULL DEFAULT 0;
ALTER TABLE epic_node_runs ADD COLUMN result TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_node_runs ADD COLUMN output TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE epic_node_runs DROP COLUMN output;
ALTER TABLE epic_node_runs DROP COLUMN result;
ALTER TABLE epic_node_runs DROP COLUMN refs_checked;
ALTER TABLE epic_node_runs DROP COLUMN refs;
ALTER TABLE epic_node_runs DROP COLUMN revision;
ALTER TABLE epic_node_runs DROP COLUMN attempt;
