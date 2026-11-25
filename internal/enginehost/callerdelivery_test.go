package enginehost

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// armedRetry is a retry timer the fake NewTimer of a callerRun armed: its duration, and the channel the test fires it on.
type armedRetry struct {
	d    time.Duration
	fire chan time.Time
}

// retryClock is the NewTimer of the delivery tests. It arms no real timer: each timer it arms is sent on armed, for the
// test to fire.
type retryClock struct {
	armed chan armedRetry
}

// newTimer arms a fake timer for d, and sends it on armed.
func (c *retryClock) newTimer(d time.Duration) (<-chan time.Time, func() bool) {
	fire := make(chan time.Time, 1)
	c.armed <- armedRetry{d: d, fire: fire}
	return fire, func() bool { return true }
}

// noRetryArmed fails the test when a retry timer has been armed. The test calls it after a round it has waited for, and
// after the round that follows that one, so the decision of the first round has been made.
func (c *retryClock) noRetryArmed(t *testing.T) {
	t.Helper()
	select {
	case retry := <-c.armed:
		t.Fatalf("retry armed for %v, want none", retry.d)
	default:
	}
}

// roundProbe runs pass, then sends on ended, so the test waits for each round to end.
type roundProbe struct {
	pass  CallerPass
	ended chan struct{}
}

// Pass runs the pass, then reports the round's end.
func (p roundProbe) Pass(ctx context.Context) error {
	err := p.pass.Pass(ctx)
	p.ended <- struct{}{}
	return err
}

// passFunc is a CallerPass that runs its function.
type passFunc func(context.Context) error

// Pass runs the function.
func (f passFunc) Pass(ctx context.Context) error { return f(ctx) }

// callerRun is a CallerEvents over one pass, running on its own goroutine until the test ends. Each round sends on
// ended once it has run, and its retries are armed on clock.
type callerRun struct {
	events *CallerEvents
	clock  *retryClock
	ended  chan struct{}
}

// startCallerRun runs a CallerEvents over pass on its own goroutine, and waits for it to return when the test ends.
func startCallerRun(t *testing.T, pass CallerPass) *callerRun {
	t.Helper()
	ended := make(chan struct{}, 8)
	clock := &retryClock{armed: make(chan armedRetry, 8)}
	events := NewCallerEvents(CallerEventsConfig{
		Passes:   []CallerPass{roundProbe{pass: pass, ended: ended}},
		NewTimer: clock.newTimer,
	})
	ctx := t.Context()
	done := make(chan struct{})
	go func() {
		defer close(done)
		events.Run(ctx)
	}()
	t.Cleanup(func() { <-done })
	return &callerRun{events: events, clock: clock, ended: ended}
}

// await waits for the round under way to end.
func (r *callerRun) await() { <-r.ended }

// wake asks for a round, and waits for it to end.
func (r *callerRun) wake() {
	r.events.Wake()
	<-r.ended
}

// addCallerEngineRow stores a headless session with row id, whose turns have ended, so it is idle.
func addCallerEngineRow(t *testing.T, s *store.Store, id string) {
	t.Helper()
	add := store.EngineAdd{ID: id, Dir: t.TempDir(), State: "done", Kind: store.KindEngine}
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{add}, fixedClock()); err != nil {
		t.Fatalf("ApplyEngine add session %s: %v", id, err)
	}
}

// storeWithEndedStep returns a store in which the idle headless session "caller" called for step-1, which has ended.
func storeWithEndedStep(t *testing.T) *store.Store {
	t.Helper()
	s, _ := openStore(t)
	addCallerEngineRow(t, s, "caller")
	addEndedCallerStep(t, s, store.EpicCaller{Session: "caller"}, "all green")
	return s
}

// deliveryLog is the Deliver of the delivery tests. It records each request, and refuses the request at each index of
// refusals with the error at that index, and accepts the rest.
type deliveryLog struct {
	refusals []error
	requests []DeliverRequest
}

// deliver records req, and returns the refusal of its index, if any.
func (l *deliveryLog) deliver(_ context.Context, req DeliverRequest) error {
	index := len(l.requests)
	l.requests = append(l.requests, req)
	if index < len(l.refusals) {
		return l.refusals[index]
	}
	return nil
}

// deliverSender returns a CallerSender that delivers to headless callers through deliver. It fails the test if a pane
// is woken, as the delivery tests have no pane caller.
func deliverSender(t *testing.T, s *store.Store, deliver func(context.Context, DeliverRequest) error) *CallerSender {
	t.Helper()
	return NewCallerSender(CallerSenderConfig{
		Deliver: deliver,
		WakePane: func(context.Context, string, string, string, string) (bool, error) {
			t.Error("a pane caller was woken in a headless delivery test")
			return false, nil
		},
		Store: s,
		Now:   fixedClock,
	})
}

// flakyCallerSteps is a StepCallerStore whose first read fails, and whose later reads are the store's.
type flakyCallerSteps struct {
	StepCallerStore
	refused bool
}

// CallerSteps fails its first read, and reads the store after that.
func (f *flakyCallerSteps) CallerSteps(ctx context.Context) ([]store.CallerStep, error) {
	if !f.refused {
		f.refused = true
		return nil, errors.New("read refused")
	}
	return f.StepCallerStore.CallerSteps(ctx)
}

func TestStepEndPushDeliveryStartPassSendsIdleHeadless(t *testing.T) {
	t.Parallel()
	s := storeWithEndedStep(t)
	log := &deliveryLog{}
	run := startCallerRun(t, stepEndPass(deliverSender(t, s, log.deliver), s))

	run.await()
	want := []DeliverRequest{{ID: "caller", Text: wantStepEndBlock("all green"), PromptID: callerStepEvent}}
	if !reflect.DeepEqual(log.requests, want) {
		t.Fatalf("requests of the start pass = %+v, want %+v", log.requests, want)
	}
	if !eventMarked(t, s, "caller") {
		t.Fatalf("step end not marked after its delivery")
	}
}

func TestStepEndPushDeliveryWakeRunsPassForLaterEnd(t *testing.T) {
	t.Parallel()
	s := storeWithEndedStep(t)
	log := &deliveryLog{}
	run := startCallerRun(t, stepEndPass(deliverSender(t, s, log.deliver), s))
	run.await()

	endCallerStep(t, s, store.StepAdd{
		ID: "step-2", Agent: "implementer", Prompt: "do it", Caller: store.EpicCaller{Session: "caller"},
	}, "stored after the start")
	if len(log.requests) != 1 {
		t.Fatalf("requests before the wake = %d, want the start's one", len(log.requests))
	}

	run.wake()
	events := make([]string, 0, len(log.requests))
	for _, req := range log.requests {
		events = append(events, req.PromptID)
	}
	if want := []string{callerStepEvent, "step:step-2"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events delivered after the wake = %q, want %q", events, want)
	}
}

func TestStepEndPushDeliveryWakesDuringPassCollapse(t *testing.T) {
	t.Parallel()
	calls := 0 // read and written by the run's goroutine only
	begun := make(chan struct{})
	gate := make(chan struct{})
	run := startCallerRun(t, passFunc(func(context.Context) error {
		calls++
		if calls == 2 {
			begun <- struct{}{}
			<-gate
		}
		return nil
	}))
	run.await()

	run.events.Wake()
	<-begun
	// Three wakes come while the second round is gated. The first is pending, and the other two are absorbed by it.
	run.events.Wake()
	run.events.Wake()
	run.events.Wake()
	gate <- struct{}{}
	run.await()
	run.await()

	if calls != 3 {
		t.Fatalf("passes = %d, want the start and one round for the wakes during the second", calls)
	}
	if pending := len(run.events.wakes); pending != 0 {
		t.Fatalf("wakes pending after the round they collapsed into = %d, want none", pending)
	}
}

func TestStepEndPushDeliveryFailedSendRetriesOnce(t *testing.T) {
	t.Parallel()
	s := storeWithEndedStep(t)
	log := &deliveryLog{refusals: []error{errors.New("refused by the session")}}
	run := startCallerRun(t, stepEndPass(deliverSender(t, s, log.deliver), s))
	run.await()

	retry := <-run.clock.armed
	if retry.d != callerEventRetry {
		t.Fatalf("retry armed for %v, want callerEventRetry %v", retry.d, callerEventRetry)
	}
	if eventMarked(t, s, "caller") {
		t.Fatalf("step end marked after its send failed")
	}

	retry.fire <- time.Time{}
	run.await()
	if len(log.requests) != 2 || !reflect.DeepEqual(log.requests[0], log.requests[1]) {
		t.Fatalf("requests = %+v, want the failed send and its one resend", log.requests)
	}
	if !eventMarked(t, s, "caller") {
		t.Fatalf("step end not marked after the retry resent it")
	}

	run.wake()
	run.clock.noRetryArmed(t)
}

func TestStepEndPushDeliveryFailedReadRetriesOnce(t *testing.T) {
	t.Parallel()
	s := storeWithEndedStep(t)
	log := &deliveryLog{}
	steps := &flakyCallerSteps{StepCallerStore: s}
	run := startCallerRun(t, stepEndPass(deliverSender(t, s, log.deliver), steps))
	run.await()

	retry := <-run.clock.armed
	if retry.d != callerEventRetry {
		t.Fatalf("retry armed for %v, want callerEventRetry %v", retry.d, callerEventRetry)
	}
	if len(log.requests) != 0 {
		t.Fatalf("requests after a failed read = %d, want none", len(log.requests))
	}

	retry.fire <- time.Time{}
	run.await()
	if !eventMarked(t, s, "caller") {
		t.Fatalf("step end not marked after the retry read and sent it")
	}

	run.wake()
	run.clock.noRetryArmed(t)
}

func TestStepEndPushDeliveryHeldSendArmsNoRetry(t *testing.T) {
	t.Parallel()
	s := storeWithEndedStep(t)
	log := &deliveryLog{refusals: []error{ErrSessionBusy, ErrSessionBusy}}
	run := startCallerRun(t, stepEndPass(deliverSender(t, s, log.deliver), s))
	run.await()
	if eventMarked(t, s, "caller") {
		t.Fatalf("step end marked while its caller was busy")
	}

	run.wake()
	run.clock.noRetryArmed(t)
	if len(log.requests) != 2 {
		t.Fatalf("requests = %d, want the start's and the wake's", len(log.requests))
	}
	if eventMarked(t, s, "caller") {
		t.Fatalf("step end marked while its caller stayed busy")
	}
}
