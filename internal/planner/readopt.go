package planner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store"
)

// ReadoptCheck returns the return check a re-adopted step row is restored with. A row that is the step of a running
// planner turn gets the plan check of that turn's epic, resolved by resolve, and any other row gets a nil check. A
// store error is returned.
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
			return returnCheck(ctx, epic, resolve), nil
		}
		return nil, nil
	}
}
