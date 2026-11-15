-- +goose Up
CREATE INDEX events_turn_end ON events (session_id, at)
WHERE kind = 'session_changed' AND json_extract(payload, '$.fields.state') = 'done';

-- +goose Down
DROP INDEX events_turn_end;
