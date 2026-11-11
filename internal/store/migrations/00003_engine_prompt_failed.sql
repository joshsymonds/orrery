-- +goose Up
CREATE TABLE engine_prompt_failed (
    session_id TEXT NOT NULL,
    prompt_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    PRIMARY KEY (session_id, prompt_id)
);

-- +goose Down
DROP TABLE engine_prompt_failed;
