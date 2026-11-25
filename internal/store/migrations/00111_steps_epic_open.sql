-- +goose Up
-- The open steps of an epic, whatever their task label, are read with every read of its flow, which the interpreter
-- makes on each decision and the stall check makes again, so the stall check counts every step of the epic that has not
-- ended. They are a few rows of the epic's steps, so they are searched by the epic among the open steps alone, in the
-- order the read returns them, and the read takes their ids from the index with no step's row.
CREATE INDEX steps_epic_open ON steps (epic, started, id) WHERE end_value = '';

-- +goose Down
DROP INDEX steps_epic_open;
