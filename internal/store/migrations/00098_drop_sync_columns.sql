-- +goose Up
-- The sync of origin's main is removed, so nothing reads the commit it merged, the epic's last read of origin's main,
-- or the state and time of that read. 00097_epic_base_legacy_sync.sql has folded synced into base first.
ALTER TABLE epics DROP COLUMN synced;
ALTER TABLE epics DROP COLUMN origin_read_base;
ALTER TABLE epics DROP COLUMN origin_read_commit;
ALTER TABLE epics DROP COLUMN origin_read_state;
ALTER TABLE epics DROP COLUMN origin_read_at;

-- +goose Down
-- The columns come back as 00053_epic_synced.sql and 00054_origin_read.sql defined them, empty and zero. No sync
-- commit or origin read is restored, since the Up dropped them.
ALTER TABLE epics ADD COLUMN synced TEXT NOT NULL DEFAULT '';
ALTER TABLE epics ADD COLUMN origin_read_base TEXT NOT NULL DEFAULT '';
ALTER TABLE epics ADD COLUMN origin_read_commit TEXT NOT NULL DEFAULT '';
ALTER TABLE epics ADD COLUMN origin_read_state TEXT NOT NULL DEFAULT '';
ALTER TABLE epics ADD COLUMN origin_read_at INTEGER NOT NULL DEFAULT 0;
