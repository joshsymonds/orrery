package interpret

import (
	"context"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// The paths of the earlier-turn cases: the build child a reopened task of turn 1 starts under the running fix plan,
// and that plan's settle walk of the task.
const (
	earlierTurnChild = "fix/rebuild/T1"
	earlierTurnBatch = "fix/S1/reintegrate"
)

// earlierTurnFlow is the flow of the earlier-turn cases: a plan that builds and integrates its tasks, then a fix plan
// that builds and integrates its own, as the built-in epic's correction round does.
func earlierTurnFlow() workflow.Workflow {
	return workflow.Workflow{Name: "decide", Nodes: []workflow.Node{
		kindNode("plan", "plan", kindNode("build", "build"), batchNode()),
		kindNode("fix", "plan", kindNode("rebuild", "build"), kindNode("reintegrate", "integrate")),
	}}
}

// earlierTurnRows returns the rows of a fix plan started after turn 1, so with mark 1, while the first plan has ended:
// turn 1 planned T1 and T2, which the first plan built and integrated, and turn 2, the fix plan's, sent T1 back. first
// is T1 as given, and tasks and runs are the tasks and node runs beyond those.
func earlierTurnRows(first store.EpicTask, tasks []store.EpicTask, runs ...store.NodeRun) kinds.Rows {
	turns := []store.EpicTurn{turnAt(1, "complete"), turnAt(2, "work")}
	all := make([]store.NodeRun, 0, 5+len(runs))
	all = append(all,
		ended(startOf("plan", "plan", "plan", 0), "complete"),
		ended(childOf("plan/build/T1", 0, 1, 1, 1), ""),
		ended(childOf("plan/build/T2", 0, 2, 1, 2), ""),
		ended(attempted(startOf("plan/S2/integrate", "integrate", "integrate", 0), 1), outcomePassed),
		withMark(running(startOf("fix", "fix", "plan", 0)), 1),
	)
	return rowsOf(turns, append([]store.EpicTask{first, claimed(2, "plan/S2/integrate")}, tasks...),
		append(all, runs...)...)
}

// startsChild reports whether acts start a build child run at path for task.
func startsChild(acts []Action, path string, task int64) bool {
	return slices.ContainsFunc(acts, func(act Action) bool {
		return act.Kind == ActStart && slices.ContainsFunc(act.Runs, func(run store.NodeRun) bool {
			return run.Path == path && run.Kind == kindBuild && run.Task == task
		})
	})
}

// TestSendBackEarlierTurnTaskBuildsUnderTheRunningPlan checks that a task of a turn at or below the running plan's mark,
// which a send_back reopened, starts a build child under that plan, though the plan run that built it first has ended.
func TestSendBackEarlierTurnTaskBuildsUnderTheRunningPlan(t *testing.T) {
	t.Parallel()
	acts := Decide(earlierTurnFlow(), earlierTurnRows(reopened(taskOpen, 1), nil), integrateKinds(kinds.Integrate{}))
	if !startsChild(acts, earlierTurnChild, 1) {
		t.Errorf("with T1 of turn 1 reopened under the fix plan, Decide = %+v, want its build child %s started", acts,
			earlierTurnChild)
	}
}

// TestSendBackEarlierTurnTaskIntegratesOnceDoneAgain checks that the fix plan integrates the earlier-turn task it
// rebuilt: no walk starts while the task builds, and once it is done again the plan's settle walk at the task's seq
// starts and its integrate run prepares the batch of the task.
func TestSendBackEarlierTurnTaskIntegratesOnceDoneAgain(t *testing.T) {
	t.Parallel()
	reg := integrateKinds(kinds.Integrate{})
	building := earlierTurnRows(reopened(taskOpen, 1), nil, running(childOf(earlierTurnChild, 0, 1, 1, 1)))
	if acts := Decide(earlierTurnFlow(), building, reg); startsIntegrate(acts) {
		t.Errorf("with T1 building under the fix plan, Decide = %+v, want no integrate run started", acts)
	}

	rebuilt := ended(childOf(earlierTurnChild, 0, 1, 1, 1), "")
	acts := Decide(earlierTurnFlow(), earlierTurnRows(reopened(taskDone, 1), nil, rebuilt), reg)
	if !startsAt(acts, earlierTurnBatch) {
		t.Errorf("with T1 done again under the fix plan, Decide = %+v, want the walk at %s started", acts,
			earlierTurnBatch)
	}
	walk := attempted(running(startOf(earlierTurnBatch, "reintegrate", "integrate", 0)), 1)
	acts = Decide(earlierTurnFlow(), earlierTurnRows(reopened(taskDone, 1), nil, rebuilt, walk), reg)
	if !slices.ContainsFunc(acts, func(act Action) bool {
		return act.Kind == ActWork && act.Work == kinds.ActPrepare && act.Path == earlierTurnBatch &&
			slices.Equal(act.Tasks, []int64{1})
	}) {
		t.Errorf("Decide = %+v, want the walk's integrate run to prepare its batch of T1", acts)
	}
}

// TestSendBackEarlierTurnUntouchedTaskIsNotRestarted checks that the fix plan takes only the earlier-turn tasks a
// send_back reopened: an earlier-turn task at generation 0 starts no build child, open or done, and a done one that no
// batch claimed starts no walk; and a reopened task the fix plan did not rebuild starts no walk either.
func TestSendBackEarlierTurnUntouchedTaskIsNotRestarted(t *testing.T) {
	t.Parallel()
	reg := integrateKinds(kinds.Integrate{})
	untouched := []store.EpicTask{openTask(3, 1), doneTask(4, 1)}
	acts := Decide(earlierTurnFlow(), earlierTurnRows(reopened(taskOpen, 1), untouched), reg)
	for _, seq := range []int64{2, 3, 4} {
		if slices.ContainsFunc(acts, func(act Action) bool {
			return (act.Kind == ActStart || act.Kind == ActRebuild) &&
				(act.Task == seq || slices.ContainsFunc(act.Runs, func(run store.NodeRun) bool { return run.Task == seq }))
		}) {
			t.Errorf("Decide = %+v, want no build of untouched T%d of turn 1", acts, seq)
		}
	}

	done := earlierTurnRows(doneTask(1, 1), []store.EpicTask{doneTask(4, 1)})
	if acts = Decide(earlierTurnFlow(), done, reg); startsIntegrate(acts) {
		t.Errorf("with no task of turn 1 reopened, Decide = %+v, want no integrate run started", acts)
	}
	elsewhere := earlierTurnRows(reopened(taskDone, 1), nil)
	if acts = Decide(earlierTurnFlow(), elsewhere, reg); startsIntegrate(acts) {
		t.Errorf("with T1 reopened but not rebuilt under the fix plan, Decide = %+v, want no integrate run started",
			acts)
	}
}

// passIn carries one pass out on the test epic with flow, and fails the test when the pass logged an error.
func (w *integrateWorld) passIn(flow workflow.Workflow) {
	w.t.Helper()
	w.runner.passFlow(context.Background(), epicOf(w.t, w.s), flow)
	if w.logs.Len() > 0 {
		w.t.Fatalf("pass logged an error: %s", w.logs.String())
	}
}

// TestSendBackEarlierTurnTaskIsRebuiltAndIntegratedByTheFixPlan runs the earlier-turn shape against the store and a
// repository: the first plan builds, integrates and ends built with T1 and T2, and the fix plan starts after turn 2.
// The planner's turn under the fix plan sends T1 back. The fix plan starts T1's build child, and once T1 is accepted
// again on the stored base its settle walk integrates the correction and advances the branch.
func TestSendBackEarlierTurnTaskIsRebuiltAndIntegratedByTheFixPlan(t *testing.T) {
	t.Parallel()
	flow := earlierTurnFlow()
	w := newIntegrateWorld(t)
	first := w.commit(w.base, "internal/a/x.go", "one\n")
	second := w.commit(w.base, "b.go", "two\n")
	w.passIn(flow)
	completeTurn(t, w.s, runPlan("complete"))
	w.passIn(flow)
	w.accept(1, w.base, first)
	w.accept(2, w.base, second)
	w.passIn(flow)
	w.endGate(integrateBatch, 0, "")
	w.passIn(flow)
	w.endCapture(integrateBatch)
	w.passIn(flow)
	landed := w.requireAdvanced(integrateBatch)
	completeTurn(t, w.s, store.EpicPlan{Decisions: []string{}, Outline: "built", Outcome: "complete"})
	w.passIn(flow)
	w.passIn(flow)
	if run := runAt(t, w.s, "fix"); run.State != store.NodeRunning || run.Mark != 2 {
		t.Fatalf("fix plan = %+v, want running with mark 2", run)
	}

	w.carryOut(w.actionTurn(store.EpicActionInput{Kind: store.ActionSendBack, Task: 1, Reason: "x.go is wrong"}))
	w.passIn(flow)
	if run := runAt(t, w.s, earlierTurnChild); run.State != store.NodeRunning || run.Task != 1 {
		t.Fatalf("T1's build child under the fix plan = %+v, want running", run)
	}
	if w.hasRunAt(earlierTurnBatch) {
		t.Fatalf("a batch started at %s while T1 is open", earlierTurnBatch)
	}

	corrected := w.commit(landed, "internal/a/x.go", "one corrected\n")
	w.reaccept(1, landed, corrected)
	w.passIn(flow)
	w.endGate(earlierTurnBatch, 0, "")
	w.passIn(flow)
	w.endCapture(earlierTurnBatch)
	w.passIn(flow)
	next := w.requireAdvanced(earlierTurnBatch)
	for path, text := range map[string]string{"internal/a/x.go": "one corrected", "b.go": "two"} {
		if got := w.file(next, path); got != text {
			t.Errorf("%s on the branch = %q, want %q", path, got, text)
		}
	}
	if run := runAt(t, w.s, "plan/build/T1"); run.State != store.NodeEnded {
		t.Errorf("T1's first build child = %+v, want it kept ended", run)
	}
}

// TestSendBackEarlierTurnLoopRoundWithAReopenedTaskMovesOn checks that a round of the built-in correct loop, which
// names no max, whose fix plan planned no task of its own turn but rebuilt a reopened task of an earlier turn, counts
// as a round that planned a task: with its closure not passed, the loop moves on to its next round.
func TestSendBackEarlierTurnLoopRoundWithAReopenedTaskMovesOn(t *testing.T) {
	t.Parallel()
	turns := []store.EpicTurn{turnAt(1, "complete"), turnAt(2, "work")}
	rebuilt := ended(childOf("correct@2/fix/rebuild/T1", 2, 1, 1, 1), "")
	decideCases(t, []decideCase{
		{
			name: "round 2 whose fix plan rebuilt a reopened task of turn 1 moves on to round 3",
			flow: []workflow.Node{builtInCorrect(t)},
			rows: rowsOf(turns, []store.EpicTask{reopened(taskDone, 1)},
				correctLoop(2), correctFix(2), rebuilt, correctClosure(2)),
			want: []Action{{Kind: ActRound, Path: "correct", Round: 2}},
		},
	})
}
