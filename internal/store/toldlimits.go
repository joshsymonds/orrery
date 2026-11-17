package store

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ToldLimits returns each usage limit that has a told name, mapped to its told names sorted. A limit with no told name
// is absent from the map.
func (s *Store) ToldLimits(ctx context.Context) (map[string][]string, error) {
	rows, err := sqlcgen.New(s.db).ListToldLimitSteps(ctx)
	if err != nil {
		return nil, fmt.Errorf("list told limit names: %w", err)
	}
	out := make(map[string][]string)
	for _, row := range rows {
		out[row.LimitKey] = append(out[row.LimitKey], row.Step)
	}
	return out, nil
}

// TellLimit replaces the told names of limit with names in one write. A name repeated in names is told once. An empty
// names leaves limit with no told name, the same as ForgetLimit.
func (s *Store) TellLimit(ctx context.Context, limit string, names []string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeleteToldLimit(ctx, limit); err != nil {
			return fmt.Errorf("clear told names of limit %q: %w", limit, err)
		}
		for _, name := range names {
			if err := queries.InsertToldLimitStep(ctx, sqlcgen.InsertToldLimitStepParams{
				LimitKey: limit, Step: name,
			}); err != nil {
				return fmt.Errorf("tell name %q of limit %q: %w", name, limit, err)
			}
		}
		return nil
	})
}

// ForgetLimit removes every told name of limit, so limit is untold. A limit that is not told is not an error.
func (s *Store) ForgetLimit(ctx context.Context, limit string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeleteToldLimit(ctx, limit); err != nil {
			return fmt.Errorf("forget limit %q: %w", limit, err)
		}
		return nil
	})
}
