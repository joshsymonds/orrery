package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// insertTrigger adds the pending trigger id with cause to epic, created at at, in the caller's write. Every write that
// adds a trigger adds it here or through insertTriggerOnce, so one rule holds for all of them: an epic in a final phase
// (FinalPhase) gets no trigger. No planner turn runs for such an epic (checkClaimable), and no write moves an epic out
// of a final phase, so a trigger added there would wait on a turn that never comes, and the planner would list the
// epic on every pass for it. A trigger that epic already holds under id is an error.
func insertTrigger(ctx context.Context, queries *sqlcgen.Queries, epic, id, cause string, at time.Time) error {
	takes, err := takesTriggers(ctx, queries, epic)
	if err != nil || !takes {
		return err
	}
	if err = queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: epic, ID: id, Cause: cause, State: triggerPending, Turn: 0, Created: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert trigger %s of epic %s: %w", id, epic, err)
	}
	return nil
}

// insertTriggerOnce adds the pending trigger id with cause to epic as insertTrigger does, under the same rule, except
// that a trigger epic already holds under id, in any state, is left as it is, so a repeat of the write adds nothing.
func insertTriggerOnce(ctx context.Context, queries *sqlcgen.Queries, epic, id, cause string, at time.Time) error {
	takes, err := takesTriggers(ctx, queries, epic)
	if err != nil || !takes {
		return err
	}
	if err = queries.InsertEpicTriggerOnce(ctx, sqlcgen.InsertEpicTriggerOnceParams{
		Epic: epic, ID: id, Cause: cause, State: triggerPending, Turn: 0, Created: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert trigger %s of epic %s: %w", id, epic, err)
	}
	return nil
}

// takesTriggers reports whether epic, as the caller's write holds it, takes a new trigger: it does unless its phase is a
// final phase. An epic the store holds no row for has no phase, so it takes its trigger, as it did before the rule: the
// trigger writes leave it to their callers to refuse an unknown epic.
func takesTriggers(ctx context.Context, queries *sqlcgen.Queries, epic string) (bool, error) {
	head, err := epicHead(ctx, queries, epic)
	if errors.Is(err, ErrNoEpic) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return !FinalPhase(head.Phase), nil
}
