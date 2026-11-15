package store

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ToldLimits returns each usage limit that has a told step, mapped to its told step ids sorted. A limit with no told
// step is absent from the map.
func (s *Store) ToldLimits(ctx context.Context) (map[string][]string, error) {
	rows, err := sqlcgen.New(s.db).ListToldLimitSteps(ctx)
	if err != nil {
		return nil, fmt.Errorf("list told limit steps: %w", err)
	}
	out := make(map[string][]string)
	for _, row := range rows {
		out[row.LimitKey] = append(out[row.LimitKey], row.Step)
	}
	return out, nil
}

// TellLimit replaces the told steps of limit with steps in one write. A step repeated in steps is told once. An empty
// steps leaves limit with no told step, the same as ForgetLimit.
func (s *Store) TellLimit(ctx context.Context, limit string, steps []string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeleteToldLimit(ctx, limit); err != nil {
			return fmt.Errorf("clear told steps of limit %q: %w", limit, err)
		}
		for _, step := range steps {
			if err := queries.InsertToldLimitStep(ctx, sqlcgen.InsertToldLimitStepParams{
				LimitKey: limit, Step: step,
			}); err != nil {
				return fmt.Errorf("tell step %q of limit %q: %w", step, limit, err)
			}
		}
		return nil
	})
}

// ForgetLimit removes every told step of limit, so limit is untold. A limit that is not told is not an error.
func (s *Store) ForgetLimit(ctx context.Context, limit string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeleteToldLimit(ctx, limit); err != nil {
			return fmt.Errorf("forget limit %q: %w", limit, err)
		}
		return nil
	})
}
