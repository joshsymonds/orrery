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
	return designStepsOf(rows), nil
}

// RunningDesignSteps returns the steps whose design's working conversation is session and whose step has not ended,
// oldest first and ties by step id. A design's working conversation is what DesignConversation returns for it. It
// returns none for a session no running step's design is worked by.
func (s *Store) RunningDesignSteps(ctx context.Context, session string) ([]DesignStep, error) {
	return readRunningDesignSteps(ctx, sqlcgen.New(s.read), session)
}

// RunningDesignSteps returns the running steps of session as Store.RunningDesignSteps does, read through the Reader.
func (r *Reader) RunningDesignSteps(ctx context.Context, session string) ([]DesignStep, error) {
	var steps []DesignStep
	err := r.readEngine(ctx, func(queries *sqlcgen.Queries) error {
		var readErr error
		steps, readErr = readRunningDesignSteps(ctx, queries, session)
		return readErr
	})
	return steps, err
}

// readRunningDesignSteps returns the running steps of session as Store.RunningDesignSteps does, read with queries.
func readRunningDesignSteps(ctx context.Context, queries *sqlcgen.Queries, session string) ([]DesignStep, error) {
	rows, err := queries.ListRunningDesignSteps(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("list running steps of session %s: %w", session, err)
	}
	return designStepsOf(rows), nil
}

// designStepsOf returns the DesignStep of each row, in order.
func designStepsOf(rows []sqlcgen.DesignStep) []DesignStep {
	steps := make([]DesignStep, 0, len(rows))
	for _, row := range rows {
		steps = append(steps, DesignStep{
			Step: row.Step, Design: row.Design, Kind: row.Kind, Subject: row.Subject, Repository: row.Repository,
			Revision: row.Revision, Delta: row.Delta, Created: time.Unix(row.Created, 0),
		})
	}
	return steps
}
