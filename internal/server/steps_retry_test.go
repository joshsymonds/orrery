package server

import (
	"context"
	"errors"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// retryClock is an rpcretry.Clock whose time moves only when a retry waits: each After call advances it by the wait and
// fires at once. The handlers run on server goroutines, so the time is guarded.
type retryClock struct {
	mu  sync.Mutex
	now time.Time
}

// newRetryClock returns a retryClock at the unix epoch.
func newRetryClock() *retryClock {
	return &retryClock{now: time.Unix(0, 0)}
}

// Now returns the clock's time.
func (c *retryClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After advances the clock by d and returns a channel that has already fired.
func (c *retryClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	fired := make(chan time.Time, 1)
	fired <- c.now
	return fired
}

// retryFake is a StepClient that fails or stalls the next calls of an op before the wrapped client sees them. failures
// answers the next calls of an op with each queued error in turn: a restart's refused dial or unavailable answer, before
// anything is sent. stalls makes the next calls of an op wait for their context to end and then answer unavailable: a
// connection cut while the call is in flight.
type retryFake struct {
	StepClient
	failures map[string][]error
	stalls   map[string]int
}

// before answers a call of op with its queued failure, or waits out a stall, and returns nil when the call proceeds to
// the wrapped client.
func (f *retryFake) before(ctx context.Context, op string) error {
	if queued := f.failures[op]; len(queued) > 0 {
		f.failures[op] = queued[1:]
		return queued[0]
	}
	if f.stalls[op] > 0 {
		f.stalls[op]--
		<-ctx.Done()
		return connect.NewError(connect.CodeUnavailable, ctx.Err())
	}
	return nil
}

func (f *retryFake) StepStatus(
	ctx context.Context,
	req *connect.Request[enginev1.StepStatusRequest],
) (*connect.Response[enginev1.StepStatusResponse], error) {
	if err := f.before(ctx, "status"); err != nil {
		return nil, err
	}
	return f.StepClient.StepStatus(ctx, req)
}

func (f *retryFake) ListSteps(
	ctx context.Context,
	req *connect.Request[enginev1.ListStepsRequest],
) (*connect.Response[enginev1.ListStepsResponse], error) {
	if err := f.before(ctx, "list"); err != nil {
		return nil, err
	}
	return f.StepClient.ListSteps(ctx, req)
}

func (f *retryFake) HoldStep(
	ctx context.Context,
	req *connect.Request[enginev1.HoldStepRequest],
) (*connect.Response[enginev1.HoldStepResponse], error) {
	if err := f.before(ctx, "hold"); err != nil {
		return nil, err
	}
	return f.StepClient.HoldStep(ctx, req)
}

func (f *retryFake) NudgeStep(
	ctx context.Context,
	req *connect.Request[enginev1.NudgeStepRequest],
) (*connect.Response[enginev1.NudgeStepResponse], error) {
	if err := f.before(ctx, "nudge"); err != nil {
		return nil, err
	}
	return f.StepClient.NudgeStep(ctx, req)
}

func (f *retryFake) StopStep(
	ctx context.Context,
	req *connect.Request[enginev1.StopStepRequest],
) (*connect.Response[enginev1.StopStepResponse], error) {
	if err := f.before(ctx, "stop"); err != nil {
		return nil, err
	}
	return f.StepClient.StopStep(ctx, req)
}

// restartUnavailable is an unavailable answer that is not a dial failure: the connection was made and then cut.
func restartUnavailable() error {
	return connect.NewError(connect.CodeUnavailable, errors.New("synthetic restart"))
}

// refusedDial is the unavailable answer of a call whose connection was never made: the socket file is gone.
func refusedDial() error {
	return connect.NewError(connect.CodeUnavailable, &net.OpError{Op: "dial", Net: "unix", Err: syscall.ENOENT})
}

// TestNudgeCutMidCallIsSentOnceAndAnswered guards the rule that a nudge cut mid-call is never sent again. Every
// NudgeStep fails here, so a retry would send the nudge again and the count would show it.
func TestNudgeCutMidCallIsSentOnceAndAnswered(t *testing.T) {
	fake := &stepsFake{forwardErr: restartUnavailable()}
	client := stepsClient(t, fake)
	_, err := client.Nudge(t.Context(), connect.NewRequest(&orreryv1.StepsNudgeRequest{
		Id: stepsLabeled, Text: stepsNudgeText,
	}))
	requireConnectError(t, err, connect.CodeUnavailable, controlUnavailable)
	if len(fake.calls) != 1 || fake.calls[0].op != "nudge" {
		t.Fatalf("forwarded %+v, want one nudge", fake.calls)
	}
}

// TestNudgeNeverConnectedIsRetriedAndSucceeds checks that a nudge whose connection was never made is sent again.
func TestNudgeNeverConnectedIsRetriedAndSucceeds(t *testing.T) {
	inner := &stepsFake{}
	fake := &retryFake{StepClient: inner, failures: map[string][]error{"nudge": {refusedDial()}}}
	client := stepsClient(t, fake)
	_, err := client.Nudge(t.Context(), connect.NewRequest(&orreryv1.StepsNudgeRequest{
		Id: stepsLabeled, Text: stepsNudgeText,
	}))
	if err != nil {
		t.Fatalf("Nudge after a refused dial: %v, want success on the retry", err)
	}
	if len(inner.calls) != 1 || inner.calls[0].op != "nudge" || inner.calls[0].text != stepsNudgeText {
		t.Fatalf("forwarded %+v, want one nudge with its text", inner.calls)
	}
}

// TestHoldAndStopRetryAfterOneUnavailable checks that a hold or a stop answered unavailable once is sent again.
func TestHoldAndStopRetryAfterOneUnavailable(t *testing.T) {
	for _, op := range stepsOps() {
		if op.op == "nudge" {
			continue
		}
		t.Run(op.name, func(t *testing.T) {
			inner := &stepsFake{}
			fake := &retryFake{StepClient: inner, failures: map[string][]error{op.op: {restartUnavailable()}}}
			client := stepsClient(t, fake)
			if err := op.call(t.Context(), client, stepsLabeled); err != nil {
				t.Fatalf("%s after one unavailable: %v, want success on the retry", op.name, err)
			}
			if len(inner.calls) != 1 || inner.calls[0].op != op.op {
				t.Fatalf("forwarded %+v, want one %s", inner.calls, op.op)
			}
		})
	}
}

// TestStatusLookupRetriesAfterOneUnavailable checks that the StepStatus lookup of each forwarder is asked again.
func TestStatusLookupRetriesAfterOneUnavailable(t *testing.T) {
	for _, op := range stepsOps() {
		t.Run(op.name, func(t *testing.T) {
			inner := &stepsFake{}
			fake := &retryFake{StepClient: inner, failures: map[string][]error{"status": {restartUnavailable()}}}
			client := stepsClient(t, fake)
			if err := op.call(t.Context(), client, stepsLabeled); err != nil {
				t.Fatalf("%s after one failed lookup: %v, want success on the retry", op.name, err)
			}
			if len(inner.calls) != 1 || inner.calls[0].op != op.op {
				t.Fatalf("forwarded %+v, want one %s", inner.calls, op.op)
			}
		})
	}
}

// TestEpicsListRetriesListStepsAfterOneUnavailable checks that the ListSteps call of List is asked again.
func TestEpicsListRetriesListStepsAfterOneUnavailable(t *testing.T) {
	root := t.TempDir()
	writeHead(t, root, "repo", "epic", `{"outcome":"shipped"}`, epicsNow())
	inner := &epicsFake{steps: []*enginev1.Step{{Id: "run-a", Epic: "repo/epic"}}}
	steps := &retryFake{StepClient: inner, failures: map[string][]error{"list": {restartUnavailable()}}}
	client := epicsServe(t, root, steps)
	epics := epicsList(t, client)
	if len(epics) != 1 || len(epics[0].GetRunningSteps()) != 1 {
		t.Fatalf("epics = %v, want the epic with its one running step", epics)
	}
	if inner.listCalls != 1 {
		t.Fatalf("ListSteps calls = %d, want the one call that reached the client after the failure", inner.listCalls)
	}
}

// TestStepsRetryAttemptsEachUnderTheTimeout checks that a stalled attempt ends at the forwarder's timeout and the retry
// runs under a timeout of its own, not the time the first attempt left.
func TestStepsRetryAttemptsEachUnderTheTimeout(t *testing.T) {
	inner := &stepsFake{}
	fake := &retryFake{StepClient: inner, stalls: map[string]int{"hold": 1}}
	srv := stepsServer{steps: fake, timeout: 20 * time.Millisecond, clock: newRetryClock()}
	_, err := srv.Hold(t.Context(), connect.NewRequest(&orreryv1.StepsHoldRequest{Id: stepsLabeled}))
	if err != nil {
		t.Fatalf("Hold after one stalled attempt: %v, want success on the retry", err)
	}
	if len(inner.calls) != 1 {
		t.Fatalf("forwarded %+v, want one hold", inner.calls)
	}
}

// TestStepsRetryEndedByCallerAnswersTheLastAttempt checks that when the caller's context ends the retries, the answer
// is the last attempt's, as it is for a single call.
func TestStepsRetryEndedByCallerAnswersTheLastAttempt(t *testing.T) {
	inner := &stepsFake{}
	fake := &retryFake{StepClient: inner, stalls: map[string]int{"status": 1}}
	srv := stepsServer{steps: fake, timeout: time.Minute, clock: newRetryClock()}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err := srv.Hold(ctx, connect.NewRequest(&orreryv1.StepsHoldRequest{Id: stepsLabeled}))
	requireConnectError(t, err, connect.CodeUnavailable, controlUnavailable)
	if len(inner.calls) != 0 {
		t.Fatalf("forwarded %+v after a failed lookup, want nothing", inner.calls)
	}
}
