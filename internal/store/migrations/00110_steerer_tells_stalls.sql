-- +goose Up
-- A tell records whether its epic's steerer has been sent it. A tell stored before this migration is marked delivered
-- here, so no steerer is sent a tell the store held before it. A tell stored after it starts undelivered.
ALTER TABLE epic_tells ADD COLUMN steerer_delivered INTEGER NOT NULL DEFAULT 0;
UPDATE epic_tells SET steerer_delivered = 1;

-- epic_stalls holds one row per stall the stall check found in an epic, numbered by seq: the time it was found, the
-- epic's counts of tasks, node runs and attempts, and actions at that time, and whether the epic parked for it. Like
-- every epic table, its epic column moves the epic's revision through the store's pre-update hook, so no trigger is
-- made (see 00072_epic_revisions_table.sql).
CREATE TABLE epic_stalls (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    at INTEGER NOT NULL,
    tasks INTEGER NOT NULL,
    runs INTEGER NOT NULL,
    actions INTEGER NOT NULL,
    parked INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (epic, seq)
);

-- +goose Down
-- The stall rows and the delivered mark go. The tells keep their text, seq and told mark.
DROP TABLE epic_stalls;
ALTER TABLE epic_tells DROP COLUMN steerer_delivered;
