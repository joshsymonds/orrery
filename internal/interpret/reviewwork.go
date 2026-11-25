package interpret

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/reviewers"
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

// prepareReview freezes the review of epic at its candidate, or at its stored base when it has none, which gives the review
// run at act's path that revision and sets the epic reviewing.
func (r *Runner) prepareReview(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	base, err := reviewRevisionOf(epic)
	if err != nil {
		return err
	}
	if err = r.store.FreezeReview(ctx, epic.ID, act.Path, act.Run.Attempt, base, at); err != nil {
		return fmt.Errorf("freeze review of epic %s: %w", epic.ID, err)
	}
	return nil
}

// prepareClosure records the stored base of epic as the revision of the closure run, or of the rerun run, at act's path.
// Neither takes refs, so none are recorded. A rerun run's revision is the stored base too, as a closure's is, and a
// rerun is not frozen, so it does not freeze the review.
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

// reviewRevisionOf returns the revision the review of epic freezes at: its candidate when it has one, since the candidate
// holds the epic's composed work over its stored base, and otherwise its stored base (storedBaseOf).
func reviewRevisionOf(epic store.EpicRow) (string, error) {
	if epic.Candidate != "" {
		return epic.Candidate, nil
	}
	return storedBaseOf(epic)
}

// storedBaseOf returns the stored base of epic, the revision its closure runs at and the one its review freezes at when it
// has no candidate. An epic with no stored base has no revision to run at, so it is an error.
func storedBaseOf(epic store.EpicRow) (string, error) {
	if epic.StoredBase == "" {
		return "", fmt.Errorf("epic %s has no stored base to run at", epic.ID)
	}
	return epic.StoredBase, nil
}

// startReviewUnits starts each unit act asks for under its id, labeled with the epic and the path of act's run, in the
// order of the units. A start error is returned, and the units after it wait for a later pass.
func (r *Runner) startReviewUnits(ctx context.Context, epic store.EpicRow, act Action) error {
	for _, unit := range act.Units {
		start, err := r.reviewUnitStart(ctx, epic, act, unit)
		if err != nil {
			return fmt.Errorf("start unit %s: %w", unit.ID, err)
		}
		start.ID, start.Epic, start.Task = unit.ID, epic.ID, act.Path
		if _, err = r.steps.Start(ctx, start); err != nil {
			return fmt.Errorf("start step %s: %w", unit.ID, err)
		}
	}
	return nil
}

// reviewUnitStart returns the step of unit, a unit of the review or closure run act is, before its id and labels are set. A
// reviewer's step is the reviewers' step at the frozen revision, which is act's run revision, over the change from the
// epic's base. A verifier's step runs over the base to that revision. A closure verifier's
// step runs over the frozen revision to the closure run's revision.
func (r *Runner) reviewUnitStart(
	ctx context.Context, epic store.EpicRow, act Action, unit kinds.Unit,
) (enginehost.StepStart, error) {
	switch {
	case strings.HasPrefix(unit.Part, reviewerPart):
		var doneGates, baseCost []store.NodeCheck
		if unit.Reviewer == reviewers.TestParsimony {
			var err error
			if doneGates, baseCost, err = r.doneGateChecks(ctx, epic.ID, act.Run.Revision); err != nil {
				return enginehost.StepStart{}, err
			}
		}
		start, err := r.reviewers.Start(
			ctx, epic, unit.Reviewer, epic.Base, act.Run.Revision, doneGates, baseCost)
		if err != nil {
			return enginehost.StepStart{}, fmt.Errorf("reviewer %s: %w", unit.Reviewer, err)
		}
		return start, nil
	case strings.HasPrefix(unit.Part, verifyPart):
		agent, err := r.verifierAgent()
		if err != nil {
			return enginehost.StepStart{}, err
		}
		return epicVerifierStart(epic, epic.Base, act.Run.Revision, unit.Finding, unit.ID, agent), nil
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

// doneGateChecks returns the checks of the Done gates that ran at revision of epic: the checks that the newest started
// run at that revision with any ran there and that ended with a result, in the order they were created. That run is the
// integrate run that made revision the epic's stored base. A check lost at a restart, and one that exited
// store.EnvironmentExit, which its rerun replaces, are left out. A Done gate that run's verification choice skipped has
// no check, so it is not among them: the Test cost reads what ran, never a skipped gate as one that passed. It returns
// none when no run ran a check at revision.
// It also returns the base measurement of test cost: the cost checks of the same read (store.CostChecks), so both
// describe the same state of the epic's checks.
func (r *Runner) doneGateChecks(
	ctx context.Context, epic, revision string,
) ([]store.NodeCheck, []store.NodeCheck, error) {
	read, err := r.store.EpicFlow(ctx, epic)
	if err != nil {
		return nil, nil, fmt.Errorf("read epic %s: %w", epic, err)
	}
	var (
		newest  time.Time
		decided []store.NodeCheck
	)
	for _, run := range read.Runs {
		if run.Revision != revision || run.Started.Before(newest) {
			continue
		}
		var ran []store.NodeCheck
		for _, check := range read.Checks {
			if check.Path == run.Path && check.Revision == revision && check.State == store.NodeEnded &&
				check.NotRun == "" && !store.EnvironmentExited(check.Exit, check.TimedOut) {
				ran = append(ran, check)
			}
		}
		if len(ran) > 0 {
			newest, decided = run.Started, ran
		}
	}
	return decided, store.CostChecks(read.Checks), nil
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
