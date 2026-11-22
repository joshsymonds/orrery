package ledger

import (
	"context"
	"errors"
)

// SessionTotalsQuery is the placeholder query SessionTotals runs. It selects no rows until SessionTotals is
// implemented.
const SessionTotalsQuery = "SELECT 1 WHERE 0"

// SessionTotal is the requests and tokens one session made for one model and agent type. The cost fields are in nano
// US dollars.
type SessionTotal struct {
	Model             string
	AgentType         string
	Requests          int64
	InputTokens       int64
	OutputTokens      int64
	CacheReadTokens   int64
	CacheWriteTokens  int64
	BilledCostNanoUSD int64
	ListCostNanoUSD   int64
}

// SessionTotals returns the totals of the requests the Claude session made, one for each model and agent type. It
// returns nil, and no error, when the ledger file is absent.
func (r *Reader) SessionTotals(ctx context.Context, _ string) ([]SessionTotal, error) {
	db, ok, err := r.open(ctx)
	if err != nil || !ok {
		return nil, err
	}
	return []SessionTotal{}, errors.Join(errNotImplemented, db.Close())
}
