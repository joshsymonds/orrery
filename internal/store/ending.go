package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EndEpic ends epic at phase in one write: it sets the phase of epic and adds body as its next tell, at time at. For
// phase clean it also stores the stored base of epic as the clean revision of its review, the revision the Done gate
// passed. An epic already in phase changes nothing, so a restart ends it once.
func (s *Store) EndEpic(ctx context.Context, epic, phase, body string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		head, err := epicHead(ctx, queries, epic)
		if err != nil {
			return err
		}
		if head.Phase == phase {
			return nil
		}
		if phase == PhaseClean {
			if err = queries.SetEpicReviewClean(ctx, sqlcgen.SetEpicReviewCleanParams{
				Clean: head.StoredBase, Epic: epic,
			}); err != nil {
				return fmt.Errorf("set clean revision of epic %s: %w", epic, err)
			}
		}
		if err = insertTell(ctx, queries, epic, body, at); err != nil {
			return fmt.Errorf("end epic %s: %w", epic, err)
		}
		return setEpicPhase(ctx, queries, epic, phase)
	})
}
