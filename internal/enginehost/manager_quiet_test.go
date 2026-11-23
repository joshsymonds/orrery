package enginehost

import (
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestOwnerInterruptQuietsCallerEventsUntilHisSay runs an owner's turn until an interrupt (HANG), which the owner's
// Interrupt ends. A caller event that is pending afterwards starts no turn: Deliver returns ErrCallerQuiet, the step-end
// pass sends nothing, and the event stays unmarked. The owner's next Say runs as a turn, and once it ends the pass
// delivers the pending event.
func TestOwnerInterruptQuietsCallerEventsUntilHisSay(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	s := storeOf(t, m)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the hanging reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1")
	})
	if err = m.Interrupt(t.Context(), id); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the interrupted turn's end", func(h store.EngineHistory) bool {
		return turnEndsWithError(t, h) && h.State == string(protocol.StateDone)
	})
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "the work is in")

	block := wantStepEndBlock("the work is in")
	if err = m.Deliver(t.Context(), DeliverRequest{ID: id, Text: block, PromptID: callerStepEvent}); !errors.Is(
		err, ErrCallerQuiet,
	) {
		t.Fatalf("Deliver after the owner's Interrupt error = %v, want ErrCallerQuiet", err)
	}
	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, []deliveredPrompt{{Text: "HANG"}}) {
		t.Fatalf("prompts after the owner's Interrupt = %+v, want only the start's prompt", got)
	}
	if eventMarked(t, s, id) {
		t.Fatal("step end marked while the owner's Interrupt keeps its caller quiet")
	}

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the owner's turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && turnEndCount(h) == 2 && h.State == string(protocol.StateDone)
	})
	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	history := waitHistory(t, engineOf(m), path, id, "the event's turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3") && turnEndCount(h) == 3 && h.State == string(protocol.StateDone)
	})
	want := []deliveredPrompt{
		{Text: "HANG"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
		{Text: block, PromptID: callerStepEvent, Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after its delivery")
	}
}

// TestOwnerInterruptQuietOutlivesIdleStop interrupts an owner's turn that runs until an interrupt (HANG), then lets the
// idle check stop the session. A pending caller event neither resumes the session nor is delivered: Deliver returns
// ErrCallerQuiet, the step-end pass sends nothing, the session stays stopped, and the event stays unmarked. The owner's
// Say resumes the session and runs as a turn, and once it ends the pass delivers the pending event.
func TestOwnerInterruptQuietOutlivesIdleStop(t *testing.T) {
	t.Parallel()
	m, path, clock := newIdleManager(t)
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the hanging reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1")
	})
	if err = m.Interrupt(t.Context(), id); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the interrupted turn's end", func(h store.EngineHistory) bool {
		return turnEndsWithError(t, h) && h.State == string(protocol.StateDone)
	})
	pid := fakePID(t, dir)
	clock.advance(defaultIdleAfter)
	m.checkIdle(t.Context())
	requireGone(t, pid)
	if _, err = m.live(id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("live after the idle stop: err = %v, want ErrNoSession", err)
	}
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "the work is in")

	block := wantStepEndBlock("the work is in")
	if err = m.Deliver(t.Context(), DeliverRequest{ID: id, Text: block, PromptID: callerStepEvent}); !errors.Is(
		err, ErrCallerQuiet,
	) {
		t.Fatalf("Deliver to the idle-stopped quiet session error = %v, want ErrCallerQuiet", err)
	}
	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	if _, err = m.live(id); !errors.Is(err, ErrNoSession) {
		t.Fatalf("live after the quiet deliveries: err = %v, want ErrNoSession, so nothing resumed it", err)
	}
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, []deliveredPrompt{{Text: "HANG"}}) {
		t.Fatalf("prompts after the idle stop = %+v, want only the start's prompt", got)
	}
	if eventMarked(t, s, id) {
		t.Fatal("step end marked while the owner's Interrupt keeps its idle-stopped caller quiet")
	}

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the owner's resumed turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && turnEndCount(h) == 2 && h.State == string(protocol.StateDone)
	})
	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	history := waitHistory(t, engineOf(m), path, id, "the event's turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3") && turnEndCount(h) == 3 && h.State == string(protocol.StateDone)
	})
	want := []deliveredPrompt{
		{Text: "HANG"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
		{Text: block, PromptID: callerStepEvent, Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after its delivery")
	}
}

// TestOwnerPreemptLeavesCallerEventsOpen delivers a caller event whose turn reads the next line from stdin while it runs
// (MIDTURN), and has the owner Say during it. The Say is written at once, and the event's turn takes it in with its own
// result, so the owner interrupts nothing: no interrupt is written, and the turn ends done. Once the owner's turn ends, the
// step-end pass delivers a pending event.
func TestOwnerPreemptLeavesCallerEventsOpen(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "start"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == string(protocol.StateDone)
	})
	if err = m.Deliver(t.Context(), DeliverRequest{ID: id, Text: "MIDTURN event", PromptID: "e1"}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the event's turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake midturn 2") && turnEndCount(h) == 2 && h.State == string(protocol.StateDone)
	})
	if n := interruptsWritten(t, dir); n != 0 {
		t.Fatalf("interrupts written for the owner's Say = %d, want none", n)
	}
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "the work is in")

	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	history := waitHistory(t, engineOf(m), path, id, "the pending event's turn end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3") && turnEndCount(h) == 3 && h.State == string(protocol.StateDone)
	})
	want := []deliveredPrompt{
		{Text: "start"},
		{Text: "MIDTURN event", PromptID: "e1", Sender: store.PromptSenderEvent},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
		{Text: wantStepEndBlock("the work is in"), PromptID: callerStepEvent, Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after its delivery")
	}
}
