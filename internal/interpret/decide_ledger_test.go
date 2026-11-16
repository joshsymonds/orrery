package interpret

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// ledgerCorrecting returns rows whose epic is correcting its review, which is review.
func ledgerCorrecting(rows kinds.Rows, review store.Review) kinds.Rows {
	rows.Epic.Phase = "correcting"
	rows.Review = review
	return rows
}

// ledgerLoop returns the loop the ledger cases run: two rounds at most, until closure passes, spent continue, and a
// body of the plan fix, then closure.
func ledgerLoop() workflow.Node {
	return loopNode(
		workflow.Loop{Max: 2, Until: workflow.Outcome{Node: "closure", Name: "passed"}, Spent: "continue"},
		kindNode("fix", "plan", kindNode("rebuild", "build")),
		kindNode("closure", "closure"),
	)
}

// ledgerOpen is a review whose one confirmed finding security-1 is open in every round.
func ledgerOpen() store.Review {
	return store.Review{Ledger: []store.LedgerFinding{{
		ID: "security-1", Reviewer: "security", Result: "confirmed", Claim: "the claim", Citation: "the citation",
		Location: "the location", Evidence: "the evidence", Reason: "the reason",
	}}}
}

func TestDecideLedgerPlanTrigger(t *testing.T) {
	t.Parallel()
	planDone := ended(startOf("plan", "plan", "plan", 0), "complete")
	flow := []workflow.Node{planNode(), ledgerLoop()}
	emptyReview := store.Review{}
	openReview := ledgerOpen()
	decideCases(t, []decideCase{
		{
			name: "fix in round 1 that starts while correcting writes the ledger trigger, empty ledger included",
			flow: flow,
			rows: ledgerCorrecting(rowsOf(nil, nil, planDone, running(loopOf(1, 0))), emptyReview),
			want: []Action{{
				Kind: ActStart, Runs: []store.NodeRun{startOf("refine@1/fix", "fix", "plan", 1)},
				Trigger: &store.NodeTrigger{
					ID:    "ledger:" + store.NodeRunID(decideEpic, "refine@1/fix", 1, "ledger"),
					Cause: kinds.LedgerCause(emptyReview, 1),
				},
			}},
		},
		{
			name: "fix in round 2 that starts while correcting writes the ledger trigger with that round's open findings",
			flow: flow,
			rows: ledgerCorrecting(rowsOf(nil, nil, planDone, running(loopOf(2, 0))), openReview),
			want: []Action{{
				Kind: ActStart, Runs: []store.NodeRun{startOf("refine@2/fix", "fix", "plan", 2)},
				Trigger: &store.NodeTrigger{
					ID:    "ledger:" + store.NodeRunID(decideEpic, "refine@2/fix", 2, "ledger"),
					Cause: kinds.LedgerCause(openReview, 2),
				},
			}},
		},
		{
			name: "fix in a loop body that starts while accepted keeps the started trigger",
			flow: flow,
			rows: rowsOf(nil, nil, planDone, running(loopOf(1, 0))),
			want: []Action{{
				Kind: ActStart, Runs: []store.NodeRun{startOf("refine@1/fix", "fix", "plan", 1)},
				Trigger: &store.NodeTrigger{
					ID:    "workflow:" + store.NodeRunID(decideEpic, "refine@1/fix", 1, "trigger"),
					Cause: "workflow node refine@1/fix started",
				},
			}},
		},
		{
			name: "first plan run that starts while correcting has no trigger",
			flow: []workflow.Node{ledgerLoop()},
			rows: ledgerCorrecting(rowsOf(nil, nil, running(loopOf(1, 0))), ledgerOpen()),
			want: []Action{{Kind: ActStart, Runs: []store.NodeRun{startOf("refine@1/fix", "fix", "plan", 1)}}},
		},
	})
}

func TestDecideLedgerTriggerIsStable(t *testing.T) {
	t.Parallel()
	rows := ledgerCorrecting(
		rowsOf(nil, nil, ended(startOf("plan", "plan", "plan", 0), "complete"), running(loopOf(1, 0))),
		ledgerOpen(),
	)
	flow := workflow.Workflow{Name: "decide", Nodes: []workflow.Node{planNode(), ledgerLoop()}}
	first := Decide(flow, rows, nil)
	second := Decide(flow, rows, nil)
	wantID := "ledger:" + store.NodeRunID(decideEpic, "refine@1/fix", 1, "ledger")
	if len(first) != 1 || first[0].Trigger == nil || first[0].Trigger.ID != wantID {
		t.Fatalf("Decide() = %+v, want one start whose trigger id is %s", first, wantID)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("second Decide() = %+v, want the first %+v", second, first)
	}
}
