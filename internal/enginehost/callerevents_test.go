package enginehost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// storeOf returns the store m writes through, which newTestManager opens.
func storeOf(t *testing.T, m *Manager) *store.Store {
	t.Helper()
	s, ok := m.store.(*store.Store)
	if !ok {
		t.Fatalf("manager store is %T, want *store.Store", m.store)
	}
	return s
}

// callerStepID is the id of the step the caller event tests end, and callerStepEvent is the event id of its end.
const (
	callerStepID    = "step-1"
	callerStepEvent = stepEventPrefix + callerStepID
)

// addEndedCallerStep starts the step callerStepID under caller with result, and ends it done, passed for gate green.
func addEndedCallerStep(t *testing.T, s *store.Store, caller store.EpicCaller, result string) {
	t.Helper()
	ops := []store.EngineOp{
		store.StepAdd{ID: callerStepID, Agent: "implementer", Prompt: "do it", Caller: caller},
		store.StepReturn{ID: callerStepID, Result: result},
		store.StepEnd{ID: callerStepID, End: "done", Status: "passed", Reason: "gate green"},
	}
	for _, op := range ops {
		if err := s.ApplyEngine(t.Context(), []store.EngineOp{op}, fixedClock()); err != nil {
			t.Fatalf("ApplyEngine %T of step %s: %v", op, callerStepID, err)
		}
	}
}

// wantStepEndBlock returns the block the end of step-1 sends a caller whose result is summary.
func wantStepEndBlock(summary string) string {
	return "<orrery-event kind=\"step_end\" id=\"step:step-1\">\n" +
		"Step step-1 ended: end done, status passed, reason gate green.\n" +
		"Summary: " + summary + "\n" +
		"step_status gives the full result.\n" +
		"</orrery-event>"
}

// eventMarked reports whether the end of the caller step is marked delivered to session in s.
func eventMarked(t *testing.T, s *store.Store, session string) bool {
	t.Helper()
	marked, err := s.CallerEventMarked(t.Context(), callerStepEvent, session)
	if err != nil {
		t.Fatalf("CallerEventMarked %s of %s: %v", callerStepEvent, session, err)
	}
	return marked
}

// newStepEndSender returns a CallerSender that sends headless callers through m and marks through marks. It fails the
// test if a pane caller is woken, as the headless tests have none.
func newStepEndSender(t *testing.T, m *Manager, marks CallerEventStore) *CallerSender {
	t.Helper()
	return NewCallerSender(CallerSenderConfig{
		Deliver: m.Deliver,
		WakePane: func(context.Context, string, string, string) (bool, error) {
			t.Error("a pane caller was woken in a headless test")
			return false, nil
		},
		Store: marks,
		Now:   fixedClock,
	})
}

// stepEndPass returns a StepCallerEvents over steps that sends through sender.
func stepEndPass(sender *CallerSender, steps StepCallerStore) *StepCallerEvents {
	return NewStepCallerEvents(StepCallerEventsConfig{Store: steps, Sender: sender})
}

// lostMark is a CallerEventStore whose first mark fails, as a write that fails after a delivery does.
type lostMark struct {
	*store.Store
	failed bool
}

// MarkCallerEvent fails the first mark and records the rest in the store.
func (l *lostMark) MarkCallerEvent(ctx context.Context, event, session string, at time.Time) error {
	if !l.failed {
		l.failed = true
		return errors.New("write refused")
	}
	return l.Store.MarkCallerEvent(ctx, event, session, at)
}

func TestCallerEventStepDeliveredOnceAfterRunningTurn(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	// WAITOPEN holds the turn open until fakeclaude.open exists, so the turn ends by itself and not by the owner's
	// Interrupt, which would leave the caller quiet (see TestOwnerInterruptQuietsCallerEventsUntilHisSay).
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "WAITOPEN"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The result carries ASK, so the caller's reply to the event asks a question, which it leaves open.
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "ASK which color is the banner?")

	block := wantStepEndBlock("ASK which color is the banner?")
	caller := store.CallerSession{ID: id, Kind: store.KindEngine}
	if sent, err := newStepEndSender(t, m, s).Send(t.Context(), caller, callerStepEvent, block); sent || err != nil {
		t.Fatalf("Send to a busy caller = %v, %v, want false, nil", sent, err)
	}
	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(
		got, []deliveredPrompt{{Text: "WAITOPEN"}},
	) {
		t.Fatalf("prompts while the turn runs = %+v, want only the start's prompt", got)
	}
	if eventMarked(t, s, id) {
		t.Fatalf("step end marked while its caller's turn runs")
	}

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.open"), nil, 0o600); err != nil {
		t.Fatalf("open the turn: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the turn ended and the session is done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && turnEndCount(h) == 1 && h.State == string(protocol.StateDone)
	})
	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	history := waitHistory(t, engineOf(m), path, id, "the caller's open question", func(h store.EngineHistory) bool {
		return h.State == string(protocol.StateWaiting)
	})
	want := []deliveredPrompt{
		{Text: "WAITOPEN"},
		{
			Text:     block,
			PromptID: callerStepEvent,
			Sender:   store.PromptSenderEvent,
		},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	if question, ok := lastQuestion(t, history); !ok || question != "Which color?" {
		t.Fatalf("question = %q (set %v), want the caller's question left open", question, ok)
	}
	if requestIDOf(t, history) == "" {
		t.Fatalf("no request of the caller is open after the delivery")
	}
	if !eventMarked(t, s, id) {
		t.Fatalf("step end not marked after its delivery")
	}

	// A new pass over the same store lists the step no more, so it sends nothing.
	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after a new pass = %+v, want %+v", got, want)
	}
}

func TestCallerEventStepLostMarkNotDeliveredAgain(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == string(protocol.StateDone)
	})
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "all green")

	stepEndPass(newStepEndSender(t, m, &lostMark{Store: s}), s).Pass(t.Context())
	waitHistory(t, engineOf(m), path, id, "the reply to the step end done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && h.State == string(protocol.StateDone)
	})
	if eventMarked(t, s, id) {
		t.Fatalf("step end marked although its mark was refused")
	}

	// The step is still listed, and its prompt id is already stored, so the send repeats nothing and marks the event.
	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	want := []deliveredPrompt{
		{Text: "hi"},
		{Text: wantStepEndBlock("all green"), PromptID: callerStepEvent, Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	if !eventMarked(t, s, id) {
		t.Fatalf("step end not marked by the pass that found its prompt stored")
	}
}

func TestCallerEventStepReadStepSendsNothing(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == string(protocol.StateDone)
	})
	caller := store.EpicCaller{Session: id}
	addEndedCallerStep(t, s, caller, "all green")
	if marked, err := s.MarkStepRead(t.Context(), callerStepID, caller); err != nil || !marked {
		t.Fatalf("MarkStepRead = %v, %v, want the caller to read its step", marked, err)
	}

	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, []deliveredPrompt{{Text: "hi"}}) {
		t.Fatalf("prompts = %+v, want only the start's prompt", got)
	}
}

func TestCallerEventStepStepCallerReceivesNothing(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	stepCaller := store.EngineAdd{ID: "step-caller", Dir: t.TempDir(), State: "working", Kind: store.KindStep}
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{stepCaller}, fixedClock()); err != nil {
		t.Fatalf("ApplyEngine add step caller: %v", err)
	}
	addEndedCallerStep(t, s, store.EpicCaller{Session: "step-caller"}, "all green")

	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	// A step caller is marked without a delivery, as a delivery to it would fail with no resumable session.
	if !eventMarked(t, s, "step-caller") {
		t.Fatalf("step end of a step caller not marked")
	}
}

func TestCallerEventStepIdleStoppedCallerResumed(t *testing.T) {
	t.Parallel()
	m, path, clock := newIdleManager(t, 1)
	s := storeOf(t, m)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && lastState(t, h) == string(protocol.StateDone)
	})
	clock.advance(defaultIdleAfter)
	m.checkIdle(t.Context())
	if _, err := m.live(id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("live after idle stop: err = %v, want ErrNoSession", err)
	}
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "all green")

	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	history := waitHistory(t, engineOf(m), path, id, "the reply to the step end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	want := []deliveredPrompt{
		{Text: "hi"},
		{Text: wantStepEndBlock("all green"), PromptID: callerStepEvent, Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

func TestCallerEventStepOwnerStoppedCallerNotRestarted(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == string(protocol.StateDone)
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "all green")

	caller := store.CallerSession{ID: id, Kind: store.KindEngine}
	block := wantStepEndBlock("all green")
	if sent, err := newStepEndSender(t, m, s).Send(t.Context(), caller, callerStepEvent, block); sent || err != nil {
		t.Fatalf("Send to an owner-stopped caller = %v, %v, want false, nil", sent, err)
	}
	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	if args := fakeArgs(t, dir); len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want the start only: the stopped caller is not restarted", args)
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, []deliveredPrompt{{Text: "hi"}}) {
		t.Fatalf("prompts = %+v, want only the start's prompt", got)
	}
	if eventMarked(t, s, id) {
		t.Fatalf("step end marked for a stopped caller that received nothing")
	}
}

func TestPaneWakeStepHeldUntilKeysSentThenMarked(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	const server, pane = "-S /tmp/tmux-1000/orrery", "%7"
	if err := s.SyncPane(t.Context(), server, store.Pane{ID: "orrery-1", Pane: pane}, fixedClock()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	addEndedCallerStep(t, s, store.EpicCaller{Server: server, Pane: pane}, "all green")

	idle := false
	var woken []string
	sender := NewCallerSender(CallerSenderConfig{
		Deliver: func(context.Context, DeliverRequest) error {
			t.Error("a pane caller received a headless delivery")
			return nil
		},
		WakePane: func(_ context.Context, gotServer, gotPane, text string) (bool, error) {
			if gotServer != server || gotPane != pane {
				t.Errorf("WakePane to %q %q, want %q %q", gotServer, gotPane, server, pane)
			}
			woken = append(woken, text)
			return idle, nil
		},
		Store: s,
		Now:   fixedClock,
	})
	pass := NewStepCallerEvents(StepCallerEventsConfig{Store: s, Sender: sender})

	pass.Pass(t.Context())
	if len(woken) != 1 {
		t.Fatalf("wakes while the pane is not idle = %d, want 1", len(woken))
	}
	if eventMarked(t, s, "orrery-1") {
		t.Fatalf("step end marked while its pane was not idle")
	}

	idle = true
	pass.Pass(t.Context())
	if len(woken) != 2 {
		t.Fatalf("wakes after the pane is idle = %d, want 2", len(woken))
	}
	if woken[1] != wantStepEndBlock("all green") {
		t.Fatalf("woken text = %q, want the step end block", woken[1])
	}
	if !eventMarked(t, s, "orrery-1") {
		t.Fatalf("step end not marked after its keys were sent")
	}

	pass.Pass(t.Context())
	if len(woken) != 2 {
		t.Fatalf("wakes after the mark = %d, want still 2", len(woken))
	}
}
