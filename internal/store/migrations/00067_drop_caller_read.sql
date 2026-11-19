-- +goose Up
-- No read suppresses a step's end, so every end comes back to its caller and the read mark goes.
-- steps_unread names caller_read, which SQLite will not drop under an index, so it is made again without it: the ended
-- steps owed to a caller, which the caller events list on every pass, are still a few rows of the whole table.
DROP INDEX steps_unread;
ALTER TABLE steps DROP COLUMN caller_read;
CREATE INDEX steps_unread ON steps (caller, ended, id) WHERE end_value <> '' AND caller <> '';

-- +goose Down
DROP INDEX steps_unread;
ALTER TABLE steps ADD COLUMN caller_read INTEGER NOT NULL DEFAULT 0;
CREATE INDEX steps_unread ON steps (caller, ended, id) WHERE end_value <> '' AND caller <> '' AND caller_read = 0;
