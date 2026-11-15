package enginehost

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

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
