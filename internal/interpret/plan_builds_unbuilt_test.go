package interpret

import (
	"context"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// unbuiltTask returns the task a turn plans while no plan runs, which claims no Requirement of the test epic.
func unbuiltTask() store.EpicTaskInput {
	task := runTask("the budget lines are added", "README")
	task.Requirements = []string{}
	return task
}

// unbuiltPlan returns a planner return with outcome and tasks.
func unbuiltPlan(outcome string, tasks ...store.EpicTaskInput) store.EpicPlan {
	return store.EpicPlan{Decisions: []string{}, Tasks: tasks, Outline: "the budget lines", Outcome: outcome}
}

// requireNoBuildOfT1 fails unless the test epic in s has no build node run of T1, under any plan run.
func requireNoBuildOfT1(t *testing.T, s *store.Store) {
	t.Helper()
	if runs := runsOf(t, s); slices.ContainsFunc(runs, func(run store.NodeRun) bool {
		return run.Kind == kindBuild && run.Task == 1
	}) {
		t.Fatalf("node runs = %+v, want no build of T1 while no plan runs", runs)
	}
}

// requireUnbuiltBuilt fails unless the plan run at plan runs with its mark below turn, the turn that planned T1 while
// no plan ran, and T1's build child runs under it at build.
func requireUnbuiltBuilt(t *testing.T, s *store.Store, plan, build string, turn int64) {
	t.Helper()
	if run := runAt(t, s, plan); run.State != store.NodeRunning || run.Kind != kindPlan || run.Mark != turn-1 {
		t.Errorf("plan run = %+v, want it running with mark %d, below the turn that planned T1", run, turn-1)
	}
	if run := runAt(t, s, build); run.State != store.NodeRunning || run.Kind != kindBuild || run.Task != 1 {
		t.Errorf("build child = %+v, want T1's build running under %s", run, plan)
	}
}

// kickUnbuilt kicks the test epic in s, as its owner does while no plan runs, and completes the turn the kick wakes
// with T1. It returns that turn's seq.
func kickUnbuilt(t *testing.T, s *store.Store) int64 {
	t.Helper()
	ctx := context.Background()
	owner := store.Maker{By: store.MakerOwner}
	if err := s.KickEpic(ctx, runEpic, "add the budget lines", owner, runClock()); err != nil {
		t.Fatalf("KickEpic: %v", err)
	}
	turn, err := s.ClaimTurn(ctx, runEpic, runClock())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = s.CompleteTurn(ctx, runEpic, turn.Seq, unbuiltPlan("work", unbuiltTask()), runClock()); err != nil {
		t.Fatalf("CompleteTurn %d: %v", turn.Seq, err)
	}
	return turn.Seq
}

// TestPlanBuildsUnbuiltReplan: a turn the planner takes while the epic's release is stopped plans T1, and no plan run
// runs to store its build child. The plan the release's replan starts is marked below that turn, so once the planner's
// turn for the replan returns work with no task, T1's build child runs under the replan's plan.
func TestPlanBuildsUnbuiltReplan(t *testing.T) {
	t.Parallel()
	t.Run("a release its integrator stopped, resumed by its owner, whose integrator then replans", func(t *testing.T) {
		t.Parallel()
		w := newReplanWorld(t)
		w.pass()
		w.blockWithoutPaths()
		if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
			t.Fatalf("epic phase = %q, want stopped by its integrator", phase)
		}
		turn, _ := w.claimReplanTurn()
		w.completeReplanTurn(turn, "work", unbuiltTask())
		requireNoBuildOfT1(t, w.s)

		owner := store.Maker{By: store.MakerOwner}
		if err := w.s.ReleaseEpic(t.Context(), runEpic, nil, owner, runClock()); err != nil {
			t.Fatalf("ReleaseEpic: %v", err)
		}
		w.pass()
		w.block("candidate")
		if phase := w.flowOf(runEpic).Epic.Phase; phase != "correcting" {
			t.Fatalf("epic phase = %q, want correcting: the integrator that blocked at attempt 1 replans", phase)
		}
		replan, _ := w.claimReplanTurn()
		w.completeReplanTurn(replan, "work")
		requireUnbuiltBuilt(t, w.s, "release/A1/plan", "release/A1/plan/build/T1", turn.Seq)
	})
	t.Run("a release its integrator stopped, which the owner's retry replans", func(t *testing.T) {
		t.Parallel()
		w := newReplanWorld(t)
		w.pass()
		w.blockWithoutPaths()
		if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
			t.Fatalf("epic phase = %q, want stopped by its integrator", phase)
		}
		turn, _ := w.claimReplanTurn()
		w.completeReplanTurn(turn, "work", unbuiltTask())
		requireNoBuildOfT1(t, w.s)

		if err := w.s.RetryEpic(t.Context(), runEpic, store.Maker{By: store.MakerOwner}, runClock()); err != nil {
			t.Fatalf("RetryEpic: %v", err)
		}
		w.pass()
		replan, _ := w.claimReplanTurn()
		w.completeReplanTurn(replan, "work")
		requireUnbuiltBuilt(t, w.s, replanPlanPath, replanBuildPath, turn.Seq)
	})
}

// TestPlanBuildsUnbuiltGapRound: once the gap round test epic's plan has ended, a turn the owner's kick wakes plans T1
// while the review runs and no plan does. The plan the conclusion's gap round starts is marked below that turn, so
// once the gap's turn returns work with no task, T1's build child runs under the gap round's plan.
func TestPlanBuildsUnbuiltGapRound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	runner := gapRoundRunner(s)
	row := store.EpicRow{ID: runEpic}
	runner.passFlow(ctx, row, gapRoundFlow())
	completeTurn(t, s, gapRoundPlanned())
	runner.passFlow(ctx, row, gapRoundFlow())
	if run := runAt(t, s, reviewNodeKind); run.State != store.NodeRunning {
		t.Fatalf("review run = %+v, want it running once the plan ended", run)
	}

	turn := kickUnbuilt(t, s)
	runner.passFlow(ctx, row, gapRoundFlow())
	requireNoBuildOfT1(t, s)
	runnerFreezeReview(t, s)
	runner.passFlow(ctx, row, gapRoundFlow())
	completeTurn(t, s, unbuiltPlan("work"))
	runner.passFlow(ctx, row, gapRoundFlow())
	requireUnbuiltBuilt(t, s, "conclude/G1/fix", "conclude/G1/fix/build/T1", turn)
}

// unbuiltDecideFlow is the flow of the decide pass case: a plan that builds and integrates its tasks, the review, then
// a fix plan that builds and integrates its own, which the decide pass starts once the review ends.
func unbuiltDecideFlow() workflow.Workflow {
	return workflow.Workflow{Name: "unbuilt", Nodes: []workflow.Node{
		kindNode("plan", "plan", kindNode("build", "build"), kindNode("integrate", "integrate")),
		kindNode(reviewNodeKind, reviewNodeKind),
		kindNode("fix", "plan", kindNode("rebuild", "build"), kindNode("reintegrate", "integrate")),
	}}
}

// TestPlanBuildsUnbuiltDecidePass: once the first plan has ended, a turn the owner's kick wakes plans T1 while the
// review runs and no plan does. The fix plan the decide pass starts after the review is marked below that turn, so
// once its turn returns work with no task, T1's build child runs under the fix plan.
func TestPlanBuildsUnbuiltDecidePass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	runner := gapRoundRunner(s)
	row := store.EpicRow{ID: runEpic}
	runner.passFlow(ctx, row, unbuiltDecideFlow())
	completeTurn(t, s, gapRoundPlanned())
	runner.passFlow(ctx, row, unbuiltDecideFlow())
	if run := runAt(t, s, "plan"); run.State != store.NodeEnded {
		t.Fatalf("plan run = %+v, want it ended", run)
	}

	turn := kickUnbuilt(t, s)
	runner.passFlow(ctx, row, unbuiltDecideFlow())
	requireNoBuildOfT1(t, s)
	runnerFreezeReview(t, s)
	runner.passFlow(ctx, row, unbuiltDecideFlow())
	completeTurn(t, s, unbuiltPlan("work"))
	runner.passFlow(ctx, row, unbuiltDecideFlow())
	requireUnbuiltBuilt(t, s, "fix", "fix/rebuild/T1", turn)
}
