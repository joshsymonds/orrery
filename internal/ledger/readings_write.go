package ledger

import (
	"context"
	"database/sql"
	"fmt"
)

// upsertReadingSQL stores one claim's reading of an account. It replaces the stored reading only when that reading is
// not newer than the answer, and a reading it replaces is no longer dropped, since the answer carries the claim.
const upsertReadingSQL = `INSERT INTO limit_readings
	(account, claim, utilization, reset_at, status, observed_at, request_id, dropped)
	VALUES (?, ?, ?, ?, ?, ?, ?, 0)
	ON CONFLICT (account, claim) DO UPDATE SET
	utilization = excluded.utilization, reset_at = excluded.reset_at, status = excluded.status,
	observed_at = excluded.observed_at, request_id = excluded.request_id, dropped = 0
	WHERE limit_readings.observed_at <= excluded.observed_at`

// recordClaimModelSQL records that a model has carried a claim for an account.
const recordClaimModelSQL = `INSERT OR IGNORE INTO limit_claim_models (account, claim, model) VALUES (?, ?, ?)`

// dropOmittedSQL marks dropped each stored reading of the account that the request omits, when the request's model has
// carried the claim before and the request is not older than the reading. The claims the request carries are the rows
// finishIn stored for it in request_claims, so a reading they name is left alone.
const dropOmittedSQL = `UPDATE limit_readings SET dropped = 1
	WHERE account = ? AND observed_at <= ?
	AND NOT EXISTS (SELECT 1 FROM request_claims c WHERE c.request_id = ? AND c.claim = limit_readings.claim)
	AND EXISTS (SELECT 1 FROM limit_claim_models m
		WHERE m.account = limit_readings.account AND m.claim = limit_readings.claim AND m.model = ?)`

// writeReadingsIn writes the usage limit readings of the request id, finalized as finish, within tx. The request's
// stored account owns them. It upserts each claim the request carries and records the request's model as having
// carried it, then marks dropped each stored reading the request omits that its model has carried.
func writeReadingsIn(ctx context.Context, tx *sql.Tx, id int64, finish Finish) error {
	var account string
	if err := tx.QueryRowContext(ctx, `SELECT account FROM requests WHERE id = ?`, id).Scan(&account); err != nil {
		return fmt.Errorf("read account of request %d: %w", id, err)
	}
	for _, claim := range finish.Claims {
		if _, err := tx.ExecContext(ctx, upsertReadingSQL, account, claim.Name, claim.Utilization, claim.Reset,
			claim.Status, finish.CompletedAt, id); err != nil {
			return fmt.Errorf("upsert reading %q for request %d: %w", claim.Name, id, err)
		}
		if _, err := tx.ExecContext(ctx, recordClaimModelSQL, account, claim.Name, finish.ResolvedModel); err != nil {
			return fmt.Errorf("record model of claim %q for request %d: %w", claim.Name, id, err)
		}
	}
	if _, err := tx.ExecContext(ctx, dropOmittedSQL,
		account, finish.CompletedAt, id, finish.ResolvedModel); err != nil {
		return fmt.Errorf("drop readings omitted by request %d: %w", id, err)
	}
	return nil
}
