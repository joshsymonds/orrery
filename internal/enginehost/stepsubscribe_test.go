package enginehost

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// stepPushA and stepPushB are the ids of the open step rows the subscription tests store.
const (
	stepPushA = "0a1b2c3d4e5f60718293a4b5c6d7e8f9"
	stepPushB = "f9e8d7c6b5a4938271605f4e3d2c1b0a"
)

// stepPushOpen stores an open step row with id in s, with no loop, as a restart leaves it.
func stepPushOpen(t *testing.T, s *store.Store, id string) {
	t.Helper()
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineAdd{ID: id, Dir: "/work", State: "working", Kind: store.KindStep},
		store.StepAdd{
			ID: id, Agent: "implementer", Prompt: "do it", Schema: stepLoopSchema, Workspace: "/ws",
			Revision: "abc123", DoneValue: stepLoopDone,
		},
	}, time.Unix(1, 0)); err != nil {
		t.Fatalf("add step row %s: %v", id, err)
	}
}

// stepPushGate is a StepStore whose first Step holds until release closes. It closes entered once it holds that read, so
// a test can run other work while the read waits. Later reads pass through.
type stepPushGate struct {
	StepStore
	held    atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (g *stepPushGate) Step(ctx context.Context, id string) (store.StepRow, error) {
	if g.held.CompareAndSwap(false, true) {
		close(g.entered)
		<-g.release
	}
	return g.StepStore.Step(ctx, id)
}

// TestStepEndPushSubscribeReportsStoredEnd covers a subscription opened after a step's end is stored: it reports that row
// once.
func TestStepEndPushSubscribeReportsStoredEnd(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	stepPushOpen(t, f.store, stepPushA)
	if err := f.steps.Stop(t.Context(), stepPushA); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	sub, err := f.steps.Subscribe(t.Context(), []string{stepPushA})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	select {
	case <-sub.Ready():
	default:
		t.Fatal("Ready is not signaled for the stored end")
	}
	rows := sub.Take()
	if len(rows) != 1 || rows[0].ID != stepPushA || rows[0].End != stepStoppedEnd {
		t.Fatalf("Take = %+v, want the stopped row of %s once", rows, stepPushA)
	}
	if again := sub.Take(); len(again) != 0 {
		t.Fatalf("second Take = %d rows, want none", len(again))
	}
}

// TestStepEndPushSubscribeInterleavedEndReportsOnce covers a subscription opened while an end is being stored: its read of
// the row waits until the end is stored and published, then reports the end, and the end is reported exactly once.
func TestStepEndPushSubscribeInterleavedEndReportsOnce(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	stepPushOpen(t, f.store, stepPushA)
	gate := &stepPushGate{StepStore: f.store, entered: make(chan struct{}), release: make(chan struct{})}
	f.steps.store = gate
	type opened struct {
		sub *StepSubscription
		err error
	}
	done := make(chan opened, 1)
	go func() {
		sub, err := f.steps.Subscribe(t.Context(), []string{stepPushA})
		done <- opened{sub: sub, err: err}
	}()
	receive(t, gate.entered)
	// The subscription is registered and its read of the row is held, so the end is stored and published while it waits.
	if err := f.steps.Stop(t.Context(), stepPushA); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	close(gate.release)
	got := receive(t, done)
	if got.err != nil {
		t.Fatalf("Subscribe: %v", got.err)
	}
	defer got.sub.Close()
	rows := got.sub.Take()
	if len(rows) != 1 || rows[0].End != stepStoppedEnd {
		t.Fatalf("Take = %+v, want the stopped row of %s exactly once", rows, stepPushA)
	}
}

// TestStepEndPushSubscribeReportsLaterEnd covers an end stored after a subscription is opened: the open row is not
// reported, and the end is reported when it is stored.
func TestStepEndPushSubscribeReportsLaterEnd(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	stepPushOpen(t, f.store, stepPushA)
	sub, err := f.steps.Subscribe(t.Context(), []string{stepPushA})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	if rows := sub.Take(); len(rows) != 0 {
		t.Fatalf("Take of an open step = %d rows, want none", len(rows))
	}
	if err = f.steps.Stop(t.Context(), stepPushA); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case <-sub.Ready():
	default:
		t.Fatal("Ready is not signaled for the end stored after Subscribe")
	}
	rows := sub.Take()
	if len(rows) != 1 || rows[0].ID != stepPushA || rows[0].End != stepStoppedEnd {
		t.Fatalf("Take = %+v, want the stopped row of %s once", rows, stepPushA)
	}
}

// TestStepEndPushSubscribeReportsLimitHold covers a hold stored by limitTurn: the subscription reports the held row, with
// LoopLimited set and no end.
func TestStepEndPushSubscribeReportsLimitHold(t *testing.T) {
	t.Parallel()
	f := newStepLimitFixture(t)
	steps, err := NewSteps(StepsConfig{Store: f.store, Writer: f.writer, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	f.run.publish = steps.publish
	sub, err := steps.Subscribe(t.Context(), []string{stepLoopID})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	f.start(t)
	limitTurn(f.run)
	receive(t, sub.Ready())
	rows := sub.Take()
	if len(rows) != 1 || rows[0].ID != stepLoopID || !rows[0].LoopLimited || rows[0].End != "" {
		t.Fatalf("Take = %+v, want the held row of %s with LoopLimited set and no end", rows, stepLoopID)
	}
}

// TestStepEndPushSubscribeReportsRestartedEnd covers an engine_restarted end stored by Reconcile's endRestarted: the
// subscription reports the row.
func TestStepEndPushSubscribeReportsRestartedEnd(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	stepPushOpen(t, f.store, stepPushA)
	sub, err := f.steps.Subscribe(t.Context(), []string{stepPushA})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	if err = f.steps.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	rows := sub.Take()
	if len(rows) != 1 || rows[0].ID != stepPushA || rows[0].End != stepRestartedEnd {
		t.Fatalf("Take = %+v, want the engine_restarted row of %s once", rows, stepPushA)
	}
}

// TestStepEndPushSubscribeStoredPerPublish covers Stored: it is called once for each end published, by Stop and by each
// row Reconcile ends.
func TestStepEndPushSubscribeStoredPerPublish(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	calls := 0
	f.steps.stored = func() { calls++ }
	stepPushOpen(t, f.store, stepPushA)
	stepPushOpen(t, f.store, stepPushB)
	if err := f.steps.Stop(t.Context(), stepPushA); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if calls != 1 {
		t.Fatalf("Stored ran %d times after one end, want 1", calls)
	}
	if err := f.steps.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if calls != 2 {
		t.Fatalf("Stored ran %d times after the end of the second step, want 2", calls)
	}
}

// TestStepEndPushSubscribeCloseStopsReports covers Close: a closed subscription reports no end stored after Close, and a
// subscription left open reports the same ends, so the closed one is not silent for want of publishes. Close is called
// twice.
func TestStepEndPushSubscribeCloseStopsReports(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	stepPushOpen(t, f.store, stepPushA)
	stepPushOpen(t, f.store, stepPushB)
	ids := []string{stepPushA, stepPushB}
	closed, err := f.steps.Subscribe(t.Context(), ids)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	open, err := f.steps.Subscribe(t.Context(), ids)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer open.Close()
	closed.Close()
	closed.Close()
	if err = f.steps.Stop(t.Context(), stepPushA); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err = f.steps.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rows := open.Take(); len(rows) != 2 {
		t.Fatalf("open subscription took %d rows, want the 2 ends", len(rows))
	}
	select {
	case <-closed.Ready():
		t.Fatal("Ready is signaled on a closed subscription")
	default:
	}
	if rows := closed.Take(); len(rows) != 0 {
		t.Fatalf("closed subscription took %d rows, want none", len(rows))
	}
}

// TestStepEndPushSubscribeUnknownIDRefused covers an id with no row: Subscribe returns an error matching store.ErrNoStep,
// and leaves nothing registered.
func TestStepEndPushSubscribeUnknownIDRefused(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	stepPushOpen(t, f.store, stepPushA)
	_, err := f.steps.Subscribe(t.Context(), []string{stepPushA, "00000000000000000000000000000000"})
	if !errors.Is(err, store.ErrNoStep) {
		t.Fatalf("Subscribe of an unknown id = %v, want ErrNoStep", err)
	}
	f.steps.mu.Lock()
	registered := len(f.steps.subs)
	f.steps.mu.Unlock()
	if registered != 0 {
		t.Fatalf("%d subscriptions registered after a refused Subscribe, want none", registered)
	}
}
