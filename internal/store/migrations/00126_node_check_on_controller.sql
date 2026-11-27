-- +goose Up
-- Whether a node check runs on the controller's own host rather than on the host its epic lives on: 1 for a release step
-- whose work is the controller's, such as switching its services or the fleet's hosts. A check stored before this column
-- was added runs on its epic's host, as it did.
ALTER TABLE epic_node_checks ADD COLUMN on_controller INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE epic_node_checks DROP COLUMN on_controller;
