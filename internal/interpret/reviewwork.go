package interpret

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/roles"
	"github.com/joshsymonds/orrery/internal/store"
)

// reviewVerifierRole is the role the verifier and closure verifier steps run under, and the parts of the units a review
// or closure run starts, as the kinds name them: "reviewer <n>" for a reviewer's step, "verify <n> <k>" for a verifier's
// and "close <id>" for a closure verifier's.
const (
	reviewVerifierRole = "verifier"
	reviewerPart       = "reviewer "
	verifyPart         = "verify "
	closePart          = "close "
)

// prepareReview freezes the review of epic at its stored base, which gives the review run at act's path that revision and
// sets the epic reviewing.
func (r *Runner) prepareReview(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	base, err := storedBaseOf(epic)
	if err != nil {
		return err
	}
	if err = r.store.FreezeReview(ctx, epic.ID, act.Path, act.Run.Attempt, base, at); err != nil {
		return fmt.Errorf("freeze review of epic %s: %w", epic.ID, err)
	}
	return nil
}

// prepareClosure records the stored base of epic as the revision of the closure run at act's path. A closure run takes no
// refs, so none are recorded.
func (r *Runner) prepareClosure(ctx context.Context, epic store.EpicRow, act Action) error {
	base, err := storedBaseOf(epic)
	if err != nil {
		return err
	}
	if err = r.store.PrepareNode(ctx, epic.ID, act.Path, act.Run.Attempt, base, ""); err != nil {
		return fmt.Errorf("prepare node %s: %w", act.Path, err)
	}
	return nil
}

// storedBaseOf returns the stored base of epic, the revision its review freezes at and its closure runs at. An epic with no
// stored base has no revision to run at, so it is an error.
func storedBaseOf(epic store.EpicRow) (string, error) {
	if epic.StoredBase == "" {
		return "", fmt.Errorf("epic %s has no stored base to run at", epic.ID)
	}
	return epic.StoredBase, nil
}

// startReviewUnits starts each unit act asks for under its id, labeled with the epic and the path of act's run, in the
// order of the units. A start the step cap refuses ends the pass's starts with no error, and the next pass starts the
// rest. Any other start error is returned, and the units after it wait for a later pass.
func (r *Runner) startReviewUnits(ctx context.Context, epic store.EpicRow, act Action) error {
	for _, unit := range act.Units {
		start, err := r.reviewUnitStart(ctx, epic, act, unit)
		if err != nil {
			return fmt.Errorf("start unit %s: %w", unit.ID, err)
		}
		start.ID, start.Epic, start.Task = unit.ID, epic.ID, act.Path
		if _, err = r.steps.Start(ctx, start); err != nil {
			if errors.Is(err, enginehost.ErrCapReached) {
				return nil
			}
			return fmt.Errorf("start step %s: %w", unit.ID, err)
		}
	}
	return nil
}

// reviewUnitStart returns the step of unit, a unit of the review or closure run act is, before its id and labels are set. A
// reviewer's step is the reviewers' step at the frozen revision, which is act's run revision, over the change from the
// epic's change base (changeBase). A verifier's step runs over the change base to that revision. A closure verifier's
// step runs over the frozen revision to the closure run's revision.
func (r *Runner) reviewUnitStart(
	ctx context.Context, epic store.EpicRow, act Action, unit kinds.Unit,
) (enginehost.StepStart, error) {
	switch {
	case strings.HasPrefix(unit.Part, reviewerPart):
		start, err := r.reviewers.Start(ctx, epic, unit.Reviewer, changeBase(epic), act.Run.Revision)
		if err != nil {
			return enginehost.StepStart{}, fmt.Errorf("reviewer %s: %w", unit.Reviewer, err)
		}
		return start, nil
	case strings.HasPrefix(unit.Part, verifyPart):
		agent, err := r.verifierAgent()
		if err != nil {
			return enginehost.StepStart{}, err
		}
		return epicVerifierStart(epic, changeBase(epic), act.Run.Revision, unit.Finding, unit.ID, agent), nil
	case strings.HasPrefix(unit.Part, closePart):
		frozen, err := r.frozenRevision(ctx, epic.ID)
		if err != nil {
			return enginehost.StepStart{}, err
		}
		agent, err := r.verifierAgent()
		if err != nil {
			return enginehost.StepStart{}, err
		}
		return closureStart(epic, frozen, act.Run.Revision, unit.Finding, unit.ID, agent), nil
	default:
		return enginehost.StepStart{}, fmt.Errorf("unit %q has no step to start", unit.Part)
	}
}

// verifierAgent returns the agent the verifier role runs under, from the machine's role file.
func (r *Runner) verifierAgent() (string, error) {
	agent, err := roles.Agent(r.getenv, reviewVerifierRole)
	if err != nil {
		return "", fmt.Errorf("verifier agent: %w", err)
	}
	return agent, nil
}

// frozenRevision returns the revision the review of epic froze at. A closure verifier checks the change from that
// revision, so an epic whose review has not frozen is an error.
func (r *Runner) frozenRevision(ctx context.Context, epic string) (string, error) {
	read, err := r.store.EpicFlow(ctx, epic)
	if err != nil {
		return "", fmt.Errorf("read epic %s: %w", epic, err)
	}
	if read.Review.Frozen == "" {
		return "", fmt.Errorf("epic %s has no frozen review", epic)
	}
	return read.Review.Frozen, nil
}
