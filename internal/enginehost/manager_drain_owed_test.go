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

// TestDrainOwesSaySentBeforeStoredTurnEndWhileOldHolderHolds sends a Say while a JUDGEHOLDS turn runs, then releases that
// turn. The Say is written at once, so it is sent and stored before the turn ends, and the turn reads none of it, so the
// turn's end leaves it unanswered and claude runs it as the turn after. Its trigger holds before it opens its turn, so the
// Say is still owed when the holder is re-adopted one version older. The session stays live, the reply and its turn end are
// recorded on the old holder, and the holder stops, so the next Say starts claude with --resume.
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
	if got := writtenPrompts(t, historyOf(t, rig.path, id)); !slices.Contains(got, "WAITOPEN") {
		t.Fatalf("prompts written when Say returns = %q, want the Say written at once", got)
	}
	releaseHeldTurn(t, dir)
	waitHistory(
		t,
		engineOf(rig.manager),
		rig.path,
		id,
		"the first turn's stored end, with the Say written before it",
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
		"the reply to the Say written before the restart",
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

// TestDrainFollowsSaySentBeforeRestartWhileOldHolderRuns sends a Say while a JUDGEHOLDS turn runs, before the restart, and
// re-adopts the holder one version older. The Say is written at once, and the turn reads none of it, so its end leaves the
// Say unanswered: the old holder runs the Say's turn as the turn after, and only then has no live process after it, so the
// holder stops. The next Say starts the session with --resume, and writes the earlier Say first.
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
		t.Fatalf("Say while the turn runs: %v", err)
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
		"the turn's end of the Say the old holder answers",
		func(h store.EngineHistory) bool {
			return turnEndCount(h) == 2 && h.State == "done"
		},
	)
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after the Say was answered, want it retired", id)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if n := stdinLinesWith(t, dir, "again"); n != 1 {
		t.Fatalf("stdin lines with the Say on the old holder = %d, want it read once", n)
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

// TestDrainFoldsSaySentBeforeRestartIntoRunningTurn sends a Say while a JUDGEHOLDS turn runs, before the restart, and
// re-adopts the holder one version older. The turn reads none of the Say, so it is not folded into the running turn: the
// Say is written at once and claude runs it as the turn after. The holder stops only once that turn ends.
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
		t.Fatalf("Say while the turn runs: %v", err)
	}
	if got := writtenPrompts(t, historyOf(t, rig.path, id)); !slices.Contains(got, "again") {
		t.Fatalf("prompts written when Say returns = %q, want the Say written at once", got)
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
		"the done state after the Say's turn",
		func(h store.EngineHistory) bool {
			return turnEndCount(h) == 2 && h.State == "done"
		},
	)
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after its turns ended, want it retired", id)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if turns := turnEndCount(history); turns != 2 {
		t.Fatalf("turn ends = %d, want 2: the running turn, then the Say's turn", turns)
	}
	if n := stdinLinesWith(t, dir, "again"); n != 1 {
		t.Fatalf("stdin lines with the Say on the old holder = %d, want it read once", n)
	}
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts = %q, want the one start", starts)
	}
}
