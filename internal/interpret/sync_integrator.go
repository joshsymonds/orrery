package interpret

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// errSyncStepNotCarriedOut is the error syncIntegrator returns for a round whose step is not carried out yet.
var errSyncStepNotCarriedOut = errors.New("the sync integrator's step is not carried out yet")

// syncIntegrator carries out one ActIntegrator action of a sync run, as the first of its items says. A start whose role
// does not resolve records its round failed (failSyncRound), so the sync's next pass ends it failed and told. Any other
// start, and every record, needs the sync integrator's step, which is not carried out yet: the error ends the sync
// skipped (syncUnfinished). T8 replaces this, and carries out the step, its worktree and the record of its end.
func (r *Runner) syncIntegrator(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if len(act.IDs) == 0 || len(act.Items) < 2 {
		return fmt.Errorf("sync integrator action of node %s names no step or round", act.Path)
	}
	round, err := strconv.ParseInt(act.Items[1], 10, 64)
	if err != nil {
		return fmt.Errorf("sync integrator round %q of node %s is not a round number: %w", act.Items[1], act.Path, err)
	}
	switch act.Items[0] {
	case kinds.SyncRoundStart:
		if _, err = roles.AgentOr(r.getenv, roleIntegrator, roleRelease); err != nil {
			return r.failSyncRound(ctx, epic, act, round, err, at)
		}
		return errSyncStepNotCarriedOut
	case kinds.SyncRoundRecord:
		return errSyncStepNotCarriedOut
	default:
		return fmt.Errorf("sync integrator action of node %s has no mode %q", act.Path, act.Items[0])
	}
}

// failSyncRound records round of the sync run of act as failed, with the conflict line of its result and cause, the
// reason the round's step did not start. The next pass ends the sync failed and told, with that failure. The round
// never merged, so its verdict is ended, as a task integrator round whose step ended is: the store has no verdict for a
// round that did not start, and rejects an empty one.
func (r *Runner) failSyncRound(
	ctx context.Context, epic store.EpicRow, act Action, round int64, cause error, at time.Time,
) error {
	result, err := kinds.ParseSyncResult(act.Run.Result)
	if err != nil {
		return fmt.Errorf("read result of node %s: %w", act.Path, err)
	}
	record := store.IntegrateRound{
		Path: act.Path, Attempt: act.Run.Attempt, Task: 0, Round: round, Step: act.IDs[0], Verdict: kinds.RoundEnded,
		Outcome: outcomeFailed, Failure: result.ConflictLine() + ": " + cause.Error(),
	}
	if err = r.store.RecordIntegrateRound(ctx, epic.ID, record, at); err != nil {
		return fmt.Errorf("record round %d of sync %s: %w", round, act.Path, err)
	}
	return nil
}
