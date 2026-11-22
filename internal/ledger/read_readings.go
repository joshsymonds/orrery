package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ReadingsQuery selects every usage limit reading, one row for each account and claim, in account then claim order. It
// reads only limit_readings, so it never reads requests.
const ReadingsQuery = `SELECT account, claim, utilization, reset_at, status, observed_at, dropped
	FROM limit_readings
	ORDER BY account, claim`

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
	readings, err := queryReadings(ctx, db)
	return readings, errors.Join(err, db.Close())
}

// queryReadings runs ReadingsQuery on db and scans each reading it returns.
func queryReadings(ctx context.Context, db *sql.DB) (readings []Reading, err error) {
	rows, err := db.QueryContext(ctx, ReadingsQuery)
	if err != nil {
		return nil, fmt.Errorf("query readings: %w", err)
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	readings = []Reading{}
	for rows.Next() {
		var reading Reading
		var utilization sql.NullFloat64
		var reset sql.NullInt64
		if err = rows.Scan(
			&reading.Account, &reading.Claim, &utilization, &reset,
			&reading.Status, &reading.Observed, &reading.Dropped,
		); err != nil {
			return nil, fmt.Errorf("scan reading: %w", err)
		}
		if utilization.Valid {
			value := utilization.Float64
			reading.Utilization = &value
		}
		if reset.Valid {
			value := reset.Int64
			reading.Reset = &value
		}
		readings = append(readings, reading)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read readings: %w", err)
	}
	return readings, nil
}
