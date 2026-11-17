package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// NextRound moves the running loop at path of epic from its round from to round from+1, in one write. A loop at another
// round, a loop that is parked or ended, and a run that is not a loop change nothing, so a repeat with the same from is
// a no-op.
func (s *Store) NextRound(ctx context.Context, epic, path string, from int64) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.MoveEpicLoopRound(ctx, sqlcgen.MoveEpicLoopRoundParams{
			Epic: epic, Path: path, LoopRound: from,
		}); err != nil {
			return fmt.Errorf("move loop %s of epic %s past round %d: %w", path, epic, from, err)
		}
		return nil
	})
}

// ParkLoop parks the running loop at path of epic at round round, and parks epic with reason as the loop planner class,
// in one write. Reason is cut to TurnReasonMax bytes. A loop at another round, and a loop that is not running, change
// nothing, so a repeat is a no-op.
func (s *Store) ParkLoop(ctx context.Context, epic, path string, round int64, reason string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.ParkEpicLoopRun(ctx, sqlcgen.ParkEpicLoopRunParams{
			Epic: epic, Path: path, LoopRound: round,
		})
		if err != nil {
			return fmt.Errorf("park loop %s of epic %s: %w", path, epic, err)
		}
		if rows == 0 {
			return nil
		}
		return parkEpicAs(ctx, queries, epic, ParkClassLoop, reason, at)
	})
}

// grantParkedLoops turns each parked loop of epic running with one more granted round, in the caller's write.
func grantParkedLoops(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	if err := queries.GrantEpicParkedLoops(ctx, epic); err != nil {
		return fmt.Errorf("grant parked loops of epic %s: %w", epic, err)
	}
	return nil
}
