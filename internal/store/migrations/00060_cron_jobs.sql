-- +goose Up
-- The cron jobs of headless sessions, which orreryd keeps so a session can sleep between fires. job_id is the job's id
-- and session the headless session's id. tool_use_id is the id of the tool call that created the job, unique within its
-- session, so a call that is retried creates no second job. cron is the job's schedule and prompt the text a fire sends.
-- recurring is 1 for a job that fires again and 0 for a one-shot job. created_at and last_fire_at are unix seconds, and
-- last_fire_at is 0 until the job first fires.
CREATE TABLE cron_jobs (
    job_id TEXT PRIMARY KEY,
    session TEXT NOT NULL,
    tool_use_id TEXT NOT NULL,
    cron TEXT NOT NULL,
    prompt TEXT NOT NULL,
    recurring INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    last_fire_at INTEGER NOT NULL DEFAULT 0
);

-- cron_jobs_tool_use makes a session's tool call create at most one job. cron_jobs_session lists a session's jobs.
CREATE UNIQUE INDEX cron_jobs_tool_use ON cron_jobs (session, tool_use_id);
CREATE INDEX cron_jobs_session ON cron_jobs (session);

-- +goose Down
DROP TABLE cron_jobs;
