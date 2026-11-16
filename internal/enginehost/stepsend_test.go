package enginehost

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestStepTurnThatEndsWhileItsOwnSendIsUnderWayIsHandedToTheObserver holds a step's Continue open after its prompt is
// written, until the turn that prompt opened has stored its end, as a fast turn can end before its send returns. That
// turn answers the prompt under way, so no later turn follows it: its end is the one the step waits on, and the observer
// gets it.
func TestStepTurnThatEndsWhileItsOwnSendIsUnderWayIsHandedToTheObserver(t *testing.T) {
	t.Parallel()
	m, path, _ := newHandOffManager(t, 1, &fakeSink{})
	obs := &stepObserver{}
	if err := m.StartStep(t.Context(), StepSession{
		ID: stepA, Dir: t.TempDir(), Prompt: "hi", Agent: "reviewer", Observer: obs,
	}); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitReturns(t, engineOf(m), obs, 1)

	m.promptWritten = func(string) {
		waitHistory(t, engineOf(m), path, stepA, "the continued turn's end, with its send under way",
			func(h store.EngineHistory) bool { return turnEndCount(h) == 2 })
	}
	if err := m.Continue(t.Context(), stepA, StepPrompt{Text: "again"}); err != nil {
		t.Fatalf("Continue: %v", err)
	}

	// The hand-off is decided, under the lock, before the turn end's line is stored, so it is settled by now: a turn
	// end that was not handed off never is, and waiting for it would wait for good.
	if !handedOff(m, stepA) {
		t.Fatal("the continued turn's end was not handed to the observer: its own send counted as a turn after it")
	}
	waitReturns(t, engineOf(m), obs, 2)
}

// handedOff reports whether the live step id's last turn end was handed to its observer, with no turn running since.
func handedOff(m *Manager, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	live := m.sessions[id]
	return live != nil && live.returned && !live.turning
}

// TestSayTurnThatEndsWhileItsOwnSendIsUnderWayEndsDone holds a Say open after its prompt is written, until the turn that
// prompt opened has stored its end. That turn answers the prompt under way, so the session is done in the same write as
// the turn's end, and the owner gets the turn's done notice.
func TestSayTurnThatEndsWhileItsOwnSendIsUnderWayEndsDone(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitNotices(t, engineOf(m), sink)

	var atEnd store.EngineHistory
	m.promptWritten = func(string) {
		atEnd = waitHistory(t, engineOf(m), path, id, "the said turn's end, with its send under way",
			func(h store.EngineHistory) bool { return turnEndCount(h) == 2 })
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if atEnd.State != "done" {
		t.Fatalf("state stored with the said turn's end = %q, want done: its own send counted as a turn after it",
			atEnd.State)
	}
	waitUntil(t, engineOf(m), "the said turn's done notice", func() bool { return len(sink.got()) == 2 })
	want := Notice{Session: id, Dir: dir, State: "done", Body: "fake reply 2"}
	if got := sink.got(); got[1] != want {
		t.Fatalf("second notice = %+v, want %+v", got[1], want)
	}
}
