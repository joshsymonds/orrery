package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// childEpic is the epic the build child tests complete turns on.
const childEpic = "orrery-5c6d7e8/epic-children"

// childStartedAt is the second the tests start plan runs at, childClaimedAt the second they claim a turn at, and
// childCompletedAt the second they complete a turn at.
func childStartedAt() time.Time {
	return time.Unix(1700002000, 0)
}

func childClaimedAt() time.Time {
	return time.Unix(1700002050, 0)
}

func childCompletedAt() time.Time {
	return time.Unix(1700002100, 0)
}

// childEndedAt is the second the tests end a plan run at.
func childEndedAt() time.Time {
	return time.Unix(1700002080, 0)
}

// openChildStore opens a migrated store file, closed when the test ends.
func openChildStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

// childWorkflow returns the workflow name with nodes in preorder, its text and sha256 derived from its name.
func childWorkflow(name string, nodes ...store.EpicWorkflowNode) store.EpicWorkflow {
	text := "name: " + name + "\n"
	sum := sha256.Sum256([]byte(text))
	return store.EpicWorkflow{Name: name, Text: text, SHA256: hex.EncodeToString(sum[:]), Nodes: nodes}
}

// planAndBuild is the workflow whose plan holds build, the shape of the built-in epic.
func planAndBuild() store.EpicWorkflow {
	return childWorkflow("epic",
		store.EpicWorkflowNode{ID: "plan", Parent: "", Kind: "plan"},
		store.EpicWorkflowNode{ID: "build", Parent: "plan", Kind: "build"},
	)
}

// createChildEpic stores childEpic pinning workflow.
func createChildEpic(t *testing.T, s *store.Store, workflow store.EpicWorkflow) {
	t.Helper()
	created := time.Unix(1700000000, 0)
	row := store.EpicRow{
		ID: childEpic, Repository: "/work/synthetic-repo", Base: "main", Title: "Synthetic epic",
		Approved: "Approved sentence", Intent: "Store the build children", Care: "serious",
		Approach: "Compare and set", ReleaseThen: "Then line", Checks: "just check", Phase: "accepted",
		Created: created, Changed: created, Workflow: workflow,
	}
	if err := s.CreateEpic(context.Background(), row); err != nil {
		t.Fatalf("CreateEpic %s: %v", childEpic, err)
	}
}

// childPlan is a valid planner return of tasks tasks, each with a file, a Requirement and a gate.
func childPlan(tasks int) store.EpicPlan {
	plan := store.EpicPlan{Outline: "Plan the tasks", Outcome: "work"}
	for i := range tasks {
		plan.Tasks = append(plan.Tasks, store.EpicTaskInput{
			Title: fmt.Sprintf("Task %d", i+1), Deliverable: "Deliverable", Files: []string{"a.go"},
			Requirements: []string{"R1"}, Gate: "check",
		})
	}
	return plan
}

// startPlanRun starts a running plan run at path of round on childEpic, with the id of its start, and returns the
// run as the store holds it.
func startPlanRun(t *testing.T, s *store.Store, path string, round int64) store.NodeRun {
	t.Helper()
	run := store.NodeRun{
		Path: path, ID: store.NodeRunID(childEpic, path, round, "start"), Node: "plan", Kind: "plan", Round: round,
	}
	if err := s.StartNodes(context.Background(), childEpic, []store.NodeRun{run}, nil, childStartedAt()); err != nil {
		t.Fatalf("StartNodes of %s: %v", path, err)
	}
	run.State, run.Started = store.NodeRunning, childStartedAt()
	return run
}

// completeChildTurn claims the next turn of childEpic and completes it with plan.
func completeChildTurn(t *testing.T, s *store.Store, plan store.EpicPlan) {
	t.Helper()
	ctx := context.Background()
	turn, err := s.ClaimTurn(ctx, childEpic, childClaimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn of %s: %v", childEpic, err)
	}
	if err = s.CompleteTurn(ctx, childEpic, turn.Seq, plan, childCompletedAt()); err != nil {
		t.Fatalf("CompleteTurn %d of %s: %v", turn.Seq, childEpic, err)
	}
}

// buildChildOf returns the build child run CompleteTurn stores for the task at position in turn, the task's seq being
// seq, under the plan run at plan of round: its path, its id by the turn's cause, and its start at completion.
func buildChildOf(plan string, round, turn, position, seq int64) store.NodeRun {
	path := fmt.Sprintf("%s/build/T%d", plan, seq)
	return store.NodeRun{
		Path: path, ID: store.NodeRunID(childEpic, path, round, fmt.Sprintf("turn %d task %d", turn, position)),
		Node: "build", Kind: "build", Task: seq, Round: round, State: store.NodeRunning, Started: childCompletedAt(),
	}
}

// assertNodeRuns checks that the node runs of childEpic are want, in path order.
func assertNodeRuns(t *testing.T, s *store.Store, want []store.NodeRun) {
	t.Helper()
	got, err := s.NodeRuns(context.Background(), childEpic)
	if err != nil {
		t.Fatalf("NodeRuns of %s: %v", childEpic, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NodeRuns of %s = %+v, want %+v", childEpic, got, want)
	}
}

// assertTurnStored checks that the first turn of childEpic succeeded and planned its two tasks.
func assertTurnStored(t *testing.T, s *store.Store) {
	t.Helper()
	row, err := s.Epic(context.Background(), childEpic)
	if err != nil {
		t.Fatalf("Epic %s: %v", childEpic, err)
	}
	if len(row.Turns) != 1 || row.Turns[0].State != "succeeded" {
		t.Errorf("turns of %s = %+v, want one succeeded turn", childEpic, row.Turns)
	}
	if len(row.Tasks) != 2 {
		t.Errorf("tasks of %s = %d, want 2", childEpic, len(row.Tasks))
	}
}

func TestCompleteTurnAddsBuildChildPerTaskUnderRunningPlan(t *testing.T) {
	t.Parallel()
	s := openChildStore(t)
	createChildEpic(t, s, planAndBuild())
	plan := startPlanRun(t, s, "plan", 0)

	completeChildTurn(t, s, childPlan(2))

	want := []store.NodeRun{
		plan,
		{
			Path: "plan/build/T1", ID: store.NodeRunID(childEpic, "plan/build/T1", 0, "turn 1 task 1"),
			Node: "build", Kind: "build", Task: 1, State: store.NodeRunning, Started: childCompletedAt(),
		},
		{
			Path: "plan/build/T2", ID: store.NodeRunID(childEpic, "plan/build/T2", 0, "turn 1 task 2"),
			Node: "build", Kind: "build", Task: 2, State: store.NodeRunning, Started: childCompletedAt(),
		},
	}
	assertNodeRuns(t, s, want)
	assertTurnStored(t, s)
}

func TestCompleteTurnChildrenOfLoopedPlanKeepItsRound(t *testing.T) {
	t.Parallel()
	s := openChildStore(t)
	createChildEpic(t, s, childWorkflow("looped",
		store.EpicWorkflowNode{ID: "rounds", Parent: "", Kind: "loop"},
		store.EpicWorkflowNode{ID: "plan", Parent: "rounds", Kind: "plan"},
		store.EpicWorkflowNode{ID: "build", Parent: "plan", Kind: "build"},
	))
	plan := startPlanRun(t, s, "rounds@2/plan", 2)

	completeChildTurn(t, s, childPlan(2))

	want := []store.NodeRun{
		plan,
		buildChildOf("rounds@2/plan", 2, 1, 1, 1),
		buildChildOf("rounds@2/plan", 2, 1, 2, 2),
	}
	assertNodeRuns(t, s, want)
}

func TestCompleteTurnNumbersChildPositionsWithinTheTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openChildStore(t)
	createChildEpic(t, s, planAndBuild())
	plan := startPlanRun(t, s, "plan", 0)
	completeChildTurn(t, s, childPlan(2))

	if err := s.StartNodes(ctx, childEpic, nil, &store.NodeTrigger{ID: "later", Cause: "later plan trigger"},
		childStartedAt()); err != nil {
		t.Fatalf("StartNodes trigger of %s: %v", childEpic, err)
	}
	completeChildTurn(t, s, childPlan(1))

	want := []store.NodeRun{
		plan,
		buildChildOf("plan", 0, 1, 1, 1),
		buildChildOf("plan", 0, 1, 2, 2),
		buildChildOf("plan", 0, 2, 1, 3),
	}
	assertNodeRuns(t, s, want)
}

func TestCompleteTurnWithoutRunningPlanRunAddsNoChildren(t *testing.T) {
	t.Parallel()
	t.Run("no plan run", func(t *testing.T) {
		t.Parallel()
		s := openChildStore(t)
		createChildEpic(t, s, planAndBuild())

		completeChildTurn(t, s, childPlan(2))

		assertNodeRuns(t, s, nil)
		assertTurnStored(t, s)
	})
	t.Run("ended plan run", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openChildStore(t)
		createChildEpic(t, s, planAndBuild())
		plan := startPlanRun(t, s, "plan", 0)
		if err := s.EndNodes(ctx, childEpic, []store.NodeEnd{{Path: "plan", Outcome: "complete"}},
			childEndedAt()); err != nil {
			t.Fatalf("EndNodes of %s: %v", childEpic, err)
		}

		completeChildTurn(t, s, childPlan(2))

		plan.State, plan.Outcome, plan.Ended = store.NodeEnded, "complete", childEndedAt()
		assertNodeRuns(t, s, []store.NodeRun{plan})
		assertTurnStored(t, s)
	})
}

func TestCompleteTurnWithoutBuildUnderPlanAddsNoChildren(t *testing.T) {
	t.Parallel()
	s := openChildStore(t)
	createChildEpic(t, s, childWorkflow("plan-only", store.EpicWorkflowNode{ID: "plan", Parent: "", Kind: "plan"}))
	plan := startPlanRun(t, s, "plan", 0)

	completeChildTurn(t, s, childPlan(2))

	assertNodeRuns(t, s, []store.NodeRun{plan})
	assertTurnStored(t, s)
}
