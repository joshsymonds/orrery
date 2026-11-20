package server_test

import (
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

const (
	// turnBehindStartAt is the unix second the first turn of the tests starts at, distinct from eventAt.
	turnBehindStartAt = eventAt + 10
	// turnBehindMidAt is the unix second of the events that fall inside the first turn, after turnBehindStartAt.
	turnBehindMidAt = eventAt + 20
	// turnBehindLaterAt is the unix second of the events that follow turnBehindMidAt.
	turnBehindLaterAt = eventAt + 30
)

// wantTurnSince opens a stream on eventEngine over the store at path and fails the test unless the first delta carries
// a state whose turn_since is want.
func wantTurnSince(t *testing.T, path string, want int64) {
	t.Helper()
	client := sessionsWatchClient(t, sessionsHandler(t, store.NewReader(path), nil, nil, 0))
	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	state := stream.next(t).GetState()
	if state == nil {
		t.Fatalf("first delta carries no state, want turn_since %d", want)
	}
	if got := state.GetTurnSince(); got != want {
		t.Fatalf("turn_since %d, want %d", got, want)
	}
}

// TestSessionWatchTurnBehindServesIdleToWorkingStart records a session that goes from idle to working at
// turnBehindStartAt. The first delta carries turn_since at that time.
func TestSessionWatchTurnBehindServesIdleToWorkingStart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOpsAt(t, s, turnBehindStartAt, interruptMarkSetState("working"))
	wantTurnSince(t, path, turnBehindStartAt)
}

// TestSessionWatchTurnBehindServesEngineAddedWorkingStart records a session added as working at turnBehindStartAt, with
// no state change before it. The first delta carries turn_since at the add's time.
func TestSessionWatchTurnBehindServesEngineAddedWorkingStart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	applyEngineOpsAt(t, s, turnBehindStartAt, store.EngineAdd{ID: eventEngine, Dir: "/a", State: "working"})
	wantTurnSince(t, path, turnBehindStartAt)
}

// TestSessionWatchTurnBehindRestartsAfterIdle records a turn that starts at turnBehindStartAt, a move to idle at
// turnBehindMidAt, and a new turn that starts at turnBehindLaterAt. The first delta carries the new turn's start.
func TestSessionWatchTurnBehindRestartsAfterIdle(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOpsAt(t, s, turnBehindStartAt, interruptMarkSetState("working"))
	applyEngineOpsAt(t, s, turnBehindMidAt, interruptMarkSetState("idle"))
	applyEngineOpsAt(t, s, turnBehindLaterAt, interruptMarkSetState("working"))
	wantTurnSince(t, path, turnBehindLaterAt)
}

// TestSessionWatchTurnBehindServesFollowedTurnEndTime records a turn that starts at turnBehindStartAt, a turn end at
// turnBehindMidAt, and the working change that follows the turn end at turnBehindLaterAt. The first delta carries the
// turn end's time, not the change's.
func TestSessionWatchTurnBehindServesFollowedTurnEndTime(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOpsAt(t, s, turnBehindStartAt, interruptMarkSetState("working"))
	applyEngineOpsAt(t, s, turnBehindMidAt, interruptMarkTurnEnd(false))
	applyEngineOpsAt(t, s, turnBehindLaterAt, interruptMarkSetState("working"))
	wantTurnSince(t, path, turnBehindMidAt)
}

// TestSessionWatchTurnBehindUnchangedAfterMidTurnSay records a turn that starts at turnBehindStartAt, then a Say at
// turnBehindMidAt: an engine_prompt with the state set to working again. The first delta still carries the turn's start.
func TestSessionWatchTurnBehindUnchangedAfterMidTurnSay(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOpsAt(t, s, turnBehindStartAt, interruptMarkSetState("working"))
	applyEngineOpsAt(t, s, turnBehindMidAt,
		store.EnginePrompt{ID: eventEngine, Text: "hi"},
		interruptMarkSetState("working"),
	)
	wantTurnSince(t, path, turnBehindStartAt)
}

// TestSessionWatchTurnBehindUnchangedAfterAnsweredRequest records a turn that starts at turnBehindStartAt, a request
// that puts the session to waiting at turnBehindMidAt, and its answer, which sets the session working again at
// turnBehindLaterAt. The first delta still carries the turn's start.
func TestSessionWatchTurnBehindUnchangedAfterAnsweredRequest(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOpsAt(t, s, turnBehindStartAt, interruptMarkSetState("working"))
	applyEngineOpsAt(t, s, turnBehindMidAt, interruptMarkSetState("waiting"))
	applyEngineOpsAt(t, s, turnBehindLaterAt, interruptMarkSetState("working"))
	wantTurnSince(t, path, turnBehindStartAt)
}

// TestSessionWatchTurnBehindZeroWhileWaiting records a turn that starts at turnBehindStartAt, then a move to waiting at
// turnBehindMidAt. The first delta carries turn_since 0, since the session is not working.
func TestSessionWatchTurnBehindZeroWhileWaiting(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOpsAt(t, s, turnBehindStartAt, interruptMarkSetState("working"))
	applyEngineOpsAt(t, s, turnBehindMidAt, interruptMarkSetState("waiting"))
	wantTurnSince(t, path, 0)
}

// TestSessionWatchTurnBehindZeroWhenIdle records a turn that starts at turnBehindStartAt, then a move to idle at
// turnBehindMidAt. The first delta carries turn_since 0, since the session is not working.
func TestSessionWatchTurnBehindZeroWhenIdle(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	applyEngineOpsAt(t, s, turnBehindStartAt, interruptMarkSetState("working"))
	applyEngineOpsAt(t, s, turnBehindMidAt, interruptMarkSetState("idle"))
	wantTurnSince(t, path, 0)
}
