-- +goose Up
-- The host a notice's pane is on: the orrery name of the host that runs the pane. It is set with pane and only with
-- pane, so a row for a session, epic, usage limit or alert has host ''. A row stored before the column existed reads as
-- '', and the controller names each such row of a pane as its own orrery name when it opens the store
-- (store.Store.NameUnhostedPaneRows).

-- pushed_notices is rebuilt so a pane's push is keyed by its host too: two hosts' panes with the same id keep two
-- rows.
ALTER TABLE pushed_notices RENAME TO pushed_notices_old;
CREATE TABLE pushed_notices (
    pane TEXT NOT NULL,
    session TEXT NOT NULL,
    time INTEGER NOT NULL,
    host TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (host, pane, session)
);
INSERT INTO pushed_notices (pane, session, time, host) SELECT pane, session, time, '' FROM pushed_notices_old;
DROP TABLE pushed_notices_old;

ALTER TABLE pending_notices ADD COLUMN host TEXT NOT NULL DEFAULT '';

ALTER TABLE notice_history ADD COLUMN host TEXT NOT NULL DEFAULT '';
DROP INDEX notice_history_key;
CREATE INDEX notice_history_key ON notice_history (host, pane, session, epic, limit_key, alert, id);

-- +goose Down
-- The shapes come back as 00012_pushed_notices.sql, 00076_notice_history.sql and 00010_pending_notices.sql defined
-- them. A pane's push kept under more than one host is kept once, at its newest time, since the old key has no host
-- to tell the rows apart.
DROP INDEX notice_history_key;
CREATE INDEX notice_history_key ON notice_history (pane, session, epic, limit_key, alert, id);
ALTER TABLE notice_history DROP COLUMN host;

ALTER TABLE pending_notices DROP COLUMN host;

ALTER TABLE pushed_notices RENAME TO pushed_notices_old;
CREATE TABLE pushed_notices (
    pane TEXT NOT NULL,
    session TEXT NOT NULL,
    time INTEGER NOT NULL,
    PRIMARY KEY (pane, session)
);
INSERT INTO pushed_notices (pane, session, time)
    SELECT pane, session, MAX(time) FROM pushed_notices_old GROUP BY pane, session;
DROP TABLE pushed_notices_old;
