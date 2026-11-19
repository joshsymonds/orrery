package server_test

import (
	"path/filepath"
	"testing"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// interruptMarkState is the state of eventEngine as addEngine writes it, in state, with its interrupting mark as given.
// A set mark carries eventAt as its interrupting_since, the time applyEngineOps stamps the interrupt at.
func interruptMarkState(state string, interrupting bool) *orreryv1.ConversationState {
	marked := &orreryv1.ConversationState{Session: engineSession(state), Interrupting: interrupting}
	if interrupting {
		marked.InterruptingSince = eventAt
	}
	return marked
}

// interruptMarkSetState is the write that sets eventEngine's state to state.
func interruptMarkSetState(state string) store.EngineSet {
	return store.EngineSet{ID: eventEngine, Changes: store.EngineChanges{State: optional(state)}}
}

// interruptMarkTurnEnd is the turn end of eventEngine that the owner's interrupt ended when owner is set, and any other
// turn end when it is not.
func interruptMarkTurnEnd(owner bool) store.EngineTurnEnded {
	return store.EngineTurnEnded{
		ID: eventEngine, Subtype: "success", Result: "ok", Origin: "user", OwnerInterrupted: owner,
	}
}

// TestSessionWatchInterruptMarkServesWhileTurnRuns records an owner interrupt that found a turn running, on a session
// that is working. The first delta carries the mark, and the state stays working.
func TestSessionWatchInterruptMarkServesWhileTurnRuns(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		interruptMarkSetState("working"),
		store.EngineInterrupt{ID: eventEngine, Turning: true},
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		Reset_:     true,
		State:      interruptMarkState("working", true),
	})
}

// TestSessionWatchInterruptMarkClearsAtTurnEnd records an interrupt that found a turn running, then the turn's end. The
// delta that follows clears the mark and leaves the state working.
func TestSessionWatchInterruptMarkClearsAtTurnEnd(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		interruptMarkSetState("working"),
		store.EngineInterrupt{ID: eventEngine, Turning: true},
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	applyEngineOps(t, s, interruptMarkTurnEnd(false))
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        4,
		State:      interruptMarkState("working", false),
	})
}

// TestSessionWatchInterruptMarkClearsWhenStateLeavesWorking records an interrupt that found a turn running, then the
// session's move to idle with no turn end. The delta that follows clears the mark.
func TestSessionWatchInterruptMarkClearsWhenStateLeavesWorking(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		interruptMarkSetState("working"),
		store.EngineInterrupt{ID: eventEngine, Turning: true},
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	applyEngineOps(t, s, interruptMarkSetState("idle"))
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        4,
		State:      interruptMarkState("idle", false),
	})
}

// TestSessionWatchInterruptMarkTurningFalseMarksNothing records an interrupt that found no turn running, as a failed
// interrupt writes it, on a working session. The first delta carries no mark.
func TestSessionWatchInterruptMarkTurningFalseMarksNothing(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		interruptMarkSetState("working"),
		store.EngineInterrupt{ID: eventEngine, Turning: false},
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		Reset_:     true,
		State:      interruptMarkState("working", false),
	})
}

// TestSessionWatchInterruptMarkOwnerTurnEndServesInterruptedItem stores a turn end that the owner's interrupt ended. The
// first delta carries one interrupted item, stamped with the event's time.
func TestSessionWatchInterruptMarkOwnerTurnEndServesInterruptedItem(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, interruptMarkTurnEnd(true))
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        2,
		Reset_:     true,
		Oldest:     2,
		Items:      []*orreryv1.ConversationItem{{Kind: "interrupted", Time: eventAt}},
		State:      &orreryv1.ConversationState{Session: engineSession("idle")},
	})
}

// TestSessionWatchInterruptMarkOtherTurnEndServesNoItem stores a turn end that no owner interrupt ended, one that
// failed with an error. The first delta carries no item.
func TestSessionWatchInterruptMarkOtherTurnEndServesNoItem(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, store.EngineTurnEnded{
		ID: eventEngine, Subtype: "error_max_turns", IsError: true, TerminalReason: "max_turns", Origin: "user",
	})
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        2,
		Reset_:     true,
		State:      &orreryv1.ConversationState{Session: engineSession("idle")},
	})
}

// TestSessionWatchInterruptMarkStaysClearedAfterLeavingWorking records an interrupt that found a turn running, then the
// session's move to stopped with no turn end, then its return to working. The first delta carries no mark.
func TestSessionWatchInterruptMarkStaysClearedAfterLeavingWorking(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		interruptMarkSetState("working"),
		store.EngineInterrupt{ID: eventEngine, Turning: true},
		interruptMarkSetState("stopped"),
		interruptMarkSetState("working"),
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        5,
		Reset_:     true,
		State:      interruptMarkState("working", false),
	})
}

// TestSessionWatchInterruptMarkReturnsOnNewInterrupt records the interrupt, the move to stopped and the return to
// working of TestSessionWatchInterruptMarkStaysClearedAfterLeavingWorking, then a new owner interrupt that found a turn
// running. The first delta carries the mark again, and the state stays working.
func TestSessionWatchInterruptMarkReturnsOnNewInterrupt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s,
		interruptMarkSetState("working"),
		store.EngineInterrupt{ID: eventEngine, Turning: true},
		interruptMarkSetState("stopped"),
		interruptMarkSetState("working"),
		store.EngineInterrupt{ID: eventEngine, Turning: true},
	)
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        6,
		Reset_:     true,
		State:      interruptMarkState("working", true),
	})
}
