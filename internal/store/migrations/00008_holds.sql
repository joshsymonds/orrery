-- +goose Up
CREATE TABLE holds (
    member TEXT PRIMARY KEY,
    since INTEGER NOT NULL
);

ALTER TABLE grants ADD COLUMN command TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE grants DROP COLUMN command;
DROP TABLE holds;
