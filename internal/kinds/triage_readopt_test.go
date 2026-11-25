package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestTriagedCheckFindsTheCheckItsStepTriages: the check a triage step judges is found by the step's id among the
// stored checks of the node run at its path: a gate run's check or a rerun of it, an integrate run's Done gate, and a
// Done gate of one of its task integrator rounds. A step of no stored check, and a run at another path, name none.
func TestTriagedCheckFindsTheCheckItsStepTriages(t *testing.T) {
	t.Parallel()
	gateRun := store.NodeRun{ID: testRunID, Path: "lint", Kind: "gate", Attempt: 1, State: store.NodeRunning}
	gateRows := Rows{
		Runs:   []store.NodeRun{gateRun},
		Checks: []store.NodeCheck{failedCheck("check"), failedCheck("check rerun")},
	}
	batch := batchNode("cand-1", conflictLeftOut, false).Run
	batch.State = store.NodeRunning
	batchRows := Rows{
		Epic:   conflictEpic(),
		Runs:   []store.NodeRun{batch},
		Checks: []store.NodeCheck{passedCheck("done 1"), failedCheck("done 2"), {ID: roundWork("done 1")}},
		Rounds: []store.IntegrateRound{recordedRound(1, RoundMerged, "head-1", "", "")},
	}
	cases := []struct {
		name, path, step, want string
		rows                   Rows
	}{
		{"a gate run's check", "lint", triageStepID("check"), workID("check"), gateRows},
		{"a rerun of a gate run's check", "lint", triageStepID("check rerun"), workID("check rerun"), gateRows},
		{"a step of a rerun not stored", "lint", triageStepID("check rerun rerun"), "", gateRows},
		{"a step of a run at another path", "other", triageStepID("check"), "", gateRows},
		{"an integrate run's Done gate", batchPath, triageStepID("done 2"), workID("done 2"), batchRows},
		{"a round's Done gate", batchPath, roundWork("triage done 1"), roundWork("done 1"), batchRows},
		{"a step of no check", batchPath, workID("task integrator T2 1"), "", batchRows},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, found := TriagedCheck(tc.rows, tc.path, tc.step)
			if got != tc.want || found != (tc.want != "") {
				t.Errorf("TriagedCheck() = %q, %v; want %q", got, found, tc.want)
			}
		})
	}
}
