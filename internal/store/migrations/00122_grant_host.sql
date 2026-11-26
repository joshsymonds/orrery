-- +goose Up
-- The host a grant was granted for. '' is the controller's own host, so a grant stored before this column was added
-- reads back with the empty host, which keeps its meaning.
ALTER TABLE grants ADD COLUMN host TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE grants DROP COLUMN host;
