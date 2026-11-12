-- +goose Up
CREATE TABLE grants (
    id TEXT PRIMARY KEY,
    project TEXT NOT NULL,
    client_pid INTEGER NOT NULL,
    client_start INTEGER NOT NULL,
    pools TEXT NOT NULL,
    granted INTEGER NOT NULL
);

CREATE TABLE grant_members (
    member TEXT PRIMARY KEY,
    grant_id TEXT NOT NULL,
    pool TEXT NOT NULL,
    position INTEGER NOT NULL
);

-- +goose Down
DROP TABLE grant_members;
DROP TABLE grants;
