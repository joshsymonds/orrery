-- +goose Up
CREATE TABLE epic_node_checks (
    id TEXT PRIMARY KEY,
    epic TEXT NOT NULL,
    path TEXT NOT NULL,
    revision TEXT NOT NULL,
    argv TEXT NOT NULL,
    limit_seconds INTEGER NOT NULL,
    needs TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('running', 'ended')),
    grant_id TEXT NOT NULL DEFAULT '',
    holder TEXT NOT NULL DEFAULT '',
    exit INTEGER NOT NULL DEFAULT 0,
    timed_out INTEGER NOT NULL DEFAULT 0,
    not_run TEXT NOT NULL DEFAULT '',
    stdout TEXT NOT NULL DEFAULT '',
    stderr TEXT NOT NULL DEFAULT '',
    created INTEGER NOT NULL,
    ended INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX epic_node_checks_epic_created ON epic_node_checks (epic, created);

-- +goose Down
DROP TABLE epic_node_checks;
