package ledger

import (
	"context"
	"errors"
)

// SpendQuery is the placeholder query Spend runs. It selects no rows until Spend is implemented.
const SpendQuery = "SELECT 1 WHERE 0"

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
func (r *Reader) Spend(ctx context.Context, _, _ int64) ([]SeatSpend, error) {
	db, ok, err := r.open(ctx)
	if err != nil || !ok {
		return nil, err
	}
	return []SeatSpend{}, errors.Join(errNotImplemented, db.Close())
}
