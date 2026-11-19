package planner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store"
)

// ReadoptCheck returns the return check a re-adopted step row is restored with. A row that is the step of a running
// planner turn gets the plan check of that turn's epic and its review, resolved by resolve, with the lines of failed
// tasks the turn may not try again, read from the triggers it claimed, and any other row gets a nil check. A store
// error is returned.
func ReadoptCheck(
	st Store,
	resolve Resolver,
) func(ctx context.Context, row store.StepRow) (func(value json.RawMessage) []string, error) {
	return func(ctx context.Context, row store.StepRow) (func(value json.RawMessage) []string, error) {
		turns, err := st.RunningTurns(ctx)
		if err != nil {
			return nil, fmt.Errorf("list running turns: %w", err)
		}
		for _, turn := range turns {
			if turn.Step != row.ID {
				continue
			}
			epic, epicErr := st.Epic(ctx, turn.Epic)
			if epicErr != nil {
				return nil, fmt.Errorf("read epic %s: %w", turn.Epic, epicErr)
			}
			review, reviewErr := st.EpicReview(ctx, turn.Epic)
			if reviewErr != nil {
				return nil, fmt.Errorf("read review of epic %s: %w", turn.Epic, reviewErr)
			}
			triggers, triggersErr := st.TurnTriggers(ctx, turn.Epic, turn.Seq)
			if triggersErr != nil {
				return nil, fmt.Errorf("read triggers of turn %d of epic %s: %w", turn.Seq, turn.Epic, triggersErr)
			}
			spent, spentErr := spentLines(ctx, st, epic, triggers)
			if spentErr != nil {
				return nil, fmt.Errorf("read the spent lines of turn %d of epic %s: %w", turn.Seq, turn.Epic, spentErr)
			}
			return returnCheck(ctx, epic, review, resolve, spent), nil
		}
		return nil, nil
	}
}
