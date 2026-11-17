package interpret

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// laterBy sets the runner's clock to by after runClock, so the runner's reads and writes from then on are stamped
// then.
func (w *integrateWorld) laterBy(by time.Duration) {
	w.t.Helper()
	at := runClock().Add(by)
	w.runner.now = func() time.Time { return at }
}

// syncPastAFailedBatchThatComposedNothingLeavesNoCandidate: a batch whose tasks were all left out composes nothing
// and fails at the stored base, with no candidate. Its tasks were built on the epic's base, an ancestor of the stored
// base, and change the files the first batch changed since, so neither composes. A sync then moves the stored base past it. The owner's retry of that
// failed batch ends it failed again at once, at the old base, and sets no candidate: one would hold the epic behind
// its stored base, where no sync starts while it stands and the next batch composes on it. So the next turn's batch,
// built on the sync's merge, composes on the stored base and advances the branch without a park, and a later move of
// origin's main is synced again.
func syncPastAFailedBatchThatComposedNothingLeavesNoCandidate(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		w, candidate := images.syncWorld(t, atCaptured)
		if !w.passed() {
			t.Fatal("the planner got no turn after the first batch passed")
		}

		completeTurn(t, w.s, runPlan("work"))
		w.pass()
		w.accept(3, w.base, w.commit(w.base, "internal/a/x.go", "three\n"))
		w.accept(4, w.base, w.commit(w.base, "b.go", "four\n"))
		if !w.passed() {
			t.Error("the planner got no turn after the batch that composed nothing failed")
		}
		leftOut := fmt.Sprintf(
			"T3 was built on %s, not on the batch base %s\nT4 was built on %s, not on the batch base %s",
			w.base,
			candidate,
			w.base,
			candidate,
		)
		if batch := runAt(t, w.s, integrateNextBatch); batch.Outcome != outcomeFailed || batch.Output != leftOut ||
			batch.Revision != candidate {
			t.Fatalf("batch = %+v, want failed at the stored base %s with %q", batch, candidate, leftOut)
		}
		if epic := epicOf(t, w.s); epic.Candidate != "" {
			t.Fatalf("candidate = %q after a batch that composed nothing, want none", epic.Candidate)
		}

		originHead := w.originCommit("origin.txt")
		w.laterBy(syncInterval)
		w.pass()
		merge := runAt(t, w.s, syncSecond).Revision
		w.endGate(syncSecond, 0, "")
		w.pass()
		w.endCapture(syncSecond)
		w.pass()
		w.requireSyncEnded(syncSecond, outcomePassed, fmt.Sprintf(
			"merged origin's commit %s into the stored base %s as %s\nR1 green", originHead, candidate, merge))

		if err := w.s.RetryNode(ctx, runEpic, "integrate"); err != nil {
			t.Fatalf("RetryNode of the failed batch: %v", err)
		}
		w.pass()
		batch := runAt(t, w.s, integrateNextBatch)
		if batch.Attempt != 1 || batch.Outcome != outcomeFailed || batch.Revision != candidate {
			t.Fatalf("batch after the retry = %+v, want its rerun at attempt 1 failed again at %s", batch, candidate)
		}
		epic := epicOf(t, w.s)
		if epic.Candidate != "" || epic.StoredBase != merge || epic.Parked != "" {
			t.Fatalf("candidate %q, stored base %q, parked %q after the retry, want none, the merge %s and not parked",
				epic.Candidate, epic.StoredBase, epic.Parked, merge)
		}

		w.nextTurn(merge, 5, 6, thirdBatch)
		later := w.originCommit("later.txt")
		w.pass()
		third := runAt(t, w.s, thirdBatch)
		if third.Outcome != outcomePassed {
			t.Fatalf("next batch = %+v, want it passed", third)
		}
		if built := w.git("rev-parse", third.Revision+"~2"); built != merge {
			t.Errorf("the next batch builds on %s, want the merge %s", built, merge)
		}
		epic = epicOf(t, w.s)
		if epic.StoredBase != third.Revision || epic.Candidate != "" || epic.Parked != "" {
			t.Errorf("stored base %q, candidate %q, parked %q, want the next batch %s, none and not parked",
				epic.StoredBase, epic.Candidate, epic.Parked, third.Revision)
		}
		if tip := w.git("rev-parse", integrateBranch); tip != third.Revision {
			t.Errorf("branch = %s, want the next batch %s", tip, third.Revision)
		}
		sync := runAt(t, w.s, syncThird)
		if sync.State != store.NodeRunning || sync.Revision == "" {
			t.Fatalf("sync = %+v, want the later move of origin's main merging", sync)
		}
		if parents := w.git("rev-parse", sync.Revision+"^1", sync.Revision+"^2"); parents != third.Revision+"\n"+later {
			t.Errorf("merge parents = %q, want %s and %s", parents, third.Revision, later)
		}
	}
}
