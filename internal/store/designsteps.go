package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// AddDesignStep stores step as a step of its design, with its Created as the time it was started. A step is stored
// once; a step id that is already stored is refused by the table's primary key, and nothing is stored.
func (s *Store) AddDesignStep(ctx context.Context, step DesignStep) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.InsertDesignStep(ctx, sqlcgen.InsertDesignStepParams{
			Step: step.Step, Design: step.Design, Kind: step.Kind, Subject: step.Subject,
			Repository: step.Repository, Revision: step.Revision, Delta: step.Delta, Created: step.Created.Unix(),
		}); err != nil {
			return fmt.Errorf("insert step %s of design %s: %w", step.Step, step.Design, err)
		}
		return nil
	})
}

// DesignSteps returns the steps stored against the design, oldest first and ties by step id. It returns none for a
// design no step names.
func (s *Store) DesignSteps(ctx context.Context, design string) ([]DesignStep, error) {
	rows, err := sqlcgen.New(s.read).ListDesignSteps(ctx, design)
	if err != nil {
		return nil, fmt.Errorf("list steps of design %s: %w", design, err)
	}
	steps := make([]DesignStep, 0, len(rows))
	for _, row := range rows {
		steps = append(steps, DesignStep{
			Step: row.Step, Design: row.Design, Kind: row.Kind, Subject: row.Subject, Repository: row.Repository,
			Revision: row.Revision, Delta: row.Delta, Created: time.Unix(row.Created, 0),
		})
	}
	return steps, nil
}
