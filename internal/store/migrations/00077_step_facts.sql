-- +goose Up
-- A step's line counts, the facts its end carries about the work it did: lines_counted is 1 once a done return of an
-- owned writing step was counted, and lines_files, lines_added and lines_removed are that count's files, added and
-- removed lines. A step no count was taken of holds zeros in all four. A return that carries a count writes the four
-- with it and a later count replaces them, and a return that carries none leaves them as they were.
ALTER TABLE steps ADD COLUMN lines_counted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE steps ADD COLUMN lines_files INTEGER NOT NULL DEFAULT 0;
ALTER TABLE steps ADD COLUMN lines_added INTEGER NOT NULL DEFAULT 0;
ALTER TABLE steps ADD COLUMN lines_removed INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE steps DROP COLUMN lines_removed;
ALTER TABLE steps DROP COLUMN lines_added;
ALTER TABLE steps DROP COLUMN lines_files;
ALTER TABLE steps DROP COLUMN lines_counted;
