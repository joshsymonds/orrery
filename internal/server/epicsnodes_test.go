package server

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// nodeStoredEpic returns the stored epic of the node tests, repoa/shared, with its workflow nodes in the order given.
func nodeStoredEpic(nodes ...*enginev1.EpicNode) *enginev1.StoredEpic {
	epic := storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour))
	epic.Nodes = nodes
	return epic
}

// syntheticNodes returns the workflow node runs of the node tests in the engine's order, which is not sorted by path,
// with the EpicNode each run is answered as. The evidence node names run-2a and then run-1a, and does not name run-1b.
func syntheticNodes() ([]*enginev1.EpicNode, []*orreryv1.EpicNode) {
	_, byID := syntheticRuns()
	engine := []*enginev1.EpicNode{
		{
			Path:     "plan",
			Node:     "plan",
			Kind:     "plan",
			State:    "ended",
			Outcome:  "passed",
			Attempt:  1,
			Output:   "synthetic plan output",
			Revision: "synthetic-rev-plan",
			Turns:    []int64{1, 2},
			Tasks:    []int64{1, 2, 3},
		},
		{
			Path: "loop", Node: "review-loop", Kind: "loop", State: "running", Attempt: 1, LoopRound: 2,
		},
		{
			Path: "loop/gate", Node: "gate-check", Kind: "gate", State: "ended", Outcome: "passed", Round: 2,
			Attempt: 1, Output: "synthetic gate output", Revision: "synthetic-rev-gate",
			Check: &enginev1.EpicNodeCheck{
				State: "ended", Exit: 0, Stdout: "synthetic gate stdout", Stderr: "synthetic gate stderr",
			},
		},
		{
			Path: "evidence", Node: "evidence", Kind: "evidence", State: "ended", Outcome: "failed", Attempt: 2,
			Output: "synthetic evidence output", Revision: "synthetic-rev-evidence", Runs: []string{"run-2a", "run-1a"},
		},
		{
			Path: "command", Node: "command-run", Kind: "command", State: "ended", Outcome: "failed", Attempt: 1,
			Check: &enginev1.EpicNodeCheck{
				State: "ended", Exit: 2, TimedOut: true, Stdout: "synthetic out", Stderr: "synthetic err",
			},
		},
		{
			Path: "skipped", Node: "skipped-gate", Kind: "gate", State: "ended", Outcome: "failed", Attempt: 1,
			Check: &enginev1.EpicNodeCheck{State: "ended", NotRun: "synthetic reason"},
		},
		{
			Path: "agent", Node: "agent-review", Kind: "agent", State: "ended", Outcome: "passed", Attempt: 1,
			Output: "synthetic agent output", Step: "synthetic-step-agent",
		},
		{
			Path: "build", Node: "build", Kind: "build", State: "parked", Attempt: 3, Output: "synthetic park reason",
			Revision: "synthetic-rev-build", Tasks: []int64{2},
		},
	}
	want := []*orreryv1.EpicNode{
		{
			Path:     "plan",
			Node:     "plan",
			Kind:     "plan",
			State:    "ended",
			Outcome:  "passed",
			Attempt:  1,
			Output:   "synthetic plan output",
			Revision: "synthetic-rev-plan",
			Turns:    []int64{1, 2},
			Tasks:    []int64{1, 2, 3},
		},
		{
			Path: "loop", Node: "review-loop", Kind: "loop", State: "running", Attempt: 1, LoopRound: 2,
		},
		{
			Path: "loop/gate", Node: "gate-check", Kind: "gate", State: "ended", Outcome: "passed", Round: 2,
			Attempt: 1, Output: "synthetic gate output", Revision: "synthetic-rev-gate",
			Check: &orreryv1.EpicNodeCheck{
				State: "ended", Exit: 0, Stdout: "synthetic gate stdout", Stderr: "synthetic gate stderr",
			},
		},
		{
			Path: "evidence", Node: "evidence", Kind: "evidence", State: "ended", Outcome: "failed", Attempt: 2,
			Output: "synthetic evidence output", Revision: "synthetic-rev-evidence",
			Runs: []*orreryv1.EvidenceRun{byID["run-2a"], byID["run-1a"]},
		},
		{
			Path: "command", Node: "command-run", Kind: "command", State: "ended", Outcome: "failed", Attempt: 1,
			Check: &orreryv1.EpicNodeCheck{
				State: "ended", Exit: 2, TimedOut: true, Stdout: "synthetic out", Stderr: "synthetic err",
			},
		},
		{
			Path: "skipped", Node: "skipped-gate", Kind: "gate", State: "ended", Outcome: "failed", Attempt: 1,
			Check: &orreryv1.EpicNodeCheck{State: "ended", NotRun: "synthetic reason"},
		},
		{
			Path: "agent", Node: "agent-review", Kind: "agent", State: "ended", Outcome: "passed", Attempt: 1,
			Output: "synthetic agent output", Step: "synthetic-step-agent",
		},
		{
			Path: "build", Node: "build", Kind: "build", State: "parked", Attempt: 3, Output: "synthetic park reason",
			Revision: "synthetic-rev-build", Tasks: []int64{2},
		},
	}
	return engine, want
}

func TestEpicsGetCarriesStoredNodesInEngineOrder(t *testing.T) {
	t.Parallel()
	engine, want := syntheticNodes()
	runs, _ := syntheticRuns()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{nodeStoredEpic(engine...)}}
	client := epicEvidenceServe(t, store, &evidenceRunsFake{runs: runs})

	got := epicsGet(t, client, "repoa/shared").GetNodes()
	if len(got) != len(want) {
		t.Fatalf("nodes = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if !proto.Equal(got[i], w) {
			t.Errorf("node %d = %v, want %v", i, got[i], w)
		}
	}
}

func TestEpicsGetEvidenceNodeLeavesOutRunsItDoesNotList(t *testing.T) {
	t.Parallel()
	runs, byID := syntheticRuns()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{nodeStoredEpic(&enginev1.EpicNode{
		Path: "evidence", Node: "evidence", Kind: "evidence", State: "ended", Outcome: "passed", Attempt: 1,
		Runs: []string{"run-1b", "run-missing", "run-2a"},
	})}}
	client := epicEvidenceServe(t, store, &evidenceRunsFake{runs: runs})

	got := epicsGet(t, client, "repoa/shared").GetNodes()
	if len(got) != 1 {
		t.Fatalf("nodes = %d, want 1", len(got))
	}
	want := []*orreryv1.EvidenceRun{byID["run-1b"], byID["run-2a"]}
	if gotRuns := got[0].GetRuns(); len(gotRuns) != len(want) {
		t.Fatalf("runs = %v, want run-1b and run-2a in the node's order", gotRuns)
	}
	for i, w := range want {
		if !proto.Equal(got[0].GetRuns()[i], w) {
			t.Errorf("run %d = %v, want %v", i, got[0].GetRuns()[i], w)
		}
	}
}
