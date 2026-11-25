package enginehost

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// ErrAttachRefused reports a bad request to attach a capture to briefs. Attach wraps it with the reason.
var ErrAttachRefused = errors.New("evidence attach is refused")

// Attach attaches the capture with id capture to the briefs, the evidence runs by id, that it answers, for the caller
// session. It returns the runs it attached the capture to, as stored. A refused request matches ErrAttachRefused and
// attaches nothing. A run the capture ends not captured is not judged, and each other run is judged in the background.
func (e *Evidence) Attach(ctx context.Context, caller, capture string, briefs []string) ([]store.EvidenceRun, error) {
	defer e.calls.enter()()
	if e.briefs == nil {
		return nil, fmt.Errorf("attach evidence: no briefs store: %w", errors.ErrUnsupported)
	}
	if len(briefs) == 0 {
		return nil, attachRefused("briefs: none named")
	}
	runs, err := e.attachRuns(ctx, briefs)
	if err != nil {
		return nil, err
	}
	repo, err := e.attachCheck(ctx, caller, capture, runs)
	if err != nil {
		return nil, err
	}
	attached, err := e.briefs.AttachCapture(ctx, capture, briefs, e.now())
	switch {
	case errors.Is(err, store.ErrEvidenceState),
		errors.Is(err, store.ErrNoEvidenceRun),
		errors.Is(err, store.ErrNoCapture):
		return nil, attachRefused(err.Error())
	case err != nil:
		return nil, fmt.Errorf("attach capture %s: %w", capture, err)
	}
	if judgeErr := e.judgeAttached(ctx, repo, attached); judgeErr != nil {
		return nil, judgeErr
	}
	return attached, nil
}

// Settle settles the briefs that the ended capture was started for (see store.SettleCaptureBriefs), so a capture reaches
// its briefs whether or not the step that asked for it still runs. Each run the capture is attached to is judged in the
// background, and the gatherers are woken for the capture's epic when a brief went back to waiting. Settling a capture
// again moves nothing.
func (e *Evidence) Settle(ctx context.Context, capture store.Capture) error {
	defer e.calls.enter()()
	if e.briefs == nil {
		return fmt.Errorf("settle capture %s: no briefs store: %w", capture.ID, errors.ErrUnsupported)
	}
	moved, err := e.briefs.SettleCaptureBriefs(ctx, capture.ID, e.now())
	if err != nil {
		return fmt.Errorf("settle briefs of capture %s: %w", capture.ID, err)
	}
	if judgeErr := e.judgeAttached(ctx, capture.Repository, moved); judgeErr != nil {
		return judgeErr
	}
	if e.gatherers != nil && slices.ContainsFunc(moved, func(run store.EvidenceRun) bool {
		return run.State == store.RunWaiting
	}) {
		e.gatherers.Wake(ctx, capture.Epic)
	}
	return nil
}

// attachRuns returns the stored runs briefs names, in the order given. It refuses a brief that names no stored run, a brief
// that is not a captured run waiting or gathering, and briefs of more than one epic.
func (e *Evidence) attachRuns(ctx context.Context, briefs []string) ([]store.EvidenceRun, error) {
	runs := make([]store.EvidenceRun, 0, len(briefs))
	for _, id := range briefs {
		run, err := e.store.EvidenceRun(ctx, id)
		if errors.Is(err, store.ErrNoEvidenceRun) {
			return nil, attachRefused(fmt.Sprintf("brief %s is no stored run", id))
		}
		if err != nil {
			return nil, fmt.Errorf("attach evidence: brief %s: %w", id, err)
		}
		if run.Source != evidence.SourceCaptured || (run.State != store.RunWaiting && run.State != store.RunGathering) {
			return nil, attachRefused(fmt.Sprintf(
				"brief %s is %s and %s, not a captured run waiting or gathering", id, run.Source, run.State,
			))
		}
		if len(runs) > 0 && run.Epic != runs[0].Epic {
			return nil, attachRefused(fmt.Sprintf("brief %s belongs to epic %s, not %s", id, run.Epic, runs[0].Epic))
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// attachCheck refuses an attach of capture to runs, which belong to one epic, unless the caller is that epic's running
// gatherer and the capture is one that answers them: ended, of the epic's repository, and at the commit each run is at. It
// returns the repository of the epic.
func (e *Evidence) attachCheck(
	ctx context.Context, caller, capture string, runs []store.EvidenceRun,
) (string, error) {
	epic := runs[0].Epic
	row, err := e.store.Epic(ctx, epic)
	if err != nil {
		return "", fmt.Errorf("attach evidence: epic %s: %w", epic, err)
	}
	gatherer, running, err := e.briefs.RunningEvidenceGatherer(ctx, epic)
	if err != nil {
		return "", fmt.Errorf("attach evidence: gatherer of epic %s: %w", epic, err)
	}
	if !running || gatherer.Step != caller {
		return "", attachRefused(fmt.Sprintf("caller %s is not the running gatherer of epic %s", caller, epic))
	}
	stored, err := e.briefs.Capture(ctx, capture)
	switch {
	case errors.Is(err, store.ErrNoCapture):
		return "", attachRefused(fmt.Sprintf("capture %s does not exist", capture))
	case err != nil:
		return "", fmt.Errorf("attach evidence: capture %s: %w", capture, err)
	case stored.Ended.IsZero():
		return "", attachRefused(fmt.Sprintf("capture %s is still running", capture))
	case stored.Repository != row.Repository:
		return "", attachRefused(fmt.Sprintf("capture %s belongs to another repository than epic %s", capture, epic))
	}
	for _, run := range runs {
		if run.Revision != stored.Commit {
			return "", attachRefused(fmt.Sprintf(
				"brief %s is at commit %s, and capture %s is at %s", run.ID, run.Revision, capture, stored.Commit,
			))
		}
	}
	return row.Repository, nil
}

// judgeAttached gives each judging run of attached its verdict in the background. A run that the capture ended not captured
// starts no judge. It returns an error when the runner is closed, and the runs not yet judged are then left judging for a
// restart's Resume to give their verdicts.
func (e *Evidence) judgeAttached(ctx context.Context, repo string, attached []store.EvidenceRun) error {
	for _, run := range attached {
		if run.State != "judging" {
			continue
		}
		if err := e.driveAttached(ctx, repo, run); err != nil {
			return fmt.Errorf("judge attached evidence run %s: %w", run.ID, err)
		}
	}
	return nil
}

// driveAttached gives run its verdict in the background, as start drives a new run: under the cancel Close calls, and
// finished once its drive returns. run is a judging run a capture was attached to. It returns an error when the runner is
// closed.
func (e *Evidence) driveAttached(ctx context.Context, repo string, run store.EvidenceRun) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errEvidenceClosed
	}
	driveCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	e.cancels[run.ID] = cancel
	e.drives.Go(func() {
		e.judgeRun(driveCtx, repo, run)
		e.finish(run.ID)
	})
	return nil
}

// attachRefused returns the refusal of an attach request for reason, which matches ErrAttachRefused.
func attachRefused(reason string) error {
	return fmt.Errorf("%w: %s", ErrAttachRefused, reason)
}

var _ AttachCommands = (*Evidence)(nil)
