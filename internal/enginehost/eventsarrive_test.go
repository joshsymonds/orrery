package enginehost

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// liveFlags returns whether session id has a background task listed and whether a turn runs, as the manager records
// them under its lock.
func liveFlags(t *testing.T, m *Manager, id string) (background, turning bool) {
	t.Helper()
	live, err := m.live(id)
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return live.background, live.turning
}

// deliverStepEndOnce ends the step of the caller id, whose prompts so far are before, and passes the step end once and
// again. The first pass stores the step end as the session's next prompt, marks it, and writes it to the fake claude in
// dir, which reads it while the turn it has open runs or once that turn ends. The second pass writes nothing more,
// which the owner's marker shows once the fake has read it.
func deliverStepEndOnce(t *testing.T, m *Manager, s *store.Store, path, dir, id string, before []deliveredPrompt) {
	t.Helper()
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, deliverNowSummary)
	pass := stepEndPass(newStepEndSender(t, m, s), s)
	want := slices.Concat(before, []deliveredPrompt{stepEndPrompt()})

	passStep(t, pass)
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after one pass = %+v, want %+v", got, want)
	}
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after its delivery")
	}
	awaitStepEndWrites(t, m, dir, 1)

	passStep(t, pass)
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts after a second pass = %+v, want %+v", got, want)
	}
	if n := sayMarker(t, m, dir, id, "owner marker", "p-marker"); n != 1 {
		t.Fatalf("step ends read by the fake claude after a second pass = %d, want 1", n)
	}
}

// TestEventsArriveRunningTurn ends the step of a caller whose HANG turn runs. The step end reaches the session while
// the turn runs: one pass stores it, writes it and marks it, and a second pass writes nothing more.
func TestEventsArriveRunningTurn(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the hanging turn running", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1") && h.State == string(protocol.StateWorking)
	})
	deliverStepEndOnce(t, m, s, path, dir, id, []deliveredPrompt{{Text: "HANG"}})
}

// TestEventsArriveBackgroundTask ends the step of a caller whose turn has ended with a background task still listed.
// The step end reaches the session at once: one pass stores it, writes it and marks it, and a second pass writes
// nothing more.
func TestEventsArriveBackgroundTask(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BACKGROUND"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the background turn done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake background 1") && lastState(t, h) == string(protocol.StateDone)
	})
	if background, turning := liveFlags(t, m, id); !background || turning {
		t.Fatalf("background task listed = %v, turn running = %v, want listed and not running", background, turning)
	}
	deliverStepEndOnce(t, m, s, path, dir, id, []deliveredPrompt{{Text: "BACKGROUND"}})
}

// TestEventsArriveTurnAndBackgroundTask ends the step of a caller whose turn runs while a background task is listed.
// The step end reaches the session at once: one pass stores it, writes it and marks it, and a second pass writes
// nothing more.
func TestEventsArriveTurnAndBackgroundTask(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, fakeClaudePath(t))
	s := storeOf(t, m)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BGHANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the turn running with a task listed", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake bghang 1") && h.State == string(protocol.StateWorking)
	})
	if background, turning := liveFlags(t, m, id); !background || !turning {
		t.Fatalf("background task listed = %v, turn running = %v, want both", background, turning)
	}
	deliverStepEndOnce(t, m, s, path, dir, id, []deliveredPrompt{{Text: "BGHANG"}})
}

// TestEventsArriveIdleKeepsBackground advances the idle clock past the idle limit while a session has a background task
// listed after its turn ended. The session is kept while the task is listed and stopped by the idle limit once the task
// has ended.
func TestEventsArriveIdleKeepsBackground(t *testing.T) {
	t.Parallel()
	m, path, clock := newIdleManager(t)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "BACKGROUND"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the background turn done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake background 1") && lastState(t, h) == string(protocol.StateDone)
	})
	if background, _ := liveFlags(t, m, id); !background {
		t.Fatal("background task not listed before the idle check")
	}
	pid := fakePID(t, dir)

	clock.advance(defaultIdleAfter + time.Minute)
	m.checkIdle(t.Context())
	requireRunning(t, m, id, pid)

	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "BGEND"}); err != nil {
		t.Fatalf("Say BGEND: %v", err)
	}
	waitHistory(
		t,
		engineOf(m),
		path,
		id,
		"the reply after the background task ended",
		func(h store.EngineHistory) bool {
			return hasBlock(t, h, "fake reply 2") && lastState(t, h) == string(protocol.StateDone)
		},
	)

	clock.advance(10*time.Minute - time.Millisecond)
	m.checkIdle(t.Context())
	requireRunning(t, m, id, pid)

	clock.advance(time.Millisecond)
	m.checkIdle(t.Context())
	requireGone(t, pid)
}

// TestEventsArriveParentStepStaysOpen covers a parent step whose not-done turn ended while its child step runs. The
// child keeps the parent open, so the idle check past the idle limit keeps the parent's session, and the step stays
// unreturned.
func TestEventsArriveParentStepStaysOpen(t *testing.T) {
	t.Parallel()
	rig := newParentRig(t)
	repo := stepGitTestRepo(t)
	parent := rig.startParent(t, repo, "TRICKLEHELD one", nil)
	child := rig.startChild(t, repo, parent)
	rig.releaseStep(t, parent)
	settleAwaited(t, engineOf(rig.manager, rig.steps), rig.spy, stepsRunOf(t, rig.steps, parent), parent)
	if row := stepRowOf(t, rig.steps, child); row.End != "" {
		t.Fatalf("child %s ended %q, want it still running", child, row.End)
	}

	rig.clock.advance(defaultIdleAfter + time.Minute)
	rig.manager.checkIdle(t.Context())
	if !rig.manager.StepLive(parent) {
		t.Fatalf("session of step %s stopped by an idle check past the idle limit, want it kept", parent)
	}
	requireUnreturned(t, rig.steps, parent)
}
