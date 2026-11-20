-- +goose Up
-- A repository's release slot is held by an epic, as 00038_release.sql made it, or by an outside releaser under a label
-- and a deadline in Unix seconds. An outside row names no epic, so its epic is '', and the partial unique index allows
-- any number of them: epic is unique among the epic rows only. Every existing row is an epic's hold. SQLite cannot
-- drop a UNIQUE constraint in place, so the table is rebuilt with its rows copied. No trigger names it:
-- 00072_epic_revisions_table.sql dropped the triggers.
ALTER TABLE epic_release_slots RENAME TO epic_release_slots_before;
CREATE TABLE epic_release_slots (
    repository TEXT PRIMARY KEY,
    epic TEXT NOT NULL DEFAULT '',
    holder_kind TEXT NOT NULL DEFAULT 'epic' CHECK (holder_kind IN ('epic', 'outside')),
    label TEXT NOT NULL DEFAULT '',
    deadline INTEGER NOT NULL DEFAULT 0
);
INSERT INTO epic_release_slots (repository, epic, holder_kind)
SELECT repository, epic, 'epic' FROM epic_release_slots_before;
DROP TABLE epic_release_slots_before;
CREATE UNIQUE INDEX epic_release_slots_epic ON epic_release_slots (epic) WHERE holder_kind = 'epic';

-- +goose Down
-- The table is rebuilt in the shape 00038_release.sql gave it, with the epic rows kept and the outside rows dropped:
-- an outside hold has no epic to name.
ALTER TABLE epic_release_slots RENAME TO epic_release_slots_outside;
CREATE TABLE epic_release_slots (
    repository TEXT PRIMARY KEY,
    epic TEXT NOT NULL UNIQUE
);
INSERT INTO epic_release_slots (repository, epic)
SELECT repository, epic FROM epic_release_slots_outside WHERE holder_kind = 'epic';
DROP TABLE epic_release_slots_outside;
