package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// HoldReviewStep stores the hold on the stopped review step of epic and, only when it stored the hold, pauses epic as
// its owner's and adds reason as a tell, in one write. A step already held changes nothing, so the pause happens once
// per stopped step.
func (s *Store) HoldReviewStep(ctx context.Context, epic, step, reason string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		stored, err := queries.InsertEpicReviewHold(ctx, sqlcgen.InsertEpicReviewHoldParams{Epic: epic, Step: step})
		if err != nil {
			return fmt.Errorf("hold step %s of epic %s: %w", step, epic, err)
		}
		if stored == 0 {
			return nil
		}
		if err = pauseAsOwner(ctx, queries, epic); err != nil {
			return fmt.Errorf("pause epic %s for held step %s: %w", epic, step, err)
		}
		return insertTell(ctx, queries, epic, reason, at)
	})
}
