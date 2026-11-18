package enginehost

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// interruptMarks returns the turning flag of each engine_interrupt in history, in seq order.
func interruptMarks(t *testing.T, history store.EngineHistory) []bool {
	t.Helper()
	var marks []bool
	for _, event := range history.Events {
		if event.Kind != store.EventEngineInterrupt {
			continue
		}
		var payload struct {
			Turning bool `json:"turning"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode interrupt payload %q: %v", event.Payload, err)
		}
		marks = append(marks, payload.Turning)
	}
	return marks
}

// turnEndOwnerFlags returns the ownerInterrupted flag of each engine_turn_ended in history, in seq order.
func turnEndOwnerFlags(t *testing.T, history store.EngineHistory) []bool {
	t.Helper()
	var flags []bool
	for _, event := range history.Events {
		if event.Kind != store.EventEngineTurnEnded {
			continue
		}
		var payload struct {
			OwnerInterrupted bool `json:"ownerInterrupted"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode turn ended payload %q: %v", event.Payload, err)
		}
		flags = append(flags, payload.OwnerInterrupted)
	}
	return flags
}

// isQuiet reports whether the owner's interrupt has left session id quiet.
func isQuiet(m *Manager, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, quiet := m.quiet[id]
	return quiet
}

// TestInterruptMarkOwnerInterruptOfRunningTurn interrupts a turn that runs until an interrupt (HANG). The owner's
// interrupt is recorded as turning while the session is still working, and the turn's end that it causes is recorded
// as ownerInterrupted.
func TestInterruptMarkOwnerInterruptOfRunningTurn(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the hanging reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1")
	})

	var pending store.EngineHistory
	m.interruptWritten = func(string) { pending = historyOf(t, path, id) }
	if err = m.OwnerInterrupt(t.Context(), id); err != nil {
		t.Fatalf("OwnerInterrupt: %v", err)
	}
	if got := interruptMarks(t, pending); !slices.Equal(got, []bool{true}) {
		t.Fatalf("engine_interrupt turning before the interrupt is sent = %v, want [true]", got)
	}
	if pending.State != string(protocol.StateWorking) {
		t.Fatalf("session state while the interrupt is pending = %q, want %q", pending.State, protocol.StateWorking)
	}

	history := waitHistory(t, engineOf(m), path, id, "the interrupted turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == string(protocol.StateDone)
	})
	if got := interruptMarks(t, history); !slices.Equal(got, []bool{true}) {
		t.Fatalf("engine_interrupt turning = %v, want [true]", got)
	}
	if got := turnEndOwnerFlags(t, history); !slices.Equal(got, []bool{true}) {
		t.Fatalf("turn ends ownerInterrupted = %v, want [true]", got)
	}
}

// TestInterruptMarkOwnerInterruptWithNoTurnRunning interrupts a session whose turn has ended. The interrupt is still
// recorded, as not turning, and no turn's end is marked.
func TestInterruptMarkOwnerInterruptWithNoTurnRunning(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "start"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1 && h.State == string(protocol.StateDone)
	})

	if err = m.OwnerInterrupt(t.Context(), id); err != nil {
		t.Fatalf("OwnerInterrupt: %v", err)
	}
	history := historyOf(t, path, id)
	if got := interruptMarks(t, history); !slices.Equal(got, []bool{false}) {
		t.Fatalf("engine_interrupt turning = %v, want [false]", got)
	}
	if got := turnEndOwnerFlags(t, history); !slices.Equal(got, []bool{false}) {
		t.Fatalf("turn ends ownerInterrupted = %v, want [false]", got)
	}
}

// TestInterruptMarkPreemptingSayStoresNone delivers a caller event whose turn reads the next line from stdin while it runs
// (MIDTURN). The owner's Say during it is written at once and taken in by that turn, so it interrupts nothing: no interrupt
// is written, no engine_interrupt is recorded, and no turn's end is marked.
func TestInterruptMarkPreemptingSayStoresNone(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
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
	history := waitHistory(t, engineOf(m), path, id, "the event's turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake midturn 2") && turnEndCount(h) == 2 && h.State == string(protocol.StateDone)
	})
	if n := interruptsWritten(t, dir); n != 0 {
		t.Fatalf("interrupts written for the owner's Say = %d, want none", n)
	}
	if got := interruptMarks(t, history); len(got) != 0 {
		t.Fatalf("engine_interrupt records = %v, want none for the owner's Say", got)
	}
	if got := turnEndOwnerFlags(t, history); !slices.Equal(got, []bool{false, false}) {
		t.Fatalf("turn ends ownerInterrupted = %v, want all false", got)
	}
}

// TestInterruptMarkStepLoopInterruptStoresNone interrupts a turn that runs until an interrupt (HANG) through Interrupt,
// as the step loop does. The interrupt records no engine_interrupt, and the turn's end is not marked.
func TestInterruptMarkStepLoopInterruptStoresNone(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
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
	history := waitHistory(t, engineOf(m), path, id, "the interrupted turn's end", func(h store.EngineHistory) bool {
		return turnEndsWithError(t, h) && h.State == string(protocol.StateDone)
	})
	if got := interruptMarks(t, history); len(got) != 0 {
		t.Fatalf("engine_interrupt records = %v, want none for the step loop's interrupt", got)
	}
	if got := turnEndOwnerFlags(t, history); !slices.Equal(got, []bool{false}) {
		t.Fatalf("turn ends ownerInterrupted = %v, want [false]", got)
	}
}

// TestInterruptMarkFailedInterruptStoresNotTurning interrupts a turn that runs until an interrupt (HANG), and stops the
// session before the interrupt is sent, so the send fails. The failed interrupt is recorded as not turning after the
// owner's record, and the session stays quiet.
func TestInterruptMarkFailedInterruptStoresNotTurning(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the hanging reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1")
	})
	live, err := m.live(id)
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	m.interruptWritten = func(string) {
		// Stopping the session closes its stdin, so the interrupt cannot be written. The stop's own error is only logged.
		if err := live.sess.Stop(); err != nil {
			t.Logf("stop the session: %v", err)
		}
	}

	if err = m.OwnerInterrupt(t.Context(), id); err == nil {
		t.Fatal("OwnerInterrupt of a stopped session returned nil, want the send's error")
	}
	history := historyOf(t, path, id)
	if got := interruptMarks(t, history); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("engine_interrupt turning = %v, want [true false]", got)
	}
	if !isQuiet(m, id) {
		t.Fatal("session not quiet after a failed owner interrupt, want it quiet")
	}
}
