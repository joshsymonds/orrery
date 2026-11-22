package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Claim is one usage limit claim a request reported. A nil Utilization or Reset is a reading that gives none.
type Claim struct {
	Name        string
	Utilization *float64
	Reset       *int64
	Status      string
}

// Start is a request as it starts, before its tokens and cost are known.
type Start struct {
	Account, CallerAccount, Seat, Context, RequestedModel, ClaudeSession, AgentID, AgentType string
	StartedAt                                                                                int64
}

// Finish is a request as it completes. A nil BilledCostNanoUSD or ListCostNanoUSD is an unknown cost.
type Finish struct {
	CompletedAt       int64
	ResolvedModel     string
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheWriteTokens  int64
	BilledCostNanoUSD *int64
	ListCostNanoUSD   *int64
	Claims            []Claim
}

// insertRequestSQL stores a request as it starts. Its completion, tokens and costs keep the column defaults until
// Finish sets them.
const insertRequestSQL = `INSERT INTO requests
	(started_at, account, seat, context, requested_model, claude_session, agent_id, agent_type)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

// finishRequestSQL sets a request's completion, tokens and costs. It matches only a request not yet finalized, so an
// id with no row and an id already finalized both match nothing.
const finishRequestSQL = `UPDATE requests SET
	completed_at = ?, resolved_model = ?, input_tokens = ?, output_tokens = ?, cache_read_tokens = ?,
	cache_write_tokens = ?, billed_cost_nano_usd = ?, list_cost_nano_usd = ?
	WHERE id = ? AND completed_at IS NULL`

// insertClaimSQL stores one usage limit claim a request reported.
const insertClaimSQL = `INSERT INTO request_claims (request_id, claim, utilization, reset_at, status)
	VALUES (?, ?, ?, ?, ?)`

// Start records the start of a request and returns the id the request is known by. The request is stored under
// Start.Account, or under Start.CallerAccount when Account is empty.
func (l *Ledger) Start(ctx context.Context, start Start) (int64, error) {
	account := start.Account
	if account == "" {
		account = start.CallerAccount
	}
	result, err := l.db.ExecContext(ctx, insertRequestSQL,
		start.StartedAt, account, start.Seat, start.Context, start.RequestedModel, start.ClaudeSession,
		start.AgentID, start.AgentType)
	if err != nil {
		return 0, fmt.Errorf("insert request: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read inserted request id: %w", err)
	}
	return id, nil
}

// Finish records the completion of the request id, with its tokens, its costs and the claims it reported, and it
// writes the account's usage limit readings from those claims. It runs in one transaction, and it refuses an id with
// no row, or a request already finalized, with an error and no change.
func (l *Ledger) Finish(ctx context.Context, id int64, finish Finish) error {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin finish: %w", err)
	}
	if err = finishIn(ctx, tx, id, finish); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err = writeReadingsIn(ctx, tx, id, finish); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit finish: %w", err)
	}
	return nil
}

// finishIn finalizes the request id within tx and stores its claims. It returns an error, for the caller to roll tx
// back, when no open request has the id or a statement fails.
func finishIn(ctx context.Context, tx *sql.Tx, id int64, finish Finish) error {
	result, err := tx.ExecContext(ctx, finishRequestSQL,
		finish.CompletedAt, finish.ResolvedModel, finish.InputTokens, finish.OutputTokens,
		finish.CacheReadTokens, finish.CacheWriteTokens, finish.BilledCostNanoUSD, finish.ListCostNanoUSD, id)
	if err != nil {
		return fmt.Errorf("update request %d: %w", id, err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated request count: %w", err)
	}
	if updated == 0 {
		return fmt.Errorf("request %d: no such request, or it is already finalized", id)
	}
	for _, c := range finish.Claims {
		if _, err = tx.ExecContext(ctx, insertClaimSQL, id, c.Name, c.Utilization, c.Reset, c.Status); err != nil {
			return fmt.Errorf("insert claim %q for request %d: %w", c.Name, id, err)
		}
	}
	return nil
}
