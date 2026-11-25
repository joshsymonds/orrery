package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SpendQuery totals the finalized requests that started in [?, ?) for each seat. Its window is on started_at, so it
// reads through requests_started_at. A SUM ignores a NULL cost, which is an unknown cost, and a row with either cost
// unknown counts once as an unknown-cost request.
const SpendQuery = `SELECT seat,
	COALESCE(SUM(billed_cost_nano_usd), 0),
	COALESCE(SUM(list_cost_nano_usd), 0),
	COALESCE(SUM(CASE WHEN billed_cost_nano_usd IS NULL OR list_cost_nano_usd IS NULL THEN 1 ELSE 0 END), 0)
	FROM requests
	WHERE completed_at IS NOT NULL AND started_at >= ? AND started_at < ?
	GROUP BY seat
	ORDER BY seat`

// SeatSpend is what one seat spent over a span: the billed and list-price costs in nano US dollars, and the number of
// its requests whose cost is unknown.
type SeatSpend struct {
	Seat                string
	BilledCostNanoUSD   int64
	ListCostNanoUSD     int64
	UnknownCostRequests int64
}

// Spend returns what each seat spent over the half-open span [from, to), one entry for each seat, in unix seconds. It
// returns nil, and no error, when the ledger file is absent.
func (r *Reader) Spend(ctx context.Context, from, to int64) ([]SeatSpend, error) {
	db, ok, err := r.open(ctx)
	if err != nil || !ok {
		return nil, err
	}
	spend, err := spendIn(ctx, db, from, to)
	return spend, errors.Join(err, db.Close())
}

// spendIn runs SpendQuery over the span [from, to) on db and returns one SeatSpend for each seat it reads.
func spendIn(ctx context.Context, db *sql.DB, from, to int64) (spend []SeatSpend, err error) {
	rows, err := db.QueryContext(ctx, SpendQuery, from, to)
	if err != nil {
		return nil, fmt.Errorf("query spend: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	spend = []SeatSpend{}
	for rows.Next() {
		var s SeatSpend
		if err = rows.Scan(&s.Seat, &s.BilledCostNanoUSD, &s.ListCostNanoUSD, &s.UnknownCostRequests); err != nil {
			return nil, fmt.Errorf("scan spend: %w", err)
		}
		spend = append(spend, s)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read spend: %w", err)
	}
	return spend, nil
}
