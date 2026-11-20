package enginehost

import (
	"slices"
	"testing"
	"time"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// nodesWorkflowFile is a repository workflow named nodes. It holds a node of every kind a node run records: a plan that
// holds a build, a loop that holds a gate, a command, an evidence node and an agent.
const nodesWorkflowFile = `name: nodes
nodes:
  - id: plan
    kind: plan
    body:
      - id: build
        kind: build
  - id: fix
    loop:
      max: 3
      until: check.passed
    body:
      - id: check
        kind: gate
        at: head
        gate: check
  - id: run
    kind: command
    at: head
    argv: [scripts/test, -race]
    limit: 60
  - id: evidence
    kind: evidence
    at: head
  - id: review
    kind: agent
    at: head
    agent: reviewer
    prompt: Review the change.
    schema: {type: object}
`

// nodesRevision is the revision every node run of the nodes epic prepares at.
const nodesRevision = "synthetic-revision"

// createNodesEpic creates epicServiceID through client, pinned to nodesWorkflowFile committed at the base of a new
// repository. It fails the test on error.
func createNodesEpic(t *testing.T, st *store.Store, client enginev1connect.EpicsClient) {
	t.Helper()
	repo := stepGitTestRepo(t)
	stepGitTestCommit(t, repo, map[string]string{".orrery/workflows/nodes.yaml": nodesWorkflowFile})
	req := fullEpicRequest(repo, epicServiceID)
	req.Workflow = "nodes"
	createEpicOK(t, st, client, req)
}

// planTwoTurns plans two turns of epicServiceID with plannedReturn: turn 1 plans tasks 1 and 2, and turn 2, claimed
// after a trigger is added, plans tasks 3 and 4. It fails the test on error.
func planTwoTurns(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := t.Context()
	first, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn of first turn: %v", err)
	}
	if err = st.CompleteTurn(ctx, epicServiceID, first.Seq, plannedReturn(), time.Unix(turnCompleted, 0)); err != nil {
		t.Fatalf("CompleteTurn of first turn: %v", err)
	}
	trigger := store.NodeTrigger{ID: "synthetic-trigger-two", Cause: "synthetic cause"}
	if err = st.StartNodes(ctx, epicServiceID, nil, &trigger, time.Unix(turnRestarted, 0)); err != nil {
		t.Fatalf("StartNodes with trigger: %v", err)
	}
	second, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnRestarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn of second turn: %v", err)
	}
	if err = st.CompleteTurn(ctx, epicServiceID, second.Seq, plannedReturn(), time.Unix(turnFreed, 0)); err != nil {
		t.Fatalf("CompleteTurn of second turn: %v", err)
	}
}

// startNodesRuns starts the node runs of the nodes epic at attempt 1, each with an id of its own. They are listed out of
// the workflow's order and out of path order, so the reply must put them in order.
func startNodesRuns(t *testing.T, st *store.Store) {
	t.Helper()
	runs := []store.NodeRun{
		{Path: "review", ID: "run-review", Node: "review", Kind: "agent", Attempt: 1},
		{Path: "evidence", ID: "run-evidence", Node: "evidence", Kind: "evidence", Attempt: 1},
		{Path: "run", ID: "run-run", Node: "run", Kind: "command", Attempt: 1},
		{Path: "fix/2/check", ID: "run-check-2", Node: "check", Kind: "gate", Round: 2, Attempt: 1},
		{Path: "fix/1/check", ID: "run-check-1", Node: "check", Kind: "gate", Round: 1, Attempt: 1},
		{Path: "fix", ID: "run-fix", Node: "fix", Kind: "loop", LoopRound: 2, Attempt: 1},
		{Path: "plan/3", ID: "run-build", Node: "build", Kind: "build", Task: 3, Attempt: 1},
		{Path: "plan", ID: "run-plan", Node: "plan", Kind: "plan", Mark: 1, Attempt: 1},
	}
	if err := st.StartNodes(t.Context(), epicServiceID, runs, nil, time.Unix(turnStarted, 0)); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
}

// TestEpicGetNodeRunsReturnTheRowsOfEachKind checks that GetEpic lists every node run of the pinned workflow in the
// workflow's preorder then path, with the row of each kind beneath it, and that a run's fields come from its stored
// rows only.
func TestEpicGetNodeRunsReturnTheRowsOfEachKind(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createNodesEpic(t, st, client)
	planTwoTurns(t, st)
	startNodesRuns(t, st)
	at := time.Unix(turnFreed, 0)

	for _, path := range []string{"review", "evidence", "run", "fix/2/check", "fix/1/check", "fix", "plan/3", "plan"} {
		if err := st.PrepareNode(ctx, epicServiceID, path, 1, nodesRevision, ""); err != nil {
			t.Fatalf("PrepareNode %s: %v", path, err)
		}
	}

	// The gate at round 2 has a check; the gate at round 1 has none, so its check is unset.
	checkID := store.NodeWorkID("run-check-2", 1, "check")
	if err := st.AddNodeCheck(ctx, store.NodeCheck{
		ID: checkID, Epic: epicServiceID, Path: "fix/2/check", Revision: nodesRevision,
		Argv: []string{"scripts/test", "-race"}, Limit: 60,
	}, at); err != nil {
		t.Fatalf("AddNodeCheck: %v", err)
	}
	if err := st.EndNodeCheck(ctx, checkID, store.NodeCheckEnd{
		Exit: 3, TimedOut: true, Stdout: "synthetic stdout", Stderr: "synthetic stderr",
	}, at); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}
	const notRun = "could not start: no such file"
	if err := st.AddNodeCheck(ctx, store.NodeCheck{
		ID: store.NodeWorkID("run-run", 1, "check"), Epic: epicServiceID, Path: "run", Revision: nodesRevision,
		Argv: []string{"scripts/test", "-race"}, Limit: 60, NotRun: notRun,
	}, at); err != nil {
		t.Fatalf("AddNodeCheck not run: %v", err)
	}

	// The evidence node's run is the one for attempt 1 and Requirement R1; the run for R2 at attempt 2 is not its. Each
	// run's spec is the item its Requirement stores, which fullEpicRequest sets to epicTestItem.
	for _, run := range []store.EvidenceRun{
		{ID: store.NodeWorkID("run-evidence", 1, "R1"), Epic: epicServiceID, Item: "R1", Revision: nodesRevision,
			Phase: "red", Source: "captured", Spec: evidenceItemOf(epicTestItem()), Created: at},
		{ID: store.NodeWorkID("run-evidence", 2, "R2"), Epic: epicServiceID, Item: "R2", Revision: nodesRevision,
			Phase: "red", Source: "captured", Spec: evidenceItemOf(epicTestItem()), Created: at},
	} {
		if err := st.AddEvidenceRun(ctx, run); err != nil {
			t.Fatalf("AddEvidenceRun %s: %v", run.ID, err)
		}
	}

	stepID := store.NodeWorkID("run-review", 1, "step")
	if err := st.ApplyEngine(ctx, []store.EngineOp{
		store.StepAdd{ID: stepID, Agent: "reviewer", Prompt: "Review the change.", Schema: "{}"},
	}, at); err != nil {
		t.Fatalf("ApplyEngine StepAdd: %v", err)
	}

	if err := st.EndNodes(ctx, epicServiceID, []store.NodeEnd{
		{Path: "fix/1/check", Outcome: "failed", Output: "synthetic failure"},
		{Path: "fix/2/check", Outcome: "failed", Output: "exit 3"},
		{Path: "run", Outcome: "failed", Output: notRun},
		{Path: "evidence", Outcome: "passed", Output: "R1 red: passed"},
		{Path: "review", Outcome: "passed", Output: "done: ok"},
	}, at); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}

	want := []*v1.EpicNode{
		{Path: "plan", Node: "plan", Kind: "plan", State: "running", Attempt: 1, Revision: nodesRevision,
			Turns: []int64{2}, Tasks: []int64{3, 4}},
		{Path: "plan/3", Node: "build", Kind: "build", State: "running", Attempt: 1, Revision: nodesRevision,
			Tasks: []int64{3}},
		{Path: "fix", Node: "fix", Kind: "loop", State: "running", Attempt: 1, Revision: nodesRevision, LoopRound: 2},
		{Path: "fix/1/check", Node: "check", Kind: "gate", State: "ended", Outcome: "failed", Round: 1, Attempt: 1,
			Revision: nodesRevision, Output: "synthetic failure"},
		{Path: "fix/2/check", Node: "check", Kind: "gate", State: "ended", Outcome: "failed", Round: 2, Attempt: 1,
			Revision: nodesRevision, Output: "exit 3", Check: &v1.EpicNodeCheck{
				State: "ended", Exit: 3, TimedOut: true, Stdout: "synthetic stdout", Stderr: "synthetic stderr",
			}},
		{Path: "run", Node: "run", Kind: "command", State: "ended", Outcome: "failed", Attempt: 1,
			Revision: nodesRevision, Output: notRun, Check: &v1.EpicNodeCheck{State: "ended", NotRun: notRun}},
		{Path: "evidence", Node: "evidence", Kind: "evidence", State: "ended", Outcome: "passed", Attempt: 1,
			Revision: nodesRevision, Output: "R1 red: passed",
			Runs: []string{store.NodeWorkID("run-evidence", 1, "R1")}},
		{Path: "review", Node: "review", Kind: "agent", State: "ended", Outcome: "passed", Attempt: 1,
			Revision: nodesRevision, Output: "done: ok", Step: stepID},
	}
	wantProtos(t, "GetEpic nodes", getEpicOK(t, client).GetNodes(), want)
}

// TestEpicGetWithNoNodeRunsHasNoNodes checks that an epic with no node runs returns no nodes.
func TestEpicGetWithNoNodeRunsHasNoNodes(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	wantProtos(t, "GetEpic nodes", getEpicOK(t, client).GetNodes(), []*v1.EpicNode{})
}

// TestEpicGetNodeRunOfNodeOutsidePinnedWorkflowComesLast checks that GetEpic still shows a node run whose node the
// pinned workflow does not hold, after the runs of the nodes it holds.
func TestEpicGetNodeRunOfNodeOutsidePinnedWorkflowComesLast(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createNodesEpic(t, st, client)
	runs := []store.NodeRun{
		{Path: "ghost", ID: "run-ghost", Node: "ghost", Kind: "gate", Attempt: 1},
		{Path: "review", ID: "run-review", Node: "review", Kind: "agent", Attempt: 1},
	}
	if err := st.StartNodes(ctx, epicServiceID, runs, nil, time.Unix(turnStarted, 0)); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}

	nodes := getEpicOK(t, client).GetNodes()
	paths := make([]string, 0, len(nodes))
	for _, node := range nodes {
		paths = append(paths, node.GetPath())
	}
	if !slices.Equal(paths, []string{"review", "ghost"}) {
		t.Fatalf("GetEpic node paths = %q, want review, then the run outside the workflow", paths)
	}
}

// TestEpicGetUnpinnedEpicOrdersNodesByTheBuiltInWorkflow checks that an epic that pins no workflow text orders its node
// runs by the built-in epic workflow, which holds plan above build.
func TestEpicGetUnpinnedEpicOrdersNodesByTheBuiltInWorkflow(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	req := fullEpicRequest(stepGitTestRepo(t), epicServiceID)
	row := epicRowOf(
		req.Contract,
		req.Decisions,
		req.Contract.GetBase(),
		store.EpicWorkflow{},
		time.Unix(turnStarted, 0),
	)
	if err := st.CreateEpic(ctx, row); err != nil {
		t.Fatalf("CreateEpic unpinned: %v", err)
	}
	runs := []store.NodeRun{
		{Path: "a", ID: "run-a", Node: "build", Kind: "build", Task: 1, Attempt: 1},
		{Path: "plan", ID: "run-plan", Node: "plan", Kind: "plan", Attempt: 1},
	}
	if err := st.StartNodes(ctx, epicServiceID, runs, nil, time.Unix(turnStarted, 0)); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}

	want := []*v1.EpicNode{
		{Path: "plan", Node: "plan", Kind: "plan", State: "running", Attempt: 1},
		{Path: "a", Node: "build", Kind: "build", State: "running", Attempt: 1, Tasks: []int64{1}},
	}
	wantProtos(t, "GetEpic nodes", getEpicOK(t, client).GetNodes(), want)
}

// TestEpicGetEvidenceNodeListsTheRerunThatDecides checks that an evidence node lists, for a Requirement whose first run
// exited store.EnvironmentExit, the rerun that decides it and not the first run, as kinds.Evidence decides it.
func TestEpicGetEvidenceNodeListsTheRerunThatDecides(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createNodesEpic(t, st, client)
	at := time.Unix(turnStarted, 0)
	run := store.NodeRun{Path: "evidence", ID: "run-evidence", Node: "evidence", Kind: "evidence", Attempt: 1}
	if err := st.StartNodes(ctx, epicServiceID, []store.NodeRun{run}, nil, at); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	first := store.NodeWorkID("run-evidence", 1, "R1")
	rerun := store.NodeWorkID("run-evidence", 1, "R1 rerun")
	for _, id := range []string{first, rerun} {
		if err := st.AddEvidenceRun(ctx, store.EvidenceRun{
			ID: id, Epic: epicServiceID, Item: "R1", Revision: nodesRevision, Phase: "red", Source: "captured",
			Spec: evidenceItemOf(epicTestItem()), Created: at,
		}); err != nil {
			t.Fatalf("AddEvidenceRun %s: %v", id, err)
		}
	}
	if err := st.RecordEvidenceCapture(ctx, first, store.EvidenceCapture{
		Exit: store.EnvironmentExit, NotCaptured: store.NotCapturedEnvironment, At: at,
	}); err != nil {
		t.Fatalf("RecordEvidenceCapture %s: %v", first, err)
	}

	var evidence *v1.EpicNode
	for _, node := range getEpicOK(t, client).GetNodes() {
		if node.GetPath() == "evidence" {
			evidence = node
		}
	}
	if evidence == nil {
		t.Fatalf("GetEpic nodes have no evidence node")
	}
	if got := evidence.GetRuns(); !slices.Equal(got, []string{rerun}) {
		t.Errorf("evidence node runs = %v, want only the deciding rerun %q", got, rerun)
	}
}
