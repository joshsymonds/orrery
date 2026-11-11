-- +goose Up
CREATE TABLE steps (
    id TEXT PRIMARY KEY,
    agent TEXT NOT NULL,
    prompt TEXT NOT NULL,
    output_schema TEXT NOT NULL,
    workspace TEXT NOT NULL,
    revision TEXT NOT NULL,
    worktree TEXT NOT NULL,
    done_value TEXT NOT NULL,
    read_only BOOLEAN NOT NULL,
    owned TEXT NOT NULL,
    gate TEXT NOT NULL,
    gate_limit INTEGER NOT NULL,
    started INTEGER NOT NULL,
    checks TEXT NOT NULL,
    result TEXT NOT NULL,
    status TEXT NOT NULL,
    end_value TEXT NOT NULL,
    reason TEXT NOT NULL,
    ended INTEGER NOT NULL
);

-- +goose Down
DROP TABLE steps;
