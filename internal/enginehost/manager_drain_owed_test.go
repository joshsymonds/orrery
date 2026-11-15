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

// TestDrainOwesSaySentBeforeStoredTurnEndWhileOldHolderHolds sends a Say while a JUDGEHOLDS turn holds, then releases that
// turn, so its end is stored before the restart. The Say's trigger holds before it opens its turn, and the turn end does not
// list the Say, so the Say is still owed when the holder is re-adopted one version older. The session stays live, the held
// reply and its turn end are recorded on the old holder, and the holder stops, so the next Say starts claude with --resume.
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
	releaseHeldTurn(t, dir)
	waitHistory(t, rig.path, id, "the first turn's stored end", func(h store.EngineHistory) bool {
		return h.State == "done"
	})

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
	waitHistory(t, rig.path, id, "the held prompt's reply", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake reply 2")
	})
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if err := restarted.Say(t.Context(), SayRequest{ID: id, Text: "after"}); err != nil {
		t.Fatalf("Say after: %v", err)
	}
	starts := fakeStarts(t, dir)
	if len(starts) != 2 || !strings.Contains(starts[1], "--resume") {
		t.Fatalf("starts = %q, want a second start with --resume", starts)
	}
}

// TestDrainFollowsSaySentBeforeRestartWhileOldHolderRuns sends a Say while a JUDGEHOLDS turn holds, before the restart,
// and re-adopts the holder one version older. The turn ends without listing the Say as answered, so claude runs the Say as
// a following turn on that same holder, which stops only after that turn has ended.
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
	waitHistory(t, rig.path, id, "the reply to the Say sent before the restart", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake reply 2")
	})
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after the reply's turn ended, want it retired", id)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts = %q, want the one start: the Say answered on the same holder", starts)
	}
}

// TestDrainFoldsSaySentBeforeRestartIntoRunningTurn sends a Say while a FOLD turn holds, before the restart, and re-adopts
// the holder one version older. claude folds the Say into the running turn, and that turn ends by listing both prompts as
// answered. No following turn runs, and the holder stops at that turn's end.
func TestDrainFoldsSaySentBeforeRestartIntoRunningTurn(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, nil, false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "FOLD"})
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
	history := waitHistory(t, rig.path, id, "the done state of the folded turn", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake fold 1")
	})
	if slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is live after the folded turn ended, want it retired", id)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	if got := blockTexts(t, history); !slices.Equal(got, []string{"fake fold 1"}) {
		t.Fatalf("finished blocks = %q, want only the folded turn's block: no following turn", got)
	}
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts = %q, want the one start", starts)
	}
	if n := stdinLinesWith(t, dir, "again"); n != 1 {
		t.Fatalf("stdin lines with the Say = %d, want the Say read once", n)
	}
}
