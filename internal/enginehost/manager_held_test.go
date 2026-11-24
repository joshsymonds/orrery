package enginehost

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// heldRow reports whether the store row of session id is held, as m's store reads it.
func heldRow(t *testing.T, m *Manager, id string) bool {
	t.Helper()
	row, err := m.store.EngineSession(t.Context(), id)
	if err != nil {
		t.Fatalf("EngineSession %s: %v", id, err)
	}
	return row.Held
}

// waitHeld waits until session id's first turn has ended with its row working and held.
func waitHeld(t *testing.T, m *Manager, path, id string) {
	t.Helper()
	history := waitHistory(t, engineOf(m), path, id, "the held turn end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1
	})
	if history.State != "working" {
		t.Fatalf("state after a turn that ended under a background agent = %q, want working", history.State)
	}
	if !heldRow(t, m, id) {
		t.Fatal("row after a turn that ended under a background agent is not held")
	}
}

// releaseAgent creates fakeclaude.release in dir, which lets the fake's background agent end.
func releaseAgent(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.release"), nil, 0o600); err != nil {
		t.Fatalf("end the background agent: %v", err)
	}
}

// TestHeldTurnEndNotifiesOnlyAtTheTurnTheAgentsEndStarts runs BGAGENTSELF: its turn ends while a background agent runs,
// which leaves the session working and held with no notice, and the turn claude starts once the agent ends ends the
// session done with the one done notice, whose body is that turn's reply.
func TestHeldTurnEndNotifiesOnlyAtTheTurnTheAgentsEndStarts(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BGAGENTSELF"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHeld(t, m, path, id)

	releaseAgent(t, dir)
	history := waitHistory(t, engineOf(m), path, id, "the agent's turn done", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 2 && h.State == "done"
	})
	if !hasBlock(t, history, "fake agent done 1") {
		t.Fatal("history lacks the reply of the turn the agent's end started")
	}
	want := []Notice{{Session: id, Dir: dir, State: "done", Body: "fake agent done 1"}}
	if got := waitNotices(t, engineOf(m), sink); !reflect.DeepEqual(got, want) {
		t.Fatalf("notices = %+v, want only %+v", got, want)
	}
	if heldRow(t, m, id) {
		t.Fatal("row is still held after the turn the agent's end started")
	}
}

// TestOwnerInterruptOfHeldSessionRecordsDone runs BGAGENT, whose turn ends while a background agent runs, then
// interrupts the held session. The interrupt stops the agent and claude starts no turn after it, so the interrupt itself
// records the session done, tells the owner the session changed, and sends no notice.
func TestOwnerInterruptOfHeldSessionRecordsDone(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BGAGENT"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHeld(t, m, path, id)

	if _, err = m.OwnerInterrupt(t.Context(), id); err != nil {
		t.Fatalf("OwnerInterrupt: %v", err)
	}
	if got := historyOf(t, path, id).State; got != "done" {
		t.Fatalf("state after the owner's interrupt of a held session = %q, want done", got)
	}
	if heldRow(t, m, id) {
		t.Fatal("row is still held after the owner's interrupt")
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices = %+v, want none", got)
	}
	if !slices.Contains(sink.changedCalls(), id) {
		t.Fatalf("changed calls = %q, want one for %s", sink.changedCalls(), id)
	}
}

// TestIdleStopOfHeldSessionWhoseAgentEndedRecordsDone runs BGAGENTGONE: its turn ends while a background agent runs,
// and the agent then ends with no turn after it. Nothing will end the hold, so the idle stop records the session done
// and sends the done notice its last turn did not.
func TestIdleStopOfHeldSessionWhoseAgentEndedRecordsDone(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, fakeClaudePath(t), sink)
	clock := newIdleClock()
	m.now = clock.now
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BGAGENTGONE"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHeld(t, m, path, id)

	releaseAgent(t, dir)
	waitUntil(t, engineOf(m), "the agent's end", func() bool {
		row, err := m.store.EngineSession(t.Context(), id)
		return err == nil && !row.Background
	})
	clock.advance(defaultIdleAfter)
	m.checkIdle(t.Context())

	if got := historyOf(t, path, id).State; got != "done" {
		t.Fatalf("state after the idle stop of a held session = %q, want done", got)
	}
	want := []Notice{{Session: id, Dir: dir, State: "done"}}
	if got := sink.got(); !reflect.DeepEqual(got, want) {
		t.Fatalf("notices = %+v, want %+v", got, want)
	}
}

// TestCloseOfHeldSessionRecordsInterrupted runs BGAGENT and closes the manager while the session is held: the close
// ends the background agent with the process, so the session is recorded interrupted, as a running turn is.
func TestCloseOfHeldSessionRecordsInterrupted(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BGAGENT"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHeld(t, m, path, id)

	m.Close()

	if got := historyOf(t, path, id).State; got != "interrupted" {
		t.Fatalf("state after Close of a held session = %q, want interrupted", got)
	}
	want := []Notice{{Session: id, Dir: dir, State: "interrupted", Body: interruptedReason}}
	if got := sink.got(); !reflect.DeepEqual(got, want) {
		t.Fatalf("notices = %+v, want %+v", got, want)
	}
}

// TestHeldStateClearsOnTheNextStateWrite checks the store's rule: a state write that carries Held sets the row's held
// flag, and any later state write without it clears it.
func TestHeldStateClearsOnTheNextStateWrite(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	working, done := "working", "done"
	apply := func(ops ...store.EngineOp) {
		t.Helper()
		if err := s.ApplyEngine(t.Context(), ops, fixedClock()); err != nil {
			t.Fatalf("ApplyEngine: %v", err)
		}
	}
	apply(store.EngineAdd{ID: chosenID, Dir: t.TempDir(), State: working})
	apply(store.EngineSet{ID: chosenID, Changes: store.EngineChanges{State: &working, Held: new(true)}})
	if row, err := s.EngineSession(t.Context(), chosenID); err != nil || !row.Held {
		t.Fatalf("row after a held write = %+v, %v; want held", row, err)
	}
	apply(store.EngineSet{ID: chosenID, Changes: store.EngineChanges{Seen: new("1700000000")}})
	if row, err := s.EngineSession(t.Context(), chosenID); err != nil || !row.Held {
		t.Fatalf("row after a write with no state = %+v, %v; want still held", row, err)
	}
	apply(store.EngineSet{ID: chosenID, Changes: store.EngineChanges{State: &done}})
	if row, err := s.EngineSession(t.Context(), chosenID); err != nil || row.Held || row.State != done {
		t.Fatalf("row after a done write = %+v, %v; want done and not held", row, err)
	}
}

// TestReadoptOfHeldSessionRunsNoTurnAndEndsAtTheAgentsTurn runs BGAGENTSELF, restarts the manager while the session is
// held, and checks that the re-adopted session is held with no turn running, then that the turn the agent's end starts
// records it done with its notice.
func TestReadoptOfHeldSessionRunsNoTurnAndEndsAtTheAgentsTurn(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BGAGENTSELF"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHeld(t, rig.manager, rig.path, id)

	rig.crash.refuse()
	restarted, sink := restartAfterCrash(t, rig)

	live, err := restarted.live(id)
	if err != nil {
		t.Fatalf("session %s is not live after the restart: %v", id, err)
	}
	restarted.mu.Lock()
	turning, held := live.turning, live.held
	restarted.mu.Unlock()
	if turning || !held {
		t.Fatalf("re-adopted held session turning %t held %t, want no turn and held", turning, held)
	}

	releaseAgent(t, dir)
	waitHistory(t, engineOf(restarted), rig.path, id, "the agent's turn done", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake agent done 1")
	})
	want := []Notice{{Session: id, Dir: dir, State: "done", Body: "fake agent done 1"}}
	if got := waitNotices(t, engineOf(restarted), sink); !reflect.DeepEqual(got, want) {
		t.Fatalf("notices after the restart = %+v, want %+v", got, want)
	}
}

// TestTurnEndUnderBackgroundCommandIsNotHeld runs BGBASH: its turn ends while a command a Bash call ran in the
// background is listed. A step waits for such a command, but it runs no model, so the turn's end of a session that is not
// a step records it done, not held, and tells the owner.
func TestTurnEndUnderBackgroundCommandIsNotHeld(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BGBASH"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the turn end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1
	})
	if history.State != "done" {
		t.Fatalf("state after a turn that ended under a background command = %q, want done", history.State)
	}
	if heldRow(t, m, id) {
		t.Fatal("row after a turn that ended under a background command is held")
	}
	want := []Notice{{Session: id, Dir: dir, State: "done", Body: "fake bgtool 1"}}
	if got := waitNotices(t, engineOf(m), sink); !reflect.DeepEqual(got, want) {
		t.Fatalf("notices = %+v, want %+v", got, want)
	}
}
