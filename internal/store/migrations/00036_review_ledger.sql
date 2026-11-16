-- +goose Up
CREATE TABLE epic_reviews (
    epic TEXT PRIMARY KEY,
    frozen TEXT NOT NULL,
    clean TEXT NOT NULL DEFAULT ''
);

CREATE TABLE epic_review_verdicts (
    epic TEXT NOT NULL,
    position INTEGER NOT NULL,
    reviewer TEXT NOT NULL,
    step TEXT NOT NULL,
    verdict TEXT NOT NULL,
    reason TEXT NOT NULL,
    PRIMARY KEY (epic, position)
);

CREATE TABLE epic_ledger (
    epic TEXT NOT NULL,
    id TEXT NOT NULL,
    position INTEGER NOT NULL,
    reviewer TEXT NOT NULL,
    finding TEXT NOT NULL,
    claim TEXT NOT NULL,
    citation TEXT NOT NULL,
    location TEXT NOT NULL,
    evidence TEXT NOT NULL,
    verify_by TEXT NOT NULL,
    step TEXT NOT NULL,
    result TEXT NOT NULL,
    unverified INTEGER NOT NULL,
    reason TEXT NOT NULL,
    PRIMARY KEY (epic, id)
);

CREATE TABLE epic_review_rounds (
    epic TEXT NOT NULL,
    round INTEGER NOT NULL,
    revision TEXT NOT NULL,
    tasks TEXT NOT NULL,
    outcome TEXT NOT NULL,
    PRIMARY KEY (epic, round)
);

CREATE TABLE epic_closures (
    epic TEXT NOT NULL,
    round INTEGER NOT NULL,
    finding TEXT NOT NULL,
    step TEXT NOT NULL,
    result TEXT NOT NULL,
    reason TEXT NOT NULL,
    PRIMARY KEY (epic, round, finding)
);

CREATE TABLE epic_review_holds (
    epic TEXT NOT NULL,
    step TEXT NOT NULL,
    PRIMARY KEY (epic, step)
);

-- +goose Down
DROP TABLE epic_review_holds;
DROP TABLE epic_closures;
DROP TABLE epic_review_rounds;
DROP TABLE epic_ledger;
DROP TABLE epic_review_verdicts;
DROP TABLE epic_reviews;
