-- +goose Up
-- Each request's list-price cost by token kind, in nano-USD, from the gateway's rate card. The four sum to
-- list_cost_nano_usd. Cache writes at the 5-minute and the 1-hour price both count in list_cache_write_nano_usd. NULL
-- where the request has no list cost, as list_cost_nano_usd is.
ALTER TABLE requests ADD COLUMN list_input_nano_usd INTEGER NULL;
ALTER TABLE requests ADD COLUMN list_output_nano_usd INTEGER NULL;
ALTER TABLE requests ADD COLUMN list_cache_read_nano_usd INTEGER NULL;
ALTER TABLE requests ADD COLUMN list_cache_write_nano_usd INTEGER NULL;

-- +goose Down
ALTER TABLE requests DROP COLUMN list_cache_write_nano_usd;
ALTER TABLE requests DROP COLUMN list_cache_read_nano_usd;
ALTER TABLE requests DROP COLUMN list_output_nano_usd;
ALTER TABLE requests DROP COLUMN list_input_nano_usd;
