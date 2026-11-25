-- +goose Up
-- held is whether a headless session's working state is held by a background task: its last turn ended while a task
-- that runs a model (an agent, a workflow or a teammate) was listed, so no turn runs and the session is not done. The
-- turn claude starts when that task ends is the one that ends it. Any later state write clears it. A row stored before
-- this column was never held, since a turn end then always wrote done.
ALTER TABLE sessions ADD COLUMN held BOOLEAN NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE sessions DROP COLUMN held;
