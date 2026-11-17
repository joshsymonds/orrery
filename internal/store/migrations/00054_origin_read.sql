-- +goose Up
-- The epic's last read of origin's main, which a sync of origin's main starts from: the stored base it was read for,
-- origin's commit it found, '' when it found none, what it found of that commit, and when it was read, in unix seconds,
-- 0 while origin's main was never read. A read for another stored base, or one older than the sync interval, is read
-- again before a sync starts, so a sync starts only when the read finds origin's main ahead of the stored base.
ALTER TABLE epics ADD COLUMN origin_read_base TEXT NOT NULL DEFAULT '';
ALTER TABLE epics ADD COLUMN origin_read_commit TEXT NOT NULL DEFAULT '';
ALTER TABLE epics ADD COLUMN origin_read_state TEXT NOT NULL DEFAULT '';
ALTER TABLE epics ADD COLUMN origin_read_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE epics DROP COLUMN origin_read_at;
ALTER TABLE epics DROP COLUMN origin_read_state;
ALTER TABLE epics DROP COLUMN origin_read_commit;
ALTER TABLE epics DROP COLUMN origin_read_base;
