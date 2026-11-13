package enginehost

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepObserver records the hand-offs of a step session. Event, Return and Exited run on the pump goroutine, so it locks.
// order holds each event and each return in the order the observer got them, a return as a stepReturnMark. When release
// is set, Exited blocks until release closes, which keeps the dead step in the live set.
type stepObserver struct {
	mu      sync.Mutex
	returns int
	exits   []string
	order   []any
	release chan struct{}
}

// stepReturnMark stands in for a Return in stepObserver.order.
type stepReturnMark struct{}

func (o *stepObserver) Event(ev session.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.order = append(o.order, ev)
}

func (o *stepObserver) Return(session.TurnEnded) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.returns++
	o.order = append(o.order, stepReturnMark{})
}

func (o *stepObserver) Exited(reason string) {
	o.mu.Lock()
	o.exits = append(o.exits, reason)
	o.mu.Unlock()
	if o.release != nil {
		<-o.release
	}
}

// returned returns how many turn ends the observer has received.
func (o *stepObserver) returned() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.returns
}

// sequence returns the events and returns the observer has received, in the order it got them.
func (o *stepObserver) sequence() []any {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]any(nil), o.order...)
}

// exited returns the exit reasons the observer has received.
func (o *stepObserver) exited() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.exits...)
}

// waitReturns waits until the observer has received at least n turn ends.
func waitReturns(t *testing.T, o *stepObserver, n int) {
	t.Helper()
	waitUntil(t, fmt.Sprintf("%d turn ends handed to the observer", n), func() bool { return o.returned() >= n })
}

// newHandOffManager returns a step manager with the given step cap and a clock set by hand. Its store is at the returned
// path.
func newHandOffManager(t *testing.T, stepCap int, sink NoticeSink) (*Manager, string, *idleClock) {
	t.Helper()
	m, path := newStepManager(t, stepCap, fakeClaudePath(t), sink)
	clock := newIdleClock()
	m.now = clock.now
	return m, path, clock
}

func TestStepHandsFirstTurnToObserver(t *testing.T) {
	m, path, _ := newHandOffManager(t, 1, &fakeSink{})
	obs := &stepObserver{}
	err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer", Observer: obs,
	})
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	waitReturns(t, obs, 1)
	if n := obs.returned(); n != 1 {
		t.Fatalf("turn ends handed to the observer = %d, want 1", n)
	}
}

func TestStepHandsEventsToObserverInOrder(t *testing.T) {
	m, path, _ := newHandOffManager(t, 1, &fakeSink{})
	obs := &stepObserver{}
	err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer", Observer: obs,
	})
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	waitReturns(t, obs, 1)

	seq := obs.sequence()
	started, reply, ended, returned := -1, -1, -1, -1
	for i, item := range seq {
		switch ev := item.(type) {
		case session.Started:
			started = i
		case session.Block:
			if ev.Kind == session.BlockText && ev.Text == "fake reply 1" {
				reply = i
			}
		case session.TurnEnded:
			ended = i
		case stepReturnMark:
			returned = i
		}
	}
	if started < 0 || reply < 0 || ended < 0 || returned < 0 {
		t.Fatalf("observer order = %v, want a Started, the reply block, a TurnEnded and a Return", seq)
	}
	if started >= reply || reply >= ended || ended >= returned {
		t.Fatalf("observer order = %v, want Started, reply block, TurnEnded, then Return", seq)
	}
}

func TestStepBackgroundTurnIsNotHandedOffUntilTaskEnds(t *testing.T) {
	m, path, _ := newHandOffManager(t, 1, &fakeSink{})
	obs := &stepObserver{}
	err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: "BACKGROUND", Agent: "reviewer", Observer: obs,
	})
	if err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the background turn done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake background 1") && lastState(t, h) == string(protocol.StateDone)
	})
	if n := obs.returned(); n != 0 {
		t.Fatalf("turn ends handed to the observer during the background task = %d, want 0", n)
	}

	if err := m.Continue(t.Context(), stepA, StepPrompt{Text: "BGEND", Sender: store.PromptSenderOwner}); err != nil {
		t.Fatalf("Continue BGEND: %v", err)
	}
	waitHistory(t, path, stepA, "the reply after the background task ended", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && lastState(t, h) == string(protocol.StateDone)
	})
	waitReturns(t, obs, 1)
	if n := obs.returned(); n != 1 {
		t.Fatalf("turn ends handed to the observer after the task ended = %d, want 1", n)
	}
}

func TestStepHandOffStaysBusyThroughIdleCheck(t *testing.T) {
	sink := &fakeSink{}
	m, path, clock := newHandOffManager(t, 2, sink)
	held := &stepObserver{}
	dirA, dirB := t.TempDir(), t.TempDir()
	if err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: dirA, Prompt: "hi", Agent: "reviewer", Observer: held,
	}); err != nil {
		t.Fatalf("StartStep handed off: %v", err)
	}
	// stepB has no observer, so nothing holds it busy: it is the control that the idle check does stop a step.
	if err := m.StartStep(t.Context(), StepSession{ID: stepB, Dir: dirB, Prompt: "hi", Agent: "reviewer"}); err != nil {
		t.Fatalf("StartStep unobserved: %v", err)
	}
	waitHistory(t, path, stepA, "the handed-off step's reply", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	waitHistory(t, path, stepB, "the unobserved step's reply", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	waitReturns(t, held, 1)
	pidA := fakePID(t, dirA)
	pidB := fakePID(t, dirB)

	clock.advance(11 * time.Minute)
	m.checkIdle(t.Context())
	requireRunning(t, m, stepA, pidA)
	requireGone(t, pidB)
	if _, err := m.live(stepB); err == nil {
		t.Fatalf("unobserved step %s is still live after the idle check", stepB)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices = %+v, want none", got)
	}
}

func TestContinueSendsPromptRecordsItAndHandsOffItsTurn(t *testing.T) {
	m, path, _ := newHandOffManager(t, 1, &fakeSink{})
	obs := &stepObserver{}
	if err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer", Observer: obs,
	}); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitReturns(t, obs, 1)

	if err := m.Continue(t.Context(), stepA, StepPrompt{Text: "again", Sender: store.PromptSenderOwner}); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	history := waitHistory(t, path, stepA, "the reply to the continued prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && lastState(t, h) == string(protocol.StateDone)
	})
	want := []string{"prompt: hi", "reply: fake reply 1", "prompt: again", "reply: fake reply 2"}
	if got := conversationOf(t, history); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("conversation = %q, want %q", got, want)
	}
	waitReturns(t, obs, 2)
	if n := obs.returned(); n != 2 {
		t.Fatalf("turn ends handed to the observer = %d, want 2", n)
	}
}

func TestStepExitedCallsObserverWithFailureReason(t *testing.T) {
	sink := &fakeSink{}
	m, path, _ := newHandOffManager(t, 1, sink)
	obs := &stepObserver{}
	dir := t.TempDir()
	if err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: dir, Prompt: "hi", Agent: "reviewer", Observer: obs,
	}); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitReturns(t, obs, 1)

	if err := m.Continue(t.Context(), stepA, StepPrompt{Text: "DIE", Sender: store.PromptSenderOwner}); err != nil {
		t.Fatalf("Continue DIE: %v", err)
	}
	waitUntil(t, "the observer's exit", func() bool { return len(obs.exited()) == 1 })
	reason := obs.exited()[0]
	if !strings.Contains(reason, "exit status 3") || !strings.HasSuffix(reason, "fake died") {
		t.Fatalf("exit reason = %q, want the exit status 3 and a suffix fake died", reason)
	}
	waitHistory(t, path, stepA, "the dead step's failed state", func(h store.EngineHistory) bool {
		return h.State == "failed"
	})
	if n := obs.returned(); n != 1 {
		t.Fatalf("turn ends handed to the observer = %d, want the one before the death", n)
	}
	waitUntil(t, "the dead step to leave the live set", func() bool {
		_, err := m.live(stepA)
		return err != nil
	})
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the step died = %+v, want none", got)
	}
}

func TestContinueSendFailureRecordsFailedWithoutNotice(t *testing.T) {
	sink := &fakeSink{}
	m, path, _ := newHandOffManager(t, 1, sink)
	obs := &stepObserver{release: make(chan struct{})}
	// Registered after newHandOffManager's Close, so it runs first and frees the pump that Exited blocks.
	t.Cleanup(func() { close(obs.release) })
	if err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer", Observer: obs,
	}); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitReturns(t, obs, 1)
	if err := m.Continue(t.Context(), stepA, StepPrompt{Text: "DIE", Sender: store.PromptSenderOwner}); err != nil {
		t.Fatalf("Continue DIE: %v", err)
	}
	waitUntil(t, "the observer's exit", func() bool { return len(obs.exited()) == 1 })

	if err := m.Continue(t.Context(), stepA, StepPrompt{Text: "again", Sender: store.PromptSenderOwner}); err == nil {
		t.Fatalf("Continue to a dead step: err = nil, want the send error")
	}
	history := historyOf(t, path, stepA)
	if history.State != "failed" {
		t.Fatalf("state after the failed send = %q, want failed", history.State)
	}
	reason := ""
	for _, fields := range changedFields(t, history) {
		if fields["reason"] != "" {
			reason = fields["reason"]
		}
	}
	if !strings.Contains(reason, "send prompt") {
		t.Fatalf("failed reason = %q, want the send error", reason)
	}
	if got := conversationOf(t, history); got[len(got)-1] != "prompt: again" {
		t.Fatalf("conversation = %q, want the failed prompt recorded last", got)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices = %+v, want none", got)
	}
	if n := len(obs.exited()); n != 1 {
		t.Fatalf("exits handed to the observer = %d, want 1", n)
	}
}

func TestStepStopAndCloseCallNoExited(t *testing.T) {
	m, _, _ := newHandOffManager(t, 2, &fakeSink{})
	stopped := &stepObserver{}
	closed := &stepObserver{}
	if err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer", Observer: stopped,
	}); err != nil {
		t.Fatalf("StartStep stopped: %v", err)
	}
	if err := m.StartStep(t.Context(), StepSession{
		ID: stepB, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer", Observer: closed,
	}); err != nil {
		t.Fatalf("StartStep closed: %v", err)
	}
	waitReturns(t, stopped, 1)
	waitReturns(t, closed, 1)

	if err := m.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	m.Close()
	if got := stopped.exited(); len(got) != 0 {
		t.Fatalf("exits after Stop = %q, want none", got)
	}
	if got := closed.exited(); len(got) != 0 {
		t.Fatalf("exits after Close = %q, want none", got)
	}
}
