-- +goose Up
-- A release no longer takes a slot: every clean epic releases at once, so the table that held the repositories' slots
-- goes. Nothing else names it, and no other table changes, so every epic keeps its phase, park and release runs.
DROP TABLE epic_release_slots;

-- +goose Down
-- The table is recreated empty, in the shape 00083_release_slot_outside.sql gave it, with its partial unique index.
CREATE TABLE epic_release_slots (
    repository TEXT PRIMARY KEY,
    epic TEXT NOT NULL DEFAULT '',
    holder_kind TEXT NOT NULL DEFAULT 'epic' CHECK (holder_kind IN ('epic', 'outside')),
    label TEXT NOT NULL DEFAULT '',
    deadline INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX epic_release_slots_epic ON epic_release_slots (epic) WHERE holder_kind = 'epic';
