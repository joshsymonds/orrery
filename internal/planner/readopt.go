package planner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store"
)

// ReadoptCheck returns the return check a re-adopted step row is restored with. A row that is the step of a running
// planner turn gets the plan check of that turn's epic and its review, resolved by resolve, with the planner turns each
// gap of the epic was given before that turn, read with the triggers it claimed, and whether the epic holds a park at the
// bound of its chain that waits on the planner's call, and any other row gets a nil check. A store error is returned.
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
			return readoptTurnCheck(ctx, st, resolve, turn)
		}
		return nil, nil
	}
}

// readoptTurnCheck returns the return check of turn, the running planner turn whose step is re-adopted, as ReadoptCheck
// describes it. A store error is returned.
func readoptTurnCheck(
	ctx context.Context, st Store, resolve Resolver, turn store.EpicTurn,
) (func(value json.RawMessage) []string, error) {
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
	gapTurns, gapTurnsErr := st.GapTurns(ctx, turn.Epic)
	if gapTurnsErr != nil {
		return nil, fmt.Errorf("read the gap turns of epic %s: %w", turn.Epic, gapTurnsErr)
	}
	chain, chainErr := st.BoundChain(ctx, turn.Epic)
	if chainErr != nil {
		return nil, fmt.Errorf("read the park bound of epic %s: %w", turn.Epic, chainErr)
	}
	return returnCheck(ctx, epic, review, resolve, priorGapTurns(gapTurns, triggers), chain.Seq != 0), nil
}
