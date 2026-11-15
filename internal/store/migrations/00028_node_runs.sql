-- +goose Up
CREATE TABLE epic_node_runs (
    epic TEXT NOT NULL,
    path TEXT NOT NULL,
    id TEXT NOT NULL,
    node TEXT NOT NULL,
    kind TEXT NOT NULL,
    task INTEGER NOT NULL DEFAULT 0,
    round INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL,
    outcome TEXT NOT NULL DEFAULT '',
    loop_round INTEGER NOT NULL DEFAULT 0,
    granted INTEGER NOT NULL DEFAULT 0,
    mark INTEGER NOT NULL DEFAULT 0,
    started INTEGER NOT NULL DEFAULT 0,
    ended INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, path)
);

CREATE UNIQUE INDEX epic_node_runs_id ON epic_node_runs (epic, id);

-- +goose Down
DROP TABLE epic_node_runs;
