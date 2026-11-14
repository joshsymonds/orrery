-- +goose Up
ALTER TABLE steps ADD COLUMN checkout_head TEXT NOT NULL DEFAULT '';
ALTER TABLE steps ADD COLUMN checkout_hash TEXT NOT NULL DEFAULT '';

ALTER TABLE evidence_runs ADD COLUMN stderr_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE evidence_runs ADD COLUMN stderr_bytes INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE evidence_runs DROP COLUMN stderr_bytes;
ALTER TABLE evidence_runs DROP COLUMN stderr_hash;
ALTER TABLE steps DROP COLUMN checkout_hash;
ALTER TABLE steps DROP COLUMN checkout_head;
