package server_test

import (
	"path/filepath"
	"testing"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

const (
	// interruptingSinceAt is the unix second the owner's interrupt is stored at, distinct from eventAt.
	interruptingSinceAt = eventAt + 30
	// interruptingLaterAt is the unix second of the events that follow the interrupt, after interruptingSinceAt.
	interruptingLaterAt = eventAt + 60
)

// TestSessionWatchInterruptingSinceServesInterruptTime records an owner interrupt at interruptingSinceAt that found a
// turn running, on a session that is working. The first delta carries the mark with the interrupt's time.
func TestSessionWatchInterruptingSinceServesInterruptTime(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, interruptMarkSetState("working"))
	applyEngineOpsAt(t, s, interruptingSinceAt, store.EngineInterrupt{ID: eventEngine, Turning: true})
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		Reset_:     true,
		State: &orreryv1.ConversationState{
			Session: engineSession("working"), Interrupting: true, InterruptingSince: interruptingSinceAt,
			TurnSince: eventAt,
		},
	})
}

// TestSessionWatchInterruptingSinceClearsAtTurnEnd records an interrupt at interruptingSinceAt that found a turn
// running, then the turn's end at interruptingLaterAt. The delta that follows clears the mark and its time.
func TestSessionWatchInterruptingSinceClearsAtTurnEnd(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, interruptMarkSetState("working"))
	applyEngineOpsAt(t, s, interruptingSinceAt, store.EngineInterrupt{ID: eventEngine, Turning: true})
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	applyEngineOpsAt(t, s, interruptingLaterAt, interruptMarkTurnEnd(false))
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        4,
		State:      &orreryv1.ConversationState{Session: engineSession("working"), TurnSince: eventAt},
	})
}

// TestSessionWatchInterruptingSinceClearsWhenStateLeavesWorking records an interrupt at interruptingSinceAt that found
// a turn running, then the session's move to idle at interruptingLaterAt with no turn end. The delta that follows
// clears the mark and its time.
func TestSessionWatchInterruptingSinceClearsWhenStateLeavesWorking(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, interruptMarkSetState("working"))
	applyEngineOpsAt(t, s, interruptingSinceAt, store.EngineInterrupt{ID: eventEngine, Turning: true})
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	stream.next(t)

	applyEngineOpsAt(t, s, interruptingLaterAt, interruptMarkSetState("idle"))
	idle := engineSession("idle")
	idle.Changed = interruptingLaterAt
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        4,
		State:      &orreryv1.ConversationState{Session: idle},
	})
}

// TestSessionWatchInterruptingSinceZeroWhenNeverInterrupted records a working session that no owner interrupt has
// reached. The first delta carries no mark and an interrupting_since of 0.
func TestSessionWatchInterruptingSinceZeroWhenNeverInterrupted(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOps(t, s, interruptMarkSetState("working"))
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, 0))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        2,
		Reset_:     true,
		State:      &orreryv1.ConversationState{Session: engineSession("working"), TurnSince: eventAt},
	})
}
