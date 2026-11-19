-- +goose Up
-- awaited is whether a headless session's last background-task report lists a task that runs a model: an agent, a
-- workflow or a teammate. background stays whether the report lists any task. A step's turn that ends while an awaited
-- task is listed is a return only when it is done, and a re-adopted step reads the flag here. A row stored before this
-- column does not say what its listed tasks run, so it takes its background flag: a step re-adopted from it waits as it
-- did, and never has a turn end it was waiting past taken as its return, until its session's next report sets the flag.
ALTER TABLE sessions ADD COLUMN awaited BOOLEAN NOT NULL DEFAULT 0;
UPDATE sessions SET awaited = background;

-- +goose Down
ALTER TABLE sessions DROP COLUMN awaited;
