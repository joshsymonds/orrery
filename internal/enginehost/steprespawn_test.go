package enginehost

import (
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// newStepIdleManager returns a step manager whose clock is set by hand, so a test can stop an idle step with checkIdle.
// Its store is at the returned path.
func newStepIdleManager(t *testing.T, stepCap int, sink NoticeSink) (*Manager, string, *idleClock) {
	t.Helper()
	m, path := newStepManager(t, stepCap, fakeClaudePath(t), sink)
	clock := newIdleClock()
	m.now = clock.now
	return m, path, clock
}

// testStep returns a step session for id in dir that runs the reviewer agent with stepSchema. Its ops write the steps row
// the respawn reads its agent and schema from, as the step run does.
func testStep(id, dir string) StepSession {
	return StepSession{
		ID: id, Dir: dir, Prompt: "hi", Agent: "reviewer", Schema: stepSchema,
		Ops: []store.EngineOp{store.StepAdd{ID: id, Agent: "reviewer", Prompt: "hi", Schema: stepSchema}},
	}
}

// stepDone reports whether the history of a step holds the block of its reply n and the step's turn has ended.
func stepDone(t *testing.T, h store.EngineHistory, reply string) bool {
	t.Helper()
	return hasBlock(t, h, reply) && lastState(t, h) == string(protocol.StateDone)
}

func TestContinueRespawnsIdleStoppedStepWithStoredLaunch(t *testing.T) {
	sink := &fakeSink{}
	m, path, clock := newStepIdleManager(t, 1, sink)
	dir := t.TempDir()
	if err := m.StartStep(t.Context(), testStep(stepA, dir)); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's first turn done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 1")
	})
	pid := fakePID(t, dir)

	clock.advance(10 * time.Minute)
	m.checkIdle(t.Context())
	requireGone(t, pid)

	if err := m.Continue(t.Context(), stepA, StepPrompt{Text: "again", Sender: store.PromptSenderOwner}); err != nil {
		t.Fatalf("Continue of an idle-stopped step: %v", err)
	}
	history := waitHistory(t, path, stepA, "the respawned step's reply done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 2")
	})
	if err := m.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop of the respawned step: %v", err)
	}

	args := fakeArgs(t, dir)
	if len(args) != 2 {
		t.Fatalf("fake claude starts = %q, want two", args)
	}
	if !hasFlagValue(args[1], "--resume", "session_1") {
		t.Fatalf("respawn args = %q, want --resume session_1", args[1])
	}
	if !hasFlagValue(args[1], "--agent", "reviewer") {
		t.Fatalf("respawn args = %q, want --agent reviewer", args[1])
	}
	if !hasFlagValue(args[1], "--json-schema", stepSchema) {
		t.Fatalf("respawn args = %q, want --json-schema %s", args[1], stepSchema)
	}
	if !slices.Contains(conversationOf(t, history), "prompt: again") {
		t.Fatalf("conversation = %q, want the respawn's prompt", conversationOf(t, history))
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after a respawned step's turn = %+v, want none", got)
	}
}

func TestRespawnedStepHandsItsTurnToTheObserverItStartedWith(t *testing.T) {
	m, path := newStepManager(t, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	obs := &stepObserver{}
	step := testStep(stepA, dir)
	step.Observer = obs
	if err := m.StartStep(t.Context(), step); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's first turn done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 1")
	})
	// Stop waits for the step's pump, so the first hand-off has reached the observer when it returns.
	if err := m.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := obs.returned(); got != 1 {
		t.Fatalf("hand-offs before the respawn = %d, want 1", got)
	}

	if err := m.Continue(t.Context(), stepA, StepPrompt{Text: "again", Sender: store.PromptSenderOwner}); err != nil {
		t.Fatalf("Continue of a stopped step: %v", err)
	}
	waitHistory(t, path, stepA, "the respawned step's reply done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 2")
	})
	if err := m.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop of the respawned step: %v", err)
	}
	if got := obs.returned(); got != 2 {
		t.Fatalf("hand-offs after the respawn = %d, want 2", got)
	}
}

func TestSayToIdleStoppedStepStillRefuses(t *testing.T) {
	m, path, clock := newStepIdleManager(t, 1, &fakeSink{})
	dir := t.TempDir()
	if err := m.StartStep(t.Context(), testStep(stepA, dir)); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's first turn done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 1")
	})
	clock.advance(10 * time.Minute)
	m.checkIdle(t.Context())

	if err := m.Say(t.Context(), SayRequest{ID: stepA, Text: "again"}); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Say to an idle-stopped step: err = %v, want ErrNoSession", err)
	}
	if args := fakeArgs(t, dir); len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want only the first", args)
	}
}

func TestContinueOfStoppedSessionStillRefuses(t *testing.T) {
	m, path := newStepManager(t, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the session's first reply done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	again := StepPrompt{Text: "again", Sender: store.PromptSenderOwner}
	if err := m.Continue(t.Context(), id, again); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Continue of a stopped session: err = %v, want ErrNoSession", err)
	}
	if args := fakeArgs(t, dir); len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want only the first", args)
	}
}

func TestRespawnWithStepCapFullGetsErrCapReached(t *testing.T) {
	m, path := newStepManager(t, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	if err := m.StartStep(t.Context(), testStep(stepA, dir)); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's first turn done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := m.StartStep(t.Context(), testStep(stepB, t.TempDir())); err != nil {
		t.Fatalf("StartStep of the other step: %v", err)
	}

	again := StepPrompt{Text: "again", Sender: store.PromptSenderOwner}
	if err := m.Continue(t.Context(), stepA, again); !errors.Is(err, ErrCapReached) {
		t.Fatalf("Continue with the step cap full: err = %v, want ErrCapReached", err)
	}
	if args := fakeArgs(t, dir); len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want only the first", args)
	}
}

func TestRespawnOfStepWhoseDirIsGoneRecordsFailedWithoutNotice(t *testing.T) {
	sink := &fakeSink{}
	m, path := newStepManager(t, 1, fakeClaudePath(t), sink)
	dir := t.TempDir()
	if err := m.StartStep(t.Context(), testStep(stepA, dir)); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's first turn done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), stepA); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove step dir: %v", err)
	}

	again := StepPrompt{Text: "again", Sender: store.PromptSenderOwner}
	if err := m.Continue(t.Context(), stepA, again); !errors.Is(err, ErrBadDir) {
		t.Fatalf("Continue with the dir gone: err = %v, want ErrBadDir", err)
	}
	if got := lastState(t, historyOf(t, path, stepA)); got != stateFailed {
		t.Fatalf("state after the failed respawn = %q, want %q", got, stateFailed)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after the failed respawn = %+v, want none", got)
	}
}
