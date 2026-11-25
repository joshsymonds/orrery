-- +goose Up
-- A release step's kind names the built-in mechanics orreryd carries out and checks for it, and params holds the
-- parameters of that kind as a JSON object. A step with no kind is an agent step, so every step stored before the
-- columns existed reads back with kind '' and params '{}', and runs as the agent step it was.
ALTER TABLE epic_release_steps ADD COLUMN kind TEXT NOT NULL DEFAULT '';
ALTER TABLE epic_release_steps ADD COLUMN params TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE epic_release_steps DROP COLUMN params;
ALTER TABLE epic_release_steps DROP COLUMN kind;
