package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// restartEndApplier stores ops through inner and records each ApplyEngine call. An op that ends the step dropID is
// refused as though its row were gone, so the writer drops that op and sends the rest of its call again.
type restartEndApplier struct {
	inner  Applier
	dropID string

	mu    sync.Mutex
	calls [][]store.EngineOp
}

func (a *restartEndApplier) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	a.mu.Lock()
	a.calls = append(a.calls, slices.Clone(ops))
	a.mu.Unlock()
	for i, op := range ops {
		if end, ok := op.(store.StepEnd); ok && a.dropID != "" && end.ID == a.dropID {
			return &store.EngineOpError{Index: i, Err: fmt.Errorf("step %s: %w", end.ID, store.ErrNoStep)}
		}
	}
	return a.inner.ApplyEngine(ctx, ops, now)
}

// recorded returns the ops of each ApplyEngine call so far, in call order.
func (a *restartEndApplier) recorded() [][]store.EngineOp {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}

// restartAdopterSpy is a StepAdopter that records each step ExpectStep unregisters, and reports no step live.
type restartAdopterSpy struct {
	mu           sync.Mutex
	unregistered []string
}

func (a *restartAdopterSpy) ExpectStep(id string, observer StepObserver) {
	if observer != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.unregistered = append(a.unregistered, id)
}

func (a *restartAdopterSpy) StepLive(string) bool {
	return false
}

// unregisteredIDs returns the ids ExpectStep unregistered, in call order.
func (a *restartAdopterSpy) unregisteredIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.unregistered)
}

// seedOpenSteps adds an open read-only step row with no worktree for each id, in one ApplyEngine call on s.
func seedOpenSteps(t *testing.T, s *store.Store, ids ...string) {
	t.Helper()
	ops := make([]store.EngineOp, 0, len(ids))
	for _, id := range ids {
		ops = append(ops, store.StepAdd{
			ID: id, Agent: "implementer", Prompt: "do it", Schema: `{"type":"object"}`,
			Workspace: "/ws", Revision: "abc123", DoneValue: "DONE", ReadOnly: true,
		})
	}
	if err := s.ApplyEngine(t.Context(), ops, time.Unix(1, 0)); err != nil {
		t.Fatalf("seed open steps: %v", err)
	}
}

// newBatchRestartSteps returns a Steps over s and writer. It is closed at cleanup.
func newBatchRestartSteps(t *testing.T, s *store.Store, writer *Writer) *Steps {
	t.Helper()
	steps, err := NewSteps(StepsConfig{
		Sessions:  &stepCtlSessions{stepStartSessions: &stepStartSessions{writer: writer}},
		Store:     s,
		Writer:    writer,
		Worktrees: t.TempDir(),
		Logger:    slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(steps.Close)
	return steps
}

func TestStepsReconcileEndsSixtyFourOpenRowsInOneApplyEngineCall(t *testing.T) {
	t.Parallel()
	const rows = 64
	for _, tc := range []struct {
		name string
		tick time.Duration
	}{
		{name: "1ms tick", tick: time.Millisecond},
		{name: "default tick"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, _ := openStore(t)
			ids := make([]string, rows)
			for i := range ids {
				ids[i] = fmt.Sprintf("%032d", i)
			}
			seedOpenSteps(t, s, ids...)
			applier := &restartEndApplier{inner: s}
			writer := NewWriter(WriterConfig{Applier: applier, Tick: tc.tick})
			startRun(t, writer)
			steps := newBatchRestartSteps(t, s, writer)

			if err := steps.Reconcile(t.Context()); err != nil {
				t.Fatalf("Reconcile: %v", err)
			}

			var restartedEnds []int
			for _, call := range applier.recorded() {
				n := 0
				for _, op := range call {
					if end, ok := op.(store.StepEnd); ok && end.End == stepRestartedEnd {
						n++
					}
				}
				if n > 0 {
					restartedEnds = append(restartedEnds, n)
				}
			}
			if !slices.Equal(restartedEnds, []int{rows}) {
				t.Errorf("ApplyEngine calls storing engine_restarted ends carry %v ends, want one call with %d",
					restartedEnds, rows)
			}
			for _, id := range ids {
				row, err := steps.Status(t.Context(), id)
				if err != nil {
					t.Fatalf("Status %s: %v", id, err)
				}
				if row.End != stepRestartedEnd {
					t.Errorf("step %s end = %q, want %s", id, row.End, stepRestartedEnd)
				}
			}
		})
	}
}

func TestStepsReconcileCleansUpOtherRowsWhenOneEndIsDropped(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	ids := []string{fmt.Sprintf("%032d", 0), fmt.Sprintf("%032d", 1), fmt.Sprintf("%032d", 2)}
	dropped := ids[1]
	applier := &restartEndApplier{inner: s, dropID: dropped}
	writer := NewWriter(WriterConfig{Applier: applier, Tick: time.Millisecond})
	startRun(t, writer)
	steps := newBatchRestartSteps(t, s, writer)
	// Readopt runs while no row is open, so it registers the adopter and rebuilds no run.
	adopter := &restartAdopterSpy{}
	if err := steps.Readopt(t.Context(), adopter); err != nil {
		t.Fatalf("Readopt: %v", err)
	}
	seedOpenSteps(t, s, ids...)

	err := steps.Reconcile(t.Context())
	if !errors.Is(err, store.ErrNoStep) {
		t.Fatalf("Reconcile error = %v, want it to match ErrNoStep", err)
	}
	for _, id := range ids {
		row, statusErr := steps.Status(t.Context(), id)
		if statusErr != nil {
			t.Fatalf("Status %s: %v", id, statusErr)
		}
		want := stepRestartedEnd
		if id == dropped {
			want = ""
		}
		if row.End != want {
			t.Errorf("step %s end = %q, want %q", id, row.End, want)
		}
	}
	if got, want := adopter.unregisteredIDs(), []string{ids[0], ids[2]}; !slices.Equal(got, want) {
		t.Errorf("unregistered steps = %v, want %v: the dropped row has no cleanup, the others do", got, want)
	}
}
