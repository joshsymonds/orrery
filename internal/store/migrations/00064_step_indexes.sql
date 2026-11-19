-- +goose Up
-- steps holds every step's prompt, result, checks and verdicts, so a scan of it reads the whole history. The
-- interpreter reads the steps labeled with an epic's review, closure and release runs on every decision, and the build
-- reads a task's steps for each of its prompts, so a step's epic and task labels are searched by key, in the order
-- the reads return them.
CREATE INDEX steps_epic_task ON steps (epic, task, started, id);

-- The open steps, which the limit notices list on every pass and the step controls read in id order, are a few rows
-- of the whole table.
CREATE INDEX steps_open ON steps (id) WHERE end_value = '';

-- The ended steps whose caller has not read their end, which the caller events list on every pass, are a few rows of
-- the whole table too. A step with no caller is owed to no session.
CREATE INDEX steps_unread ON steps (caller, ended, id) WHERE end_value <> '' AND caller <> '' AND caller_read = 0;

-- +goose Down
DROP INDEX steps_unread;
DROP INDEX steps_open;
DROP INDEX steps_epic_task;
