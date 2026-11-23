-- +goose Up
-- The host an epic's work and a step's work live on: '' for a row stored before hosts, which the controller fills with its
-- own name when it opens the store (AdoptHost). A step copies its epic's host when it is started, so each step names the
-- host of its epic. Each column is added at the end of its table with a default, so every stored row reads back with no host.
ALTER TABLE epics ADD COLUMN host TEXT NOT NULL DEFAULT '';
ALTER TABLE steps ADD COLUMN host TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE steps DROP COLUMN host;
ALTER TABLE epics DROP COLUMN host;
