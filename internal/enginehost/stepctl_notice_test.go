package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// stepCtlDropApplier stores engine ops through db, except the StepEnd of dropID, which it refuses as a step row that is
// gone. The Writer drops that op and stores the rest.
type stepCtlDropApplier struct {
	db     *store.Store
	dropID string
}

func (a *stepCtlDropApplier) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	for i, op := range ops {
		if end, ok := op.(store.StepEnd); ok && end.ID == a.dropID {
			return &store.EngineOpError{Index: i, Err: fmt.Errorf("step %s: %w", a.dropID, store.ErrNoStep)}
		}
	}
	return a.db.ApplyEngine(ctx, ops, now)
}

// startNoticeStep starts a read-only step at the head of repo under epic, which empty leaves unnamed, and returns its id.
func (f *stepCtlFixture) startNoticeStep(t *testing.T, repo, epic string) string {
	t.Helper()
	start := stepStartReadOnly(repo, stepGitTestRun(t, repo, "rev-parse", "HEAD"))
	start.Epic = epic
	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start read-only step: %v", err)
	}
	return id
}

// restartSteps returns a Steps over the fixture's store and sessions, with writer. It is closed at cleanup.
func (f *stepCtlFixture) restartSteps(t *testing.T, writer *Writer) *Steps {
	t.Helper()
	steps := NewSteps(StepsConfig{
		Sessions:     f.sessions,
		Store:        f.store,
		Writer:       writer,
		Pools:        f.pools,
		Worktrees:    f.worktrees,
		Environ:      f.environ,
		Logger:       slog.New(slog.DiscardHandler),
		GateStateDir: f.gateStateDir,
	})
	t.Cleanup(steps.Close)
	return steps
}

func TestStepsReconcileEndsTheKeptStepWhenAnotherEndIsDropped(t *testing.T) {
	t.Parallel()
	f := newStepCtlFixture(t)
	repo := stepGitTestRepo(t)
	kept := f.startNoticeStep(t, repo, "")
	dropped := f.startNoticeStep(t, repo, "")
	f.steps.Close()

	writer := NewWriter(WriterConfig{
		Applier: &stepCtlDropApplier{db: f.store, dropID: dropped},
	})
	startRun(t, writer)
	restarted := f.restartSteps(t, writer)
	err := restarted.Reconcile(t.Context())
	if !errors.Is(err, store.ErrNoStep) {
		t.Fatalf("Reconcile error = %v, want it to match ErrNoStep for the dropped end", err)
	}

	row, err := restarted.Status(t.Context(), kept)
	if err != nil {
		t.Fatalf("Status %s: %v", kept, err)
	}
	if row.End != "engine_restarted" {
		t.Errorf("kept step %s end = %q, want engine_restarted", kept, row.End)
	}
}
