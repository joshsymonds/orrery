package enginehost

import (
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// countingWake sets the event hook of m to count its calls, and returns the count. The manager calls the hook
// synchronously, so a count read after a call returns includes the calls that call made.
func countingWake(m *Manager) *atomic.Int32 {
	var n atomic.Int32
	m.eventWake = func() { n.Add(1) }
	return &n
}

// TestStepEndPushManagerWakeOwnerSayClearsQuiet interrupts a turn that runs until an interrupt (HANG), which leaves the
// session quiet. The owner's Say clears the quiet before its prompt is sent, so the hook is called before the send, and
// once more when Say returns nil.
func TestStepEndPushManagerWakeOwnerSayClearsQuiet(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	wakes := countingWake(m)
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

	before := wakes.Load()
	var atSend int32
	m.promptWritten = func(string) { atSend = wakes.Load() }
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if atSend == before {
		t.Fatalf(
			"EventWake calls before the owner's prompt was sent = %d, want more than %d: the Say cleared the quiet "+
				"without calling the hook",
			atSend,
			before,
		)
	}
	if after := wakes.Load(); after == atSend {
		t.Fatalf("EventWake calls after the owner's Say returned nil = %d, want more than %d", after, atSend)
	}
}

// TestStepEndPushManagerWakeResumeStoppedSession resumes an interrupted session that no process runs. Resume sends its
// prompt through Say, which starts the process, so the session goes live and the hook is called.
func TestStepEndPushManagerWakeResumeStoppedSession(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	dir := t.TempDir()
	seedClaudeRow(t, s, "interrupted", dir, "interrupted", store.KindEngine)
	stallNextPrompt(t, dir)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := newReconcileManager(t, s, writer, &fakeSink{})
	wakes := countingWake(m)

	if err := m.Resume(t.Context(), "interrupted"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !slices.Contains(m.Live(), "interrupted") {
		t.Fatalf("session is not live after Resume: live %v", m.Live())
	}
	if wakes.Load() == 0 {
		t.Fatal("EventWake not called when Resume brought the stopped session back")
	}
}

// TestStepEndPushManagerWakeStopFreesSlotUnderFullCap fills the cap of one with a session and checks a second start is
// refused. Stopping the session frees its slot, and the hook is called for the freed slot before the stop returns.
func TestStepEndPushManagerWakeStopFreesSlotUnderFullCap(t *testing.T) {
	t.Parallel()
	m, _ := newTestManager(t, 1, fakeClaudePath(t))
	wakes := countingWake(m)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "again"}); !errors.Is(err, ErrCapReached) {
		t.Fatalf("Start under the full cap error = %v, want ErrCapReached", err)
	}

	before := wakes.Load()
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if wakes.Load() == before {
		t.Fatal("EventWake not called when the stop freed the session slot under the full cap")
	}
}

// TestStepEndPushManagerWakeAnsweredRequestCloses starts a session that asks a question and answers it. The answer
// closes the session's only open request, and the hook is called for it.
func TestStepEndPushManagerWakeAnsweredRequestCloses(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	wakes := countingWake(m)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == string(protocol.StateWaiting)
	})
	requestID := requestIDOf(t, history)

	before := wakes.Load()
	if err := m.Answer(t.Context(), id, requestID, "red"); err != nil {
		t.Fatalf("Answer red: %v", err)
	}
	if wakes.Load() == before {
		t.Fatal("EventWake not called when the answer closed the session's only open request")
	}
}

// TestStepEndPushManagerWakeReadoptedSessionGoesLive ends a session whose turn is done, leaving its holder running,
// then restarts the manager on the same store. Reconcile re-adopts the holder, which takes the session live, and the
// hook is called for it.
func TestStepEndPushManagerWakeReadoptedSessionGoesLive(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(
		t,
		engineOf(rig.manager),
		rig.path,
		id,
		"the done state of the first turn",
		func(h store.EngineHistory) bool {
			return h.State == string(protocol.StateDone)
		},
	)

	rig.crash.refuse()
	rig.manager.Close()
	writer := NewWriter(WriterConfig{Applier: rig.store, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	restarted := newStateManager(t, rig.store, writer, &fakeSink{}, fakeSystemdRunPath(t), rig.root, nil)
	wakes := countingWake(restarted)

	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile after the crash: %v", err)
	}
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart: live %v", id, restarted.Live())
	}
	if wakes.Load() == 0 {
		t.Fatal("EventWake not called when the re-adopted session went live")
	}
}
