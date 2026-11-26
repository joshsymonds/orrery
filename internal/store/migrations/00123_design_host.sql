-- +goose Up
-- The host a design's steps run on. '' is the controller's own host, so a design stored before this column was added
-- reads back with the empty host, which keeps its meaning.
ALTER TABLE designs ADD COLUMN host TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE designs DROP COLUMN host;
