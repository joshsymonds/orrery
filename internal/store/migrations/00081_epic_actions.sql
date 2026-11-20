-- +goose Up
-- An epic's actions are its moves and its owner or orreryd controls, one row each in seq order per epic. A move has
-- from_session and to_session, the steerer before and after it. A recipient names the session an action is owed to:
-- the steerer, for an action the owner made, and "" for one nobody is owed.
-- A decision's recipient names the session an owner's answer, note or kick is owed to. The epic_decisions no-update
-- trigger means the recipient is written by the INSERT only, so it is "" for every decision that exists now.
CREATE TABLE epic_actions (
    epic TEXT NOT NULL,
    seq INTEGER NOT NULL,
    kind TEXT NOT NULL,
    maker TEXT NOT NULL,
    maker_session TEXT NOT NULL DEFAULT '',
    from_session TEXT NOT NULL DEFAULT '',
    to_session TEXT NOT NULL DEFAULT '',
    recipient TEXT NOT NULL DEFAULT '',
    at INTEGER NOT NULL,
    PRIMARY KEY (epic, seq)
);
ALTER TABLE epic_decisions ADD COLUMN recipient TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE epic_decisions DROP COLUMN recipient;
DROP TABLE epic_actions;
