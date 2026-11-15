package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepCtlNoticeRouter is the Router a Notifier hands notices to in these tests. It keeps the notices it is given.
type stepCtlNoticeRouter struct {
	notices []route.Notice
}

func (r *stepCtlNoticeRouter) Notify(_ context.Context, n route.Notice) {
	r.notices = append(r.notices, n)
}

func (r *stepCtlNoticeRouter) Changed(notice.Key) {}

func (r *stepCtlNoticeRouter) Seen(context.Context, notice.Key, int64) {}

// events returns the notice events the Router was handed, decoded from the source data each notice carries.
func (r *stepCtlNoticeRouter) events(t *testing.T) []Notice {
	t.Helper()
	events := make([]Notice, 0, len(r.notices))
	for _, n := range r.notices {
		var event Notice
		if err := json.Unmarshal(n.Source.Data, &event); err != nil {
			t.Fatalf("decode notice source: %v", err)
		}
		events = append(events, event)
	}
	return events
}

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

// noticeSteps returns a Steps over the fixture's store and sessions, with writer and notifier. It is closed at cleanup.
func (f *stepCtlFixture) noticeSteps(t *testing.T, writer *Writer, notifier *Notifier) *Steps {
	t.Helper()
	steps, err := NewSteps(StepsConfig{
		Sessions:     f.sessions,
		Store:        f.store,
		Writer:       writer,
		Pools:        f.pools,
		Worktrees:    f.worktrees,
		Environ:      f.environ,
		Logger:       slog.New(slog.DiscardHandler),
		GateStateDir: f.gateStateDir,
		Notifier:     notifier,
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(steps.Close)
	return steps
}

func TestStepsReconcileNoticesOnlyTheEpiclessRestartedStep(t *testing.T) {
	t.Parallel()
	f := newStepCtlFixture(t)
	repo := stepGitTestRepo(t)
	held := f.startNoticeStep(t, repo, "epic-a")
	free := f.startNoticeStep(t, repo, "")
	f.steps.Close()

	router := &stepCtlNoticeRouter{}
	restarted := f.noticeSteps(t, f.writer, NewNotifier(NotifierConfig{Route: router}))
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	want := Notice{Session: free, Dir: repo, State: "interrupted", Body: "orreryd restarted during the step"}
	if got := router.events(t); len(got) != 1 || got[0] != want {
		t.Errorf("notices = %+v, want exactly [%+v]", got, want)
	}
	row, err := restarted.Status(t.Context(), held)
	if err != nil {
		t.Fatalf("Status %s: %v", held, err)
	}
	if row.End != "engine_restarted" {
		t.Errorf("epic step %s end = %q, want engine_restarted", held, row.End)
	}
}

func TestStepsReconcileNoticesNoStepWhoseEndIsDropped(t *testing.T) {
	t.Parallel()
	f := newStepCtlFixture(t)
	repo := stepGitTestRepo(t)
	kept := f.startNoticeStep(t, repo, "")
	dropped := f.startNoticeStep(t, repo, "")
	f.steps.Close()

	writer := NewWriter(WriterConfig{
		Applier: &stepCtlDropApplier{db: f.store, dropID: dropped},
		Tick:    5 * time.Millisecond,
	})
	startRun(t, writer)
	router := &stepCtlNoticeRouter{}
	restarted := f.noticeSteps(t, writer, NewNotifier(NotifierConfig{Route: router}))
	err := restarted.Reconcile(t.Context())
	if !errors.Is(err, store.ErrNoStep) {
		t.Fatalf("Reconcile error = %v, want it to match ErrNoStep for the dropped end", err)
	}

	want := Notice{Session: kept, Dir: repo, State: "interrupted", Body: "orreryd restarted during the step"}
	if got := router.events(t); len(got) != 1 || got[0] != want {
		t.Errorf("notices = %+v, want exactly [%+v]", got, want)
	}
}
