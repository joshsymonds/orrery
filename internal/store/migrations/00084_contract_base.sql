-- +goose Up
-- A contract's accepted state records the base its epic starts from: the commit origin's main names when the contract is
-- accepted (enginehost.contractService.AcceptContract). Every other state stores '', and so does every state stored
-- before this migration, so those rows read as they did.
ALTER TABLE contract_states ADD COLUMN base TEXT NOT NULL DEFAULT '';

-- +goose Down
-- The column is dropped. Its states keep their other columns, and each accepted state loses the base it stored.
ALTER TABLE contract_states DROP COLUMN base;
