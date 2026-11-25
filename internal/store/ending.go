package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EndEpic ends epic at phase in one write: it sets the phase of epic and, for every phase but clean, adds body as its
// next tell, at time at, and it ends each running node run of epic that ends names, as EndNodes does. A tell of a phase
// whose event the steerer is sent is delivered already, as insertPhaseTell gives. Clean adds no tell, since the release
// that follows ends the epic released or done and that ending tells. For phase clean it stores
// the stored base of epic as the clean revision of its review, the revision the Done gate passed. An epic already in
// phase gets no new tell or phase, and its runs in ends still end. So the run whose node decided the ending ends in the
// write that ends the epic: no restart finds the epic ended and that run still running, and decides the ending again
// after another write has moved the phase on.
func (s *Store) EndEpic(ctx context.Context, epic, phase, body string, ends []NodeEnd, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := endNodeRuns(ctx, queries, epic, ends, at); err != nil {
			return err
		}
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
		} else if err = insertPhaseTell(ctx, queries, epic, phase, body, at); err != nil {
			return fmt.Errorf("end epic %s: %w", epic, err)
		}
		return setEpicPhase(ctx, queries, epic, phase, at)
	})
}
