package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepLoopID is a step id in the form checkID accepts.
const stepLoopID = "3123456789abcdef0123456789abcdef"

// stepLoopSchema requires an object with a string status, which is the shape every step schema has.
const stepLoopSchema = `{"type":"object","properties":{"status":{"type":"string"}}}`

// stepLoopDone is the status that ends a step done.
const stepLoopDone = "DONE"

// stepLoopSessions is a fake stepSessions that records every Continue and Stop.
type stepLoopSessions struct {
	mu           sync.Mutex
	continued    []string
	interrupted  []string
	stopped      []string
	continueErr  error
	interruptErr error
	// cancelOnContinue, when set, cancels the run's ctx during Continue, and Continue then returns that ctx's error.
	cancelOnContinue context.CancelFunc
}

func (f *stepLoopSessions) StartStep(context.Context, StepSession) error {
	return errors.New("stepLoopSessions: StartStep is not used by the loop")
}

func (f *stepLoopSessions) Interrupt(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != stepLoopID {
		return errors.New("stepLoopSessions: Interrupt for unexpected step " + id)
	}
	f.interrupted = append(f.interrupted, id)
	return f.interruptErr
}

func (f *stepLoopSessions) Continue(ctx context.Context, id, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != stepLoopID {
		return errors.New("stepLoopSessions: Continue for unexpected step " + id)
	}
	f.continued = append(f.continued, text)
	if f.cancelOnContinue != nil {
		f.cancelOnContinue()
		return ctx.Err()
	}
	return f.continueErr
}

func (f *stepLoopSessions) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	return nil
}

// continuedTexts returns a copy of the texts Continue was called with.
func (f *stepLoopSessions) continuedTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.continued...)
}

// interruptedIDs returns a copy of the ids Interrupt was called with.
func (f *stepLoopSessions) interruptedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.interrupted...)
}

// stoppedIDs returns a copy of the ids Stop was called with.
func (f *stepLoopSessions) stoppedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stopped...)
}

// stepLoopFixture is a stepRun over a real store and writer, with a step row already recorded.
type stepLoopFixture struct {
	run      *stepRun
	store    *store.Store
	writer   *Writer
	sessions *stepLoopSessions
	cleaned  int
}

// newStepLoopFixture returns a fixture whose run has a step row, a real writer and a cleanup that counts its calls.
func newStepLoopFixture(t *testing.T, continueErr error) *stepLoopFixture {
	t.Helper()
	s, _ := openStore(t)
	ctx := t.Context()
	if err := s.ApplyEngine(ctx, []store.EngineOp{
		store.EngineAdd{ID: stepLoopID, Dir: "/work", State: "working", Kind: store.KindStep},
		store.StepAdd{
			ID: stepLoopID, Agent: "implementer", Prompt: "do it", Schema: stepLoopSchema, Workspace: "/ws",
			Revision: "abc123", Worktree: "/wt", DoneValue: stepLoopDone,
		},
	}, time.Unix(1, 0)); err != nil {
		t.Fatalf("add step row: %v", err)
	}
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	fixture := &stepLoopFixture{store: s, writer: writer, sessions: &stepLoopSessions{continueErr: continueErr}}
	cleanup := func(context.Context) error {
		fixture.cleaned++
		return nil
	}
	fixture.run = newStepRun(stepRunConfig{
		id: stepLoopID, schema: stepLoopSchema, doneValue: stepLoopDone, sessions: fixture.sessions, writer: writer,
		logger: slog.New(slog.DiscardHandler), cleanup: cleanup,
	})
	return fixture
}

// start runs the fixture's run until the returned cancel is called, and returns a channel closed when run returns.
func (f *stepLoopFixture) start(t *testing.T) (cancel context.CancelFunc, done <-chan struct{}) {
	t.Helper()
	ctx, cancelRun := context.WithCancel(t.Context())
	t.Cleanup(cancelRun)
	return cancelRun, f.runOn(ctx)
}

// runOn runs the fixture's run on ctx and returns a channel closed when run returns.
func (f *stepLoopFixture) runOn(ctx context.Context) <-chan struct{} {
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		f.run.run(ctx)
	}()
	return finished
}

// settle returns once every op queued before the call is stored. The writer stores ops in queue order, so a barrier
// op that is stored has been preceded by all earlier ops. EngineTextClear touches no row of a step with no text.
func (f *stepLoopFixture) settle(t *testing.T) {
	t.Helper()
	if err := f.writer.Commit(t.Context(), stepLoopID, store.EngineTextClear{ID: stepLoopID}); err != nil {
		t.Fatalf("settle writer: %v", err)
	}
}

// waitRunEnd fails the test unless run returns within waitTimeout.
func waitRunEnd(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("run did not return")
	}
}

// row returns the step row the fixture's store holds.
func (f *stepLoopFixture) row(t *testing.T) store.StepRow {
	t.Helper()
	row, err := f.store.Step(t.Context(), stepLoopID)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	return row
}

func TestStepLoopDoneReturnEndsDone(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	_, done := f.start(t)
	raw := json.RawMessage(`{"status":"DONE"}`)

	f.run.Return(session.TurnEnded{StructuredOutput: raw})
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndDone || row.Status != stepLoopDone || row.Reason != "" {
		t.Fatalf("end = %q status %q reason %q, want done DONE with no reason", row.End, row.Status, row.Reason)
	}
	if row.Result != string(raw) {
		t.Errorf("result = %s, want %s", row.Result, raw)
	}
	wantChecks := []store.StepCheck{{Round: 1, Name: stepLoopSchemaName, OK: true}}
	if !reflect.DeepEqual(row.Checks, wantChecks) {
		t.Errorf("checks = %+v, want %+v", row.Checks, wantChecks)
	}
	if got := f.sessions.continuedTexts(); len(got) != 0 {
		t.Errorf("Continue called %d times for a done return, want 0", len(got))
	}
	if got, want := f.sessions.stoppedIDs(), []string{stepLoopID}; !reflect.DeepEqual(got, want) {
		t.Errorf("stopped = %v, want %v", got, want)
	}
	if f.cleaned != 1 {
		t.Errorf("cleanup ran %d times, want 1", f.cleaned)
	}
	select {
	case <-f.run.ended:
	default:
		t.Error("ended is not closed after a done end")
	}
}

func TestStepLoopOtherStatusEndsReturned(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	_, done := f.start(t)
	raw := json.RawMessage(`{"status":"PARTIAL"}`)

	f.run.Return(session.TurnEnded{StructuredOutput: raw})
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndReturned || row.Status != "PARTIAL" {
		t.Fatalf("end = %q status %q, want returned PARTIAL", row.End, row.Status)
	}
	if row.Result != string(raw) {
		t.Errorf("result = %s, want %s", row.Result, raw)
	}
	if len(row.Checks) != 1 || !row.Checks[0].OK {
		t.Errorf("checks = %+v, want one passing schema check", row.Checks)
	}
	if got := f.sessions.continuedTexts(); len(got) != 0 {
		t.Errorf("Continue called %d times for a returned end, want 0", len(got))
	}
}

func TestStepLoopSchemaFailureThenGoodReturnIsDone(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	_, done := f.start(t)

	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":1}`)})
	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE"}`)})
	waitRunEnd(t, done)

	continued := f.sessions.continuedTexts()
	if len(continued) != 1 {
		t.Fatalf("Continue called %d times, want 1", len(continued))
	}
	if !strings.Contains(continued[0], stepLoopSchemaName) {
		t.Errorf("Continue text %q does not name the schema check", continued[0])
	}
	row := f.row(t)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want done", row.End)
	}
	if len(row.Checks) != 2 {
		t.Fatalf("checks = %+v, want both rounds kept", row.Checks)
	}
	if row.Checks[0].Round != 1 || row.Checks[0].OK || row.Checks[0].Detail == "" {
		t.Errorf("round 1 check = %+v, want a failing schema check with a detail", row.Checks[0])
	}
	if row.Checks[1].Round != 2 || !row.Checks[1].OK {
		t.Errorf("round 2 check = %+v, want a passing schema check", row.Checks[1])
	}
}

func TestStepLoopFourFailuresEndChecksFailed(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	_, done := f.start(t)

	for range 4 {
		f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":1}`)})
	}
	waitRunEnd(t, done)

	if got := len(f.sessions.continuedTexts()); got != 3 {
		t.Fatalf("Continue called %d times, want 3", got)
	}
	row := f.row(t)
	if row.End != stepLoopEndChecksFailed {
		t.Fatalf("end = %q, want checks_failed", row.End)
	}
	if !strings.Contains(row.Reason, stepLoopSchemaName) {
		t.Errorf("reason %q does not name the failed schema check", row.Reason)
	}
	if len(row.Checks) != 4 {
		t.Fatalf("checks = %+v, want one per round", row.Checks)
	}
	for i, check := range row.Checks {
		if check.Round != i+1 || check.OK {
			t.Errorf("check %d = %+v, want round %d failing", i, check, i+1)
		}
	}
}

func TestStepRunEventsReachWatch(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	clock := newIdleClock()
	cfg := f.run.stepRunConfig
	cfg.now = clock.now
	cfg.modelLimit = time.Hour
	f.run = newStepRun(cfg)

	f.run.Event(session.Started{})
	f.run.Event(session.Block{Kind: session.BlockText, Text: "reply text"})
	if got := f.run.watch.recent(clock.now()); !strings.Contains(got, "reply text") {
		t.Fatalf("recent = %q, want the reply block", got)
	}
	clock.advance(5 * time.Minute)
	trip, ok := f.run.watch.due(clock.now())
	if !ok || trip.Kind != watchTripPeriodic {
		t.Fatalf("due = %+v, %v, want a periodic trip after five minutes of turn time", trip, ok)
	}
}

func TestStepLoopExitedEndsFailed(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	_, done := f.start(t)

	f.run.Exited("claude exited: status 1")
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndFailed || row.Reason != "claude exited: status 1" {
		t.Fatalf("end = %q reason %q, want failed with the exit reason", row.End, row.Reason)
	}
	if got, want := f.sessions.stoppedIDs(), []string{stepLoopID}; !reflect.DeepEqual(got, want) {
		t.Errorf("stopped = %v, want %v", got, want)
	}
	if f.cleaned != 1 {
		t.Errorf("cleanup ran %d times, want 1", f.cleaned)
	}
}

func TestStepLoopCancelStoresNoEnd(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	cancel, done := f.start(t)

	cancel()
	waitRunEnd(t, done)
	f.settle(t)

	row := f.row(t)
	if row.End != "" || !row.Ended.IsZero() {
		t.Fatalf("end = %q ended %v, want no end after a canceled ctx", row.End, row.Ended)
	}
	if f.cleaned != 0 {
		t.Errorf("cleanup ran %d times after a canceled ctx, want 0", f.cleaned)
	}
	select {
	case <-f.run.ended:
		t.Error("ended is closed after a canceled ctx, want it open")
	default:
	}
}

// assertNoEndAfterCancel checks that a run whose ctx ended before it acted stored no end, ran no cleanup and left
// ended open.
func (f *stepLoopFixture) assertNoEndAfterCancel(t *testing.T) {
	t.Helper()
	f.settle(t)
	row := f.row(t)
	if row.End != "" || !row.Ended.IsZero() {
		t.Errorf("end = %q ended %v, want no end after a canceled ctx", row.End, row.Ended)
	}
	if f.cleaned != 0 {
		t.Errorf("cleanup ran %d times after a canceled ctx, want 0", f.cleaned)
	}
	select {
	case <-f.run.ended:
		t.Error("ended is closed after a canceled ctx, want it open")
	default:
	}
}

func TestStepLoopCanceledCtxWithExitQueuedStoresNoEnd(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f.run.Exited("claude exited: status 1")

	waitRunEnd(t, f.runOn(ctx))

	f.assertNoEndAfterCancel(t)
}

func TestStepLoopCanceledCtxWithReturnQueuedStoresNoEnd(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE"}`)})

	waitRunEnd(t, f.runOn(ctx))

	f.assertNoEndAfterCancel(t)
}

func TestStepLoopCtxCanceledDuringContinueStoresNoEnd(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	f.sessions.cancelOnContinue = cancel

	done := f.runOn(ctx)
	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":1}`)})
	waitRunEnd(t, done)

	if got := len(f.sessions.continuedTexts()); got != 1 {
		t.Fatalf("Continue called %d times, want 1", got)
	}
	f.assertNoEndAfterCancel(t)
}

func TestStepLoopReturnAndExitedDoNotBlockAfterRunReturns(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	waitRunEnd(t, f.runOn(ctx))

	// More calls than either buffer holds, so a call that waits on a full buffer would hang.
	calls := make(chan struct{})
	go func() {
		defer close(calls)
		for range 4 * stepLoopBuffer {
			f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE"}`)})
			f.run.Exited("claude exited: status 1")
		}
	}()
	select {
	case <-calls:
	case <-time.After(waitTimeout):
		t.Fatal("Return or Exited blocked after run returned")
	}
}

func TestStepLoopContinueErrorEndsFailed(t *testing.T) {
	f := newStepLoopFixture(t, errors.New("session refused"))
	_, done := f.start(t)

	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":1}`)})
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndFailed || !strings.Contains(row.Reason, "session refused") {
		t.Fatalf("end = %q reason %q, want failed naming the Continue error", row.End, row.Reason)
	}
}

func TestStepLoopFinishActsOnce(t *testing.T) {
	f := newStepLoopFixture(t, nil)
	ctx := t.Context()

	if !f.run.finish(ctx, stepLoopEndDone, stepLoopDone, "") {
		t.Fatal("first finish did nothing")
	}
	if f.run.finish(ctx, stepLoopEndFailed, "", "second") {
		t.Fatal("second finish acted")
	}
	row := f.row(t)
	if row.End != stepLoopEndDone || row.Reason != "" {
		t.Errorf("end = %q reason %q, want the first finish's done with no reason", row.End, row.Reason)
	}
	if got := f.sessions.stoppedIDs(); len(got) != 1 {
		t.Errorf("Stop called %d times, want 1", len(got))
	}
	if f.cleaned != 1 {
		t.Errorf("cleanup ran %d times, want 1", f.cleaned)
	}
}
