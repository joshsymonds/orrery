package ledger

import (
	"context"
	"errors"
)

// ReadingsQuery is the placeholder query Readings runs. It selects no rows until Readings is implemented.
const ReadingsQuery = "SELECT 1 WHERE 0"

// Reading is one usage limit reading of an account and claim. A nil Utilization or Reset is a reading that gives none,
// and Observed is in unix seconds.
type Reading struct {
	Account     string
	Claim       string
	Utilization *float64
	Reset       *int64
	Status      string
	Observed    int64
	Dropped     bool
}

// Readings returns the usage limit readings, one for each account and claim. It returns nil, and no error, when the
// ledger file is absent.
func (r *Reader) Readings(ctx context.Context) ([]Reading, error) {
	db, ok, err := r.open(ctx)
	if err != nil || !ok {
		return nil, err
	}
	return []Reading{}, errors.Join(errNotImplemented, db.Close())
}
