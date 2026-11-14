package enginehost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// Resume takes up every evidence run the store holds open, as a restarted orreryd does. A capturing run with a stored grant
// readopts its gate, one with no grant runs its capture from the start, and a judging run gets its verdict. Each run is driven
// in the background under a context that Close cancels and waits for, as Run's drives are, and a run this Evidence already
// drives is left alone. It returns the errors of the runs it could not take up, joined.
func (e *Evidence) Resume(ctx context.Context) error {
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

// readoptDrive takes up open run at repo. A judging run gets its verdict, a capturing run with a grant is readopted, and any
// other capturing run starts its capture.
func (e *Evidence) readoptDrive(ctx context.Context, repo string, run store.EvidenceRun) {
	switch {
	case run.State == "judging":
		e.readoptJudge(ctx, repo, run)
	case run.Grant != "":
		e.readoptCapture(ctx, repo, run)
	default:
		e.readoptUnlaunched(ctx, repo, run)
	}
}

// readoptUnlaunched runs the capture of a run with no grant from the start. Its gate never launched, so a worktree a restart
// found at its path is what a start stopped before the grant left, and it is removed before the capture is added again.
func (e *Evidence) readoptUnlaunched(ctx context.Context, repo string, run store.EvidenceRun) {
	if run.Source == evidence.SourceCaptured {
		worktree := filepath.Join(e.captures.worktrees, run.ID)
		if err := removeStepWorktree(context.WithoutCancel(ctx), repo, worktree); err != nil {
			e.logger.WarnContext(ctx, "clear worktree of unlaunched evidence run", "run", run.ID, "err", err)
			return
		}
	}
	e.drive(ctx, repo, run)
}

// readoptCapture readopts the capture of run, whose grant is stored, and records it as drive does. A gate that detaches again
// records nothing, so the next restart readopts it.
func (e *Evidence) readoptCapture(ctx context.Context, repo string, run store.EvidenceRun) {
	capture, err := e.captures.readopt(ctx, repo, run.Revision, run.ID, run.Spec, run.Grant)
	if err != nil {
		e.logger.InfoContext(ctx, "evidence capture detached from its gate", "run", run.ID, "err", err)
		return
	}
	if ctx.Err() != nil {
		return
	}
	if err = e.store.RecordEvidenceCapture(ctx, run.ID, capture); err != nil {
		e.logger.ErrorContext(ctx, "record evidence capture", "run", run.ID, "err", err)
		return
	}
	if capture.NotCaptured != "" {
		return
	}
	e.readoptJudge(ctx, repo, evidenceRunCaptured(run, capture))
}

// readoptJudge gives judging run its verdict as judgeRun does. It waits for the judge step the run holds, or starts one when it
// holds none, and a judge that ends with no verdict is followed by another, up to evidenceJudgeTries judges in all. The verdict
// is recorded once.
func (e *Evidence) readoptJudge(ctx context.Context, repo string, run store.EvidenceRun) {
	step, judges, err := e.readoptJudgeStep(ctx, run)
	if err != nil {
		e.logger.WarnContext(ctx, "evidence judge stopped", "run", run.ID, "err", err)
		return
	}
	verdict, reason := "", ""
	if step != "" {
		row, waitErr := e.awaitJudge(ctx, step)
		if waitErr != nil {
			e.logger.WarnContext(ctx, "evidence judge stopped", "run", run.ID, "err", waitErr)
			return
		}
		verdict, reason = judgeVerdict(row)
	}
	for verdict == "" && judges < evidenceJudgeTries {
		verdict, reason, err = e.judgeOnce(ctx, repo, run)
		if err != nil {
			e.logger.WarnContext(ctx, "evidence judge stopped", "run", run.ID, "err", err)
			return
		}
		judges++
	}
	e.recordVerdict(ctx, run, verdict, reason)
}

// readoptJudgeStep returns the judge step run holds and the judges the store counts for it. A run's judges start one after
// another, so the newest step LabelledSteps lists for it is its judge. When that step is not the stored one, a restart
// found it started and not stored: it is adopted and counted as a judge, so a judge that still runs is waited for and
// never doubled. Otherwise the stored step, or none, stands.
func (e *Evidence) readoptJudgeStep(ctx context.Context, run store.EvidenceRun) (string, int, error) {
	steps, err := e.store.LabelledSteps(ctx, run.Epic, "evidence-"+run.ID)
	if err != nil {
		return "", 0, fmt.Errorf("labeled steps of evidence run %s: %w", run.ID, err)
	}
	newest := readoptNewestJudge(steps, run.JudgeStep)
	if newest == "" || newest == run.JudgeStep {
		return run.JudgeStep, run.Judges, nil
	}
	if err = e.store.SetEvidenceJudge(ctx, run.ID, newest); err != nil {
		return "", 0, fmt.Errorf("record judge of evidence run %s: %w", run.ID, err)
	}
	return newest, run.Judges + 1, nil
}

// readoptNewestJudge returns the id of the newest of a run's judge steps: the latest to start, then a running step over an
// ended one, then the latest to end. A tie keeps stored, the step the run already holds. It returns "" when steps is empty.
func readoptNewestJudge(steps []store.StepRow, stored string) string {
	var newest *store.StepRow
	for i := range steps {
		if steps[i].ID == stored {
			newest = &steps[i]
			break
		}
	}
	for i := range steps {
		if newest == nil || readoptJudgeAfter(steps[i], *newest) {
			newest = &steps[i]
		}
	}
	if newest == nil {
		return ""
	}
	return newest.ID
}

// readoptJudgeAfter reports whether judge step candidate is newer than current: it started later, or started in the same
// second and still runs while current has ended, or both ended and candidate ended later.
func readoptJudgeAfter(candidate, current store.StepRow) bool {
	switch {
	case !candidate.Started.Equal(current.Started):
		return candidate.Started.After(current.Started)
	case (candidate.End == "") != (current.End == ""):
		return candidate.End == ""
	default:
		return candidate.Ended.After(current.Ended)
	}
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
