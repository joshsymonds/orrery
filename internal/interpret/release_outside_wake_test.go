package interpret

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// slotOutsideWakeD is the epic of the outside-wake test that reads a release slot an epic holds, not an outside hold.
const slotOutsideWakeD = "orrery-1a2b3c4/wake-outside-d"

// TestSlotOutsideHoldWakeOutsideWaitersPassesWaiter: C reads the release slot of its repository, held by an outside
// releaser, and waits on that hold. WakeOutsideWaiters, which an outside hold given back calls, wakes C, so C's flow is
// read again.
func TestSlotOutsideHoldWakeOutsideWaitersPassesWaiter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	_, held, err := s.HoldReleaseSlot(ctx, runRepo, "gambit:e1", runClock().Add(30*time.Minute))
	if err != nil || !held {
		t.Fatalf("HoldReleaseSlot = held %v, %v, want the slot held for gambit:e1", held, err)
	}
	flowWakeCreate(t, s, flowWakeC, runPhase)
	rig := newFlowWakeRig(t, s)
	rig.runner.Pass(ctx)
	rig.start()
	rig.awaitPass()
	readsC := rig.reads.flowWakeReads(flowWakeC)
	rig.runner.mu.Lock()
	_, waiting := rig.runner.waiters["outside:gambit:e1"][flowWakeC]
	rig.runner.mu.Unlock()
	if !waiting {
		t.Fatalf("C does not wait on the outside hold after its read, so a wake of the hold cannot reach it")
	}

	rig.runner.WakeOutsideWaiters()
	rig.awaitPass()
	rig.stop()
	if got := rig.reads.flowWakeReads(flowWakeC) - readsC; got != 1 {
		t.Errorf("reads of C's flow = %d, want 1", got)
	}
	if got := rig.passCount.Load(); got != 2 {
		t.Errorf("passes = %d, want the start pass and one pass", got)
	}
}

// TestSlotOutsideHoldWakeOutsideWaitersOnlyOutside: C reads a release slot held by an outside releaser, and D reads one
// held by the epic flowWakeH. WakeOutsideWaiters wakes C alone. It names no epic for the outside holder, and it leaves
// the waiters of the epic holder waiting.
func TestSlotOutsideHoldWakeOutsideWaitersOnlyOutside(t *testing.T) {
	t.Parallel()
	runner := New(Config{Planner: &fakePlanner{}, Builder: &fakeBuilder{}, Now: runClock})
	runner.noteRead(flowWakeC, store.EpicFlow{Slot: "outside:gambit:e1"})
	runner.noteRead(slotOutsideWakeD, store.EpicFlow{Slot: flowWakeH})

	runner.WakeOutsideWaiters()
	if got := runner.takeWoken(); !slices.Equal(got, []string{flowWakeC}) {
		t.Errorf("woken = %v, want only %s", got, flowWakeC)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if _, ok := runner.waiters[flowWakeH][slotOutsideWakeD]; !ok {
		t.Errorf("waiters of %s = %v, want %s still waiting", flowWakeH, runner.waiters[flowWakeH], slotOutsideWakeD)
	}
	for holder := range runner.waiters {
		if strings.HasPrefix(holder, store.ReleaseSlotOutsidePrefix) {
			t.Errorf("waiters still hold outside holder %q", holder)
		}
	}
}
