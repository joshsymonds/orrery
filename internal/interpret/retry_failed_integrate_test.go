package interpret

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// retryFailedIntegrateDoneGateRerunsOnCandidateAndAdvancesBranch: a batch whose Done gate failed is rerun by the
// owner. The gate runs again on the same candidate under the rerun's attempt, the capture follows, and once the batch
// passes the epic's branch advances to the candidate and the stored base follows it, as a first pass does.
func retryFailedIntegrateDoneGateRerunsOnCandidateAndAdvancesBranch(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		w, candidate := images.integrateWorld(t, atGate)
		w.endGate(integrateBatch, 1, "lint failed")
		w.pass()
		if run := runAt(t, w.s, integrateBatch); run.State != store.NodeEnded || run.Outcome != "failed" {
			t.Fatalf("batch = %+v, want ended failed before the rerun", run)
		}

		if err := w.s.RetryNode(
			ctx,
			runEpic,
			"integrate",
			store.Maker{By: store.MakerOwner},
			time.Unix(1700000000, 0),
		); err != nil {
			t.Fatalf("RetryNode of the failed batch: %v", err)
		}
		w.pass()
		rerun := runAt(t, w.s, integrateBatch)
		if rerun.Attempt != 1 || rerun.State != store.NodeRunning || rerun.Revision != candidate {
			t.Fatalf("batch after the rerun = %+v, want running at attempt 1 on candidate %s", rerun, candidate)
		}
		wantGate := store.NodeCheck{
			ID: workID(t, w.s, integrateBatch, "done 1"), Epic: runEpic, Path: integrateBatch, Revision: candidate,
			Argv: []string{"./bin/lint"}, Limit: 60, Needs: []store.EpicGateNeed{{Pool: "lint", Count: 2}},
		}
		if got := w.fakes.checks.calls; len(got) != 2 || !reflect.DeepEqual(got[1], wantGate) {
			t.Fatalf("checks run = %+v, want the failed gate then the rerun %+v", got, wantGate)
		}

		w.endGate(integrateBatch, 0, "")
		w.pass()
		w.endCapture(integrateBatch)
		pending := w.pending()
		w.pass()

		run := runAt(t, w.s, integrateBatch)
		if run.State != store.NodeEnded || run.Outcome != "passed" || run.Attempt != 1 {
			t.Errorf("batch = %+v, want ended passed at attempt 1", run)
		}
		if epic := epicOf(t, w.s); epic.StoredBase != candidate || epic.Candidate != "" {
			t.Errorf("stored base %q and candidate %q, want %q and none", epic.StoredBase, epic.Candidate, candidate)
		}
		if tip := w.git("rev-parse", integrateBranch); tip != candidate {
			t.Errorf("branch = %s, want the candidate %s", tip, candidate)
		}
		if got := w.pending(); got != pending+1 {
			t.Errorf("pending triggers = %d, want %d: the passed rerun wakes the planner once", got, pending+1)
		}
		if got := len(w.fakes.evidence.calls); got != 1 {
			t.Errorf("%d evidence runs, want one capture at the candidate", got)
		}
		if call := w.fakes.evidence.calls[0]; call.revision != candidate || call.phase != evidence.PhaseGreen {
			t.Errorf("capture = %+v, want a green capture at the candidate %s", call, candidate)
		}
	}
}
