package interpret

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// costFlow is a flow of one cost node at base.
const costFlow = `name: nodes
nodes:
  - id: cost
    kind: cost
    at: base
`

// createCostEpic stores the test epic on repo at base, accepted and pinned to costFlow. Its Done gates are tests, then
// lint, so the first Done gate is tests, which is not the first gate the epic holds.
func createCostEpic(t *testing.T, s *store.Store, repo, base string) {
	t.Helper()
	pinned, err := workflow.Load("nodes", func(string) ([]byte, bool, error) { return []byte(costFlow), true, nil })
	if err != nil {
		t.Fatalf("Load nodes: %v", err)
	}
	if err = s.CreateEpic(context.Background(), store.EpicRow{
		ID: runEpic, Repository: repo, Base: base, Title: "Measure the cost", Phase: runPhase, Created: runClock(),
		Changed: runClock(), DoneGates: []string{"tests", "lint"},
		Gates: []store.EpicGate{
			{
				Name: "lint", Argv: []string{"./bin/lint"}, Limit: 60,
				Needs: []store.EpicGateNeed{{Pool: "lint", Count: 2}},
			},
			{
				Name: "tests", Argv: []string{"./bin/tests", "-v"}, Limit: 120,
				Needs: []store.EpicGateNeed{{Pool: "tests", Count: 1}},
			},
		},
		Workflow: store.EpicWorkflow{
			Name: pinned.Name, Text: pinned.Text, SHA256: pinned.SHA256, Nodes: pinnedNodes(pinned.Workflow.Nodes, ""),
		},
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
}

// endCost ends the running check of the built-in epic's cost run with exit 0, the check of the epic's first Done gate. A
// cost run passes whatever its check exits, so the plan that waits on it starts once this check has ended.
func endCost(t *testing.T, s *store.Store) {
	t.Helper()
	nodeEndCheck(t, s, workID(t, s, "cost", "check"), 0, "")
}

// TestCostCheckRunsFirstDoneGate: a pass over an epic whose workflow is a cost node at base starts that node's check
// with the argv, limit and needs of the epic's first Done gate, at the base the run is prepared at.
func TestCostCheckRunsFirstDoneGate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	s := openRunStore(t)
	createCostEpic(t, s, repo, base)
	fakes := newNodeFakes(s)
	runNodes(s, fakes).Pass(ctx)

	want := []store.NodeCheck{{
		ID: workID(t, s, "cost", "check"), Epic: runEpic, Path: "cost", Revision: base,
		Argv: []string{"./bin/tests", "-v"}, Limit: 120, Needs: []store.EpicGateNeed{{Pool: "tests", Count: 1}},
	}}
	if !reflect.DeepEqual(fakes.checks.calls, want) {
		t.Errorf("check calls = %+v, want %+v", fakes.checks.calls, want)
	}
}
