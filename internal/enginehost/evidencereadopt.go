package enginehost

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// Resume takes up every evidence run the store holds open, as a restarted orreryd does. A judging run gets its verdict, and a
// supplied run still capturing is read again. A captured brief is left to its gatherer, which the gatherers' Resume wakes.
// Each run is driven in the background under a context that Close cancels and waits for, as Run's drives are, and a run this
// Evidence already drives is left alone. It returns the errors of the runs it could not take up, joined.
func (e *Evidence) Resume(ctx context.Context) error {
	defer e.calls.enter()()
	open, err := e.store.OpenEvidenceRuns(ctx)
	if err != nil {
		return fmt.Errorf("resume evidence runs: %w", err)
	}
	var errs []error
	for _, run := range open {
		if err = e.readoptStart(ctx, run); err != nil {
			errs = append(errs, fmt.Errorf("resume evidence run %s: %w", run.ID, err))
		}
	}
	return errors.Join(errs...)
}

// readoptStart drives open run in the background at its epic's repository, as start drives a new run, under the same
// cancel: a Close under a gate scope detaches the readopted gate, as it does a new one. It returns an error when the epic
// cannot be read or the runner is closed.
func (e *Evidence) readoptStart(ctx context.Context, run store.EvidenceRun) error {
	row, err := e.store.Epic(ctx, run.Epic)
	if err != nil {
		return fmt.Errorf("epic %s: %w", run.Epic, err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errEvidenceClosed
	}
	if _, driven := e.cancels[run.ID]; driven {
		return nil
	}
	driveCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	e.cancels[run.ID] = cancel
	e.drives.Go(func() {
		e.readoptDrive(driveCtx, row.Repository, run)
		e.finish(run.ID)
	})
	return nil
}

// readoptDrive takes up open run at repo. A judging run gets its verdict, and a supplied run still capturing is read again. A
// captured run that is not judging is a brief, which waits for its gatherer, so it is left as it is.
func (e *Evidence) readoptDrive(ctx context.Context, repo string, run store.EvidenceRun) {
	switch {
	case run.State == "judging":
		e.readoptJudge(ctx, repo, run)
	case run.Source == evidence.SourceSupplied:
		e.drive(ctx, repo, run)
	}
}

// readoptJudge gives judging run its verdict as judgeRun does. It waits for the judge step the run holds, or for the judge a
// restart finds under the next derived id, and a judge that ends with no verdict is followed by another, up to evidenceJudgeTries
// attempts in all. A run that holds no judge step and has a verdict without a judge (see unjudgedVerdict) is recorded with it,
// and no judge starts. The verdict is recorded once.
func (e *Evidence) readoptJudge(ctx context.Context, repo string, run store.EvidenceRun) {
	step, judges, err := e.readoptJudgeStep(ctx, run)
	if err != nil {
		e.logger.WarnContext(ctx, "evidence judge stopped", "run", run.ID, "err", err)
		return
	}
	verdict, reason := "", ""
	if step == "" {
		if unjudged, why, ok := e.unjudgedVerdict(ctx, repo, run); ok {
			e.recordVerdict(ctx, run, unjudged, why)
			return
		}
	}
	if step != "" {
		row, waitErr := e.awaitJudge(ctx, step)
		if waitErr != nil {
			e.logger.WarnContext(ctx, "evidence judge stopped", "run", run.ID, "err", waitErr)
			return
		}
		verdict, reason = judgeVerdict(row)
	}
	for tries := judges; verdict == "" && tries < evidenceJudgeTries; tries++ {
		var started bool
		started, verdict, reason, err = e.judgeOnce(ctx, repo, run, judges+1)
		if err != nil {
			e.logger.WarnContext(ctx, "evidence judge stopped", "run", run.ID, "err", err)
			return
		}
		if started {
			judges++
		}
	}
	e.recordVerdict(ctx, run, verdict, reason)
}

// readoptJudgeStep returns the judge step run holds and the judges the store counts for it. A run's next judge starts under the
// derived id of its next number, so a step the store has under that id is a judge a restart found started and not recorded. It
// is adopted and counted, so a judge that still runs is waited for and never doubled. Otherwise the stored step, or none, stands.
func (e *Evidence) readoptJudgeStep(ctx context.Context, run store.EvidenceRun) (string, int, error) {
	next := evidenceJudgeID(run.ID, run.Judges+1)
	_, err := e.store.Step(ctx, next)
	if errors.Is(err, store.ErrNoStep) {
		return run.JudgeStep, run.Judges, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("judge step of evidence run %s: %w", run.ID, err)
	}
	if recordErr := e.recordJudge(ctx, run.ID, next); recordErr != nil {
		return "", 0, recordErr
	}
	return next, run.Judges + 1, nil
}

// EvidenceReadoptGate returns the ReadoptGate of a Steps. A judge step of an evidence run has its agent gate rebuilt from the
// run's stored item, as judgeGate gives it. Any other step row has no gate, and a judge row whose run is not stored is an error.
func EvidenceReadoptGate(
	st interface {
		EvidenceRun(ctx context.Context, id string) (store.EvidenceRun, error)
	},
) func(ctx context.Context, row store.StepRow) (*gates.Gate, error) {
	return func(ctx context.Context, row store.StepRow) (*gates.Gate, error) {
		id, isRun := strings.CutPrefix(row.Task, "evidence-")
		if row.GateName != evidenceJudgeGate || !isRun {
			return nil, nil
		}
		run, err := st.EvidenceRun(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("rebuild judge gate of step %s: %w", row.ID, err)
		}
		gate := judgeGate(run.Spec)
		return &gate, nil
	}
}
