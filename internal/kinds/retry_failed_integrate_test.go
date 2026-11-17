package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// rerunAttempt is the attempt a failed integrate run is rerun at: its first rerun.
const rerunAttempt = 2

// rerunBatchNode returns the integrate run at batchPath of the test run at rerunAttempt, rerun on the revision and result
// its failed attempt claimed, with its refs not yet checked again.
func rerunBatchNode() Node {
	return Node{Run: store.NodeRun{
		ID: testRunID, Path: batchPath, Kind: "integrate", Attempt: rerunAttempt,
		Revision: testRev, Result: composedOne, State: store.NodeRunning,
	}}
}

// TestRetryFailedIntegrateDecidesDoneGatesAgain checks that an integrate run rerun at its next attempt walks its Done
// gates under that attempt's ids, so a gate its failed attempt ran is run again on the same candidate. The failed
// attempt's check stays stored, and does not decide the rerun.
func TestRetryFailedIntegrateDecidesDoneGatesAgain(t *testing.T) {
	t.Parallel()
	epic := integrateEpic()
	failedFirstGate := failedCheck("done 1")
	rerunFirstGate := store.NodeCheck{ID: attemptWorkID(rerunAttempt, "done 1"), State: store.NodeRunning}
	rerunFirstPassed := store.NodeCheck{ID: attemptWorkID(rerunAttempt, "done 1"), State: store.NodeEnded}
	cases := []decideCase{
		{
			"rerun starts done gate 1 under its attempt though the failed attempt's check is stored",
			rerunBatchNode(),
			Rows{Epic: epic, Checks: []store.NodeCheck{failedFirstGate}},
			Action{Kind: ActStart, IDs: []string{attemptWorkID(rerunAttempt, "done 1")}, Items: []string{"just check"}},
		},
		{
			"rerun's running done gate 1 waits",
			rerunBatchNode(),
			Rows{Epic: epic, Checks: []store.NodeCheck{failedFirstGate, rerunFirstGate}},
			Action{Kind: ActNone},
		},
		{
			"rerun's passed done gate 1 starts done gate 2 under its attempt",
			rerunBatchNode(),
			Rows{Epic: epic, Checks: []store.NodeCheck{failedFirstGate, rerunFirstPassed}},
			Action{Kind: ActStart, IDs: []string{attemptWorkID(rerunAttempt, "done 2")}, Items: []string{"just lint"}},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}
