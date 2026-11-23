-- +goose Up
-- The headless listing's reply, newest reply time, first prompt and request id, stored on each session row so the
-- listing reads them without scanning events. Engine writes keep them current (see ApplyEngine): latest_reply and
-- newest_reply_at follow the main-conversation assistant text blocks, first_prompt_text the first prompt, and
-- latest_request_id the newest request that was not refused. Each column starts at its empty value. The backfill
-- computes each one from the events the store holds now, with the expressions ListHeadlessSessions reads them by, so
-- a migrated store holds what the listing computes from its events.
ALTER TABLE sessions ADD COLUMN latest_reply TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN newest_reply_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN first_prompt_text TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN latest_request_id TEXT NOT NULL DEFAULT '';

UPDATE sessions SET
    latest_reply = CAST(COALESCE((
        SELECT json_extract(events.payload, '$.text') FROM events
        WHERE events.session_id = sessions.id AND events.kind = 'engine_block'
          AND json_extract(events.payload, '$.kind') = 'text'
          AND json_extract(events.payload, '$.role') = 'assistant'
          AND IFNULL(json_extract(events.payload, '$.parentToolUseId'), '') = ''
        ORDER BY events.at DESC, events.seq DESC LIMIT 1
    ), '') AS TEXT),
    newest_reply_at = CAST(COALESCE((
        SELECT MAX(events.at) FROM events
        WHERE events.session_id = sessions.id AND (
            (events.kind = 'engine_block' AND json_extract(events.payload, '$.kind') = 'text'
             AND json_extract(events.payload, '$.role') = 'assistant'
             AND IFNULL(json_extract(events.payload, '$.parentToolUseId'), '') = '')
            OR (events.kind = 'engine_request' AND json_extract(events.payload, '$.refused') IS NOT 1)
        )
    ), 0) AS INTEGER),
    first_prompt_text = CAST(COALESCE((
        SELECT json_extract(events.payload, '$.text') FROM events
        WHERE events.session_id = sessions.id AND events.kind = 'engine_prompt'
        ORDER BY events.seq LIMIT 1
    ), '') AS TEXT),
    latest_request_id = CAST(COALESCE((
        SELECT json_extract(events.payload, '$.requestId') FROM events
        WHERE events.session_id = sessions.id AND events.kind = 'engine_request'
          AND json_extract(events.payload, '$.refused') IS NOT 1
        ORDER BY events.seq DESC LIMIT 1
    ), '') AS TEXT);

-- +goose Down
ALTER TABLE sessions DROP COLUMN latest_request_id;
ALTER TABLE sessions DROP COLUMN first_prompt_text;
ALTER TABLE sessions DROP COLUMN newest_reply_at;
ALTER TABLE sessions DROP COLUMN latest_reply;
