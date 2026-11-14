package enginehost

import (
	"errors"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestReleaseFreesStepSlotAndRecordsNothing covers R1: with the step cap full by a handed-off step, Release stops its
// process, frees its slot so another step starts, records nothing and tells the observer of no exit.
func TestReleaseFreesStepSlotAndRecordsNothing(t *testing.T) {
	t.Parallel()
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
	waitUntil(t, "the step's hand-off", func() bool { return obs.returned() == 1 })
	if err := m.StartStep(t.Context(), testStep(stepB, t.TempDir())); !errors.Is(err, ErrCapReached) {
		t.Fatalf("StartStep with the step cap full: err = %v, want ErrCapReached", err)
	}
	pid := fakePID(t, dir)

	if err := m.Release(t.Context(), stepA); err != nil {
		t.Fatalf("Release: %v", err)
	}
	requireGone(t, pid)

	if err := m.StartStep(t.Context(), testStep(stepB, t.TempDir())); err != nil {
		t.Fatalf("StartStep after the release: %v", err)
	}
	if got := lastState(t, historyOf(t, path, stepA)); got != string(protocol.StateDone) {
		t.Fatalf("state of the released step = %q, want %q", got, protocol.StateDone)
	}
	if got := obs.exited(); len(got) != 0 {
		t.Fatalf("observer exits after the release = %q, want none", got)
	}
}

// TestContinueRespawnsReleasedStepInItsSession covers R2: Continue of a released step respawns its claude process in
// the same session with --resume, and the prompt's turn ends as usual.
func TestContinueRespawnsReleasedStepInItsSession(t *testing.T) {
	t.Parallel()
	m, path := newStepManager(t, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	if err := m.StartStep(t.Context(), testStep(stepA, dir)); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's first turn done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 1")
	})
	pid := fakePID(t, dir)

	if err := m.Release(t.Context(), stepA); err != nil {
		t.Fatalf("Release: %v", err)
	}
	requireGone(t, pid)

	if err := m.Continue(t.Context(), stepA, StepPrompt{Text: "again", Sender: store.PromptSenderOwner}); err != nil {
		t.Fatalf("Continue of a released step: %v", err)
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
	if !slices.Contains(conversationOf(t, history), "prompt: again") {
		t.Fatalf("conversation = %q, want the respawn's prompt", conversationOf(t, history))
	}
}

// TestReleaseOfStepNotLiveReturnsNil covers the no-op: Release of a step whose process was already released, and of an
// id with no live session, returns nil.
func TestReleaseOfStepNotLiveReturnsNil(t *testing.T) {
	t.Parallel()
	m, path := newStepManager(t, 1, fakeClaudePath(t), &fakeSink{})
	dir := t.TempDir()
	if err := m.StartStep(t.Context(), testStep(stepA, dir)); err != nil {
		t.Fatalf("StartStep: %v", err)
	}
	waitHistory(t, path, stepA, "the step's first turn done", func(h store.EngineHistory) bool {
		return stepDone(t, h, "fake reply 1")
	})
	pid := fakePID(t, dir)
	if err := m.Release(t.Context(), stepA); err != nil {
		t.Fatalf("Release of a live step: %v", err)
	}
	requireGone(t, pid)

	if err := m.Release(t.Context(), stepA); err != nil {
		t.Fatalf("Release of a released step: err = %v, want nil", err)
	}
	if err := m.Release(t.Context(), stepB); err != nil {
		t.Fatalf("Release of an id with no live session: err = %v, want nil", err)
	}
	if args := fakeArgs(t, dir); len(args) != 1 {
		t.Fatalf("fake claude starts = %q, want only the first", args)
	}
}
