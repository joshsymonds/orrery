package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// HoldRow is an owner hold on a pool member. Since is stored at whole-second precision.
type HoldRow struct {
	Member string
	Since  time.Time
}

// AddHold stores a hold on row.Member. A member already held keeps its stored row, so the first hold's since
// stands and no error is returned.
func (s *Store) AddHold(ctx context.Context, row HoldRow) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.InsertHold(ctx, sqlcgen.InsertHoldParams{
			Member: row.Member, Since: row.Since.Unix(),
		}); err != nil {
			return fmt.Errorf("insert hold on member %s: %w", row.Member, err)
		}
		return nil
	})
}

// EndHold removes the hold on member, so the member is free again. A member that is not held is not an error.
func (s *Store) EndHold(ctx context.Context, member string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeleteHold(ctx, member); err != nil {
			return fmt.Errorf("delete hold on member %s: %w", member, err)
		}
		return nil
	})
}

// Holds returns every hold ordered by member.
func (s *Store) Holds(ctx context.Context) ([]HoldRow, error) {
	holds, err := sqlcgen.New(s.db).ListHolds(ctx)
	if err != nil {
		return nil, fmt.Errorf("list holds: %w", err)
	}
	out := make([]HoldRow, 0, len(holds))
	for _, hold := range holds {
		out = append(out, HoldRow{Member: hold.Member, Since: time.Unix(hold.Since, 0)})
	}
	return out, nil
}
