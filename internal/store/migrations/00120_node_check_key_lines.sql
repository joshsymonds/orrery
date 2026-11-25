-- +goose Up
-- The key lines of a node check's output, as a JSON list of strings: the first and last lines scripts/gate-remote.sh
-- printed about where the gate ran and the newest lines that say how it failed, read from the whole of its stdout and
-- stderr before their tails are cut, in the order the gate wrote them, at most 64 lines and 8 KiB. A check stored
-- before this migration, one that did not run, and one whose output holds no key line has [].
ALTER TABLE epic_node_checks ADD COLUMN key_lines TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE epic_node_checks DROP COLUMN key_lines;
