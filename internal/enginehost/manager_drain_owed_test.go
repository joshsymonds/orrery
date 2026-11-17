package enginehost

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestDrainOwesSaySentBeforeStoredTurnEndWhileOldHolderHolds holds a Say while a JUDGEHOLDS turn holds, then releases that
// turn. The turn's end writes the held Say as the turn after it, so the Say is sent and stored before the restart, and the
// turn end does not list it. The Say's trigger holds before it opens its turn, so the Say is still owed when the holder is
// re-adopted one version older. The session stays live, the held reply and its turn end are recorded on the old holder, and
// the holder stops, so the next Say starts claude with --resume.
func TestDrainOwesSaySentBeforeStoredTurnEndWhileOldHolderHolds(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))
	if err := rig.manager.Say(t.Context(), SayRequest{ID: id, Text: "WAITOPEN"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if got := heldTexts(t, rig.path, id); !slices.Equal(got, []string{"WAITOPEN"}) {
		t.Fatalf("held messages during the turn = %q, want [WAITOPEN]", got)
	}
	if got := writtenPrompts(t, historyOf(t, rig.path, id)); slices.Contains(got, "WAITOPEN") {
		t.Fatalf("prompts written during the turn = %q, want the Say held", got)
	}
	releaseHeldTurn(t, dir)
	waitHistory(
		t,
		engineOf(rig.manager),
		rig.path,
		id,
		"the first turn's stored end, with the held Say written after it",
		func(h store.EngineHistory) bool {
			return turnEndCount(h) == 1 && slices.Contains(writtenPrompts(t, h), "WAITOPEN")
		},
	)

	restarted, _ := restartDrain(t, rig, rig.store, holder.WireVersion+1)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart: live %v", id, restarted.Live())
	}
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.open"), nil, 0o600); err != nil {
		t.Fatalf("write open: %v", err)
	}
	waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the held prompt's reply",
		func(h store.EngineHistory) bool {
			return h.State == "done" && hasBlock(t, h, "fake reply 2")
		},
	)
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if err := restarted.Say(t.Context(), SayRequest{ID: id, Text: "after"}); err != nil {
		t.Fatalf("Say after: %v", err)
	}
	// Say returns once the new holder has the prompt, which can be before its claude has started and logged its
	// arguments. The reply to the Say comes from that claude, so its start is logged by then.
	waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the reply to the Say after the stop",
		func(h store.EngineHistory) bool {
			return h.State == "done" && hasBlock(t, h, "fake reply 3")
		},
	)
	starts := fakeStarts(t, dir)
	if len(starts) != 2 || !strings.Contains(starts[1], "--resume") {
		t.Fatalf("starts = %q, want a second start with --resume", starts)
	}
}

// TestDrainFollowsSaySentBeforeRestartWhileOldHolderRuns holds a Say while a JUDGEHOLDS turn holds, before the restart, and
// re-adopts the holder one version older. The turn ends with no live process after it, so the holder stops and the Say stays
// held: the old holder never receives it. The next Say starts the session with --resume, and writes the held Say first.
func TestDrainFollowsSaySentBeforeRestartWhileOldHolderRuns(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))
	if err := rig.manager.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say while the turn holds: %v", err)
	}

	restarted, _ := restartDrain(t, rig, rig.store, holder.WireVersion+1)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart: live %v", id, restarted.Live())
	}

	releaseHeldTurn(t, dir)
	waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the first turn's end",
		func(h store.EngineHistory) bool {
			return turnEndCount(h) == 1
		},
	)
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after the drained turn ended, want it retired", id)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if got := heldTexts(t, rig.path, id); !slices.Equal(got, []string{"again"}) {
		t.Fatalf("held messages after the holder stopped = %q, want [again] still held", got)
	}
	if n := stdinLinesWith(t, dir, "again"); n != 0 {
		t.Fatalf("stdin lines with the held Say on the old holder = %d, want none", n)
	}
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts = %q, want the one start: nothing starts the session", starts)
	}

	if err := restarted.Say(t.Context(), SayRequest{ID: id, Text: "after"}); err != nil {
		t.Fatalf("Say after the stop: %v", err)
	}
	history := waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the reply to the Say after the stop",
		func(h store.EngineHistory) bool {
			return h.State == "done" && hasBlock(t, h, "fake reply 3")
		},
	)
	written := writtenPrompts(t, history)
	if again, after := slices.Index(
		written,
		"again",
	), slices.Index(
		written,
		"after",
	); again < 0 || after < 0 ||
		again > after {
		t.Fatalf("prompts written = %q, want the held Say before the new one", written)
	}
	starts := fakeStarts(t, dir)
	if len(starts) != 2 || !strings.Contains(starts[1], "--resume") {
		t.Fatalf("starts = %q, want a second start with --resume", starts)
	}
}

// TestDrainFoldsSaySentBeforeRestartIntoRunningTurn holds a Say while a JUDGEHOLDS turn holds, before the restart, and
// re-adopts the holder one version older. The Say is never folded into the running turn: the turn ends with no following
// turn, the holder stops at that turn's end, and the old holder never reads the Say.
func TestDrainFoldsSaySentBeforeRestartIntoRunningTurn(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))
	if err := rig.manager.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say while the turn holds: %v", err)
	}

	restarted, _ := restartDrain(t, rig, rig.store, holder.WireVersion+1)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart: live %v", id, restarted.Live())
	}

	releaseHeldTurn(t, dir)
	history := waitHistory(
		t,
		engineOf(rig.manager, restarted),
		rig.path,
		id,
		"the first turn's end",
		func(h store.EngineHistory) bool {
			return turnEndCount(h) == 1 && h.State == "done"
		},
	)
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after the turn ended, want it retired", id)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if turns := turnEndCount(history); turns != 1 {
		t.Fatalf("turn ends = %d, want 1: no following turn", turns)
	}
	if got := heldTexts(t, rig.path, id); !slices.Equal(got, []string{"again"}) {
		t.Fatalf("held messages = %q, want [again] still held", got)
	}
	if n := stdinLinesWith(t, dir, "again"); n != 0 {
		t.Fatalf("stdin lines with the held Say on the old holder = %d, want none", n)
	}
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts = %q, want the one start", starts)
	}
}
