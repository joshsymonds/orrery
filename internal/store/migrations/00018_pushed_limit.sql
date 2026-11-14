-- +goose Up
CREATE TABLE pushed_limit_notices (
    limit_key TEXT PRIMARY KEY,
    time INTEGER NOT NULL
);

-- +goose Down
DROP TABLE pushed_limit_notices;
