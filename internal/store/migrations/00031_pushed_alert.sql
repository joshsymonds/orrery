-- +goose Up
CREATE TABLE pushed_alert_notices (
    alert TEXT PRIMARY KEY,
    time INTEGER NOT NULL
);

-- +goose Down
DROP TABLE pushed_alert_notices;
