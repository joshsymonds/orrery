-- +goose Up
-- The sync of origin's main is removed, so an epic's base is the commit of origin's main its last sync merged, as
-- synced records it. The stored base stays where the sync left it. The epic's own change and its reviews start from
-- the synced commit, now the base, so origin's change is not read as the epic's own.
UPDATE epics SET base = synced WHERE synced <> '';

-- A sync run still running has no sync to finish, so it ends skipped, with the output the removal leaves. A passed sync
-- run, and every run of another kind, is left as it is.
UPDATE epic_node_runs SET
    state = 'ended',
    outcome = 'skipped',
    output = 'the sync was removed at deploy',
    ended = CAST(strftime('%s', 'now') AS INTEGER)
WHERE kind = 'sync' AND state = 'running';

-- +goose Down
-- Nothing is undone. The base this Up moved is overwritten, and no column keeps the base an epic had before, so the
-- fold of origin's main cannot be restored. The sync runs it ended stay ended as skipped: a sync is removed, so no run
-- of kind sync can run again to finish them.
