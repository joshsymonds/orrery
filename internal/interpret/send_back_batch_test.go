package interpret

import (
	"context"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// The paths of the send_back batch cases: the settle walk of turn 1's tasks T1 to T3, whose integrate ended failed,
// and the late walk of T1 at the reopen generation its send_back moved it to.
const (
	sendBackSettleBatch = "plan/S3/integrate"
	sendBackFirstBatch  = "plan/L1g1/integrate"
)

// sendBackBatchFlow is the flow of the send_back batch cases: a plan whose body builds each task and then integrates.
func sendBackBatchFlow() workflow.Workflow {
	return workflow.Workflow{Name: "decide", Nodes: planWithChecks(batchNode())}
}

// reopened returns task 1 of turn 1 at reopen generation generation, which a send_back reopened that many times, with
// status status and no batch, as a send_back leaves it.
func reopened(status string, generation int64) store.EpicTask {
	return store.EpicTask{Seq: 1, Turn: 1, Status: status, Generation: generation}
}

// failedSettle returns the settle walk's integrate run, which ended failed at its second attempt on candidate
// 58cd4c0855ec, as task-branches' plan/S3/integrate did.
func failedSettle() store.NodeRun {
	run := ended(attempted(startOf(sendBackSettleBatch, "integrate", "integrate", 0), 2), outcomeFailed)
	run.Revision = "58cd4c0855ec"
	return run
}

// sendBackRows returns the rows of the task-branches shape: turn 1 planned T1 to T3, its settle walk's integrate ended
// failed with T2 and T3 claimed, and turn 2 sent T1 back, which is first as given. runs are the node runs beyond the
// plan, the settle walk's integrate and the ended build runs of T2 and T3.
func sendBackRows(first store.EpicTask, runs ...store.NodeRun) kinds.Rows {
	turns := []store.EpicTurn{turnAt(1, "complete"), turnAt(2, "work")}
	tasks := []store.EpicTask{first, claimed(2, sendBackSettleBatch), claimed(3, sendBackSettleBatch)}
	all := make([]store.NodeRun, 0, 4+len(runs))
	all = append(all, runningPlan(0), failedSettle(),
		ended(childOf("plan/build/T2", 0, 2, 1, 2), outcomePassed),
		ended(childOf("plan/build/T3", 0, 3, 1, 3), outcomePassed),
	)
	return rowsOf(turns, tasks, append(all, runs...)...)
}

// startsIntegrate reports whether acts start any integrate run.
func startsIntegrate(acts []Action) bool {
	return slices.ContainsFunc(acts, func(act Action) bool {
		return act.Kind == ActStart && slices.ContainsFunc(act.Runs, func(run store.NodeRun) bool {
			return run.Kind == kindIntegrate
		})
	})
}

// TestSendBackBatchIntegratesTheReopenedTaskOnceItIsDoneAgain reproduces task-branches: the settle walk's integrate
// ended failed, a send_back reopened T1 and cleared its claim, and T1 is done again. The settle walk at S3 has runs, so
// no settle walk starts, yet a late walk of T1's reopen generation starts on its own, with no planner turn, and its
// integrate run prepares the batch of T1.
func TestSendBackBatchIntegratesTheReopenedTaskOnceItIsDoneAgain(t *testing.T) {
	t.Parallel()
	reg := integrateKinds(kinds.Integrate{})
	rebuilt := ended(childOf("plan/build/T1", 0, 1, 1, 1), outcomePassed)
	rows := sendBackRows(reopened(taskDone, 1), rebuilt)
	acts := Decide(sendBackBatchFlow(), rows, reg)
	if !startsAt(acts, sendBackFirstBatch) {
		t.Errorf("with T1 done again after its send_back, Decide = %+v, want the late walk at %s started", acts,
			sendBackFirstBatch)
	}
	if endsPlan(acts) {
		t.Errorf("Decide = %+v, want the plan kept running while its tasks are not integrated", acts)
	}

	walk := attempted(running(startOf(sendBackFirstBatch, "integrate", "integrate", 0)), 1)
	acts = Decide(sendBackBatchFlow(), sendBackRows(reopened(taskDone, 1), rebuilt, walk), reg)
	if !slices.ContainsFunc(acts, func(act Action) bool {
		return act.Kind == ActWork && act.Work == kinds.ActPrepare && act.Path == sendBackFirstBatch &&
			slices.Equal(act.Tasks, []int64{1})
	}) {
		t.Errorf("Decide = %+v, want the late walk's integrate run to prepare its batch of T1", acts)
	}
}

// TestSendBackBatchStartsNothingWhileTheReopenedTaskIsOpen checks that no walk starts while the task a send_back
// reopened is open: neither before its build run is rebuilt nor while it builds.
func TestSendBackBatchStartsNothingWhileTheReopenedTaskIsOpen(t *testing.T) {
	t.Parallel()
	reg := integrateKinds(kinds.Integrate{})
	cases := []struct {
		name  string
		build store.NodeRun
	}{
		{name: "its build run has not been rebuilt", build: ended(childOf("plan/build/T1", 0, 1, 1, 1), outcomePassed)},
		{name: "it builds", build: running(childOf("plan/build/T1", 0, 1, 1, 1))},
	}
	for _, tc := range cases {
		acts := Decide(sendBackBatchFlow(), sendBackRows(reopened(taskOpen, 1), tc.build), reg)
		if startsIntegrate(acts) {
			t.Errorf("%s: Decide = %+v, want no integrate run started while T1 is open", tc.name, acts)
		}
		if endsPlan(acts) {
			t.Errorf("%s: Decide = %+v, want the plan kept running while T1 is open", tc.name, acts)
		}
	}
}

// TestSendBackBatchWalksEachReopenGenerationOnce checks that a reopen generation's late walk starts once: a walk of T1's
// first generation that ended at a failed check without claiming T1 starts no other until a second send_back moves T1
// to its next generation, whose walk then starts once T1 is done again.
func TestSendBackBatchWalksEachReopenGenerationOnce(t *testing.T) {
	t.Parallel()
	nodes := planWithChecks(kindNode("check", "gate"), batchNode())
	flow := workflow.Workflow{Name: "decide", Nodes: nodes}
	reg := map[string]kinds.Kind{
		"plan": kinds.Plan{}, "build": kinds.Build{}, "integrate": kinds.Integrate{}, "gate": scripted{},
	}
	rebuilt := ended(childOf("plan/build/T1", 0, 1, 1, 1), outcomePassed)
	checked := ended(startOf("plan/S3/check", "check", "gate", 0), outcomePassed)
	walked := ended(startOf("plan/L1g1/check", "check", "gate", 0), outcomeFailed)

	acts := Decide(flow, sendBackRows(reopened(taskDone, 1), rebuilt, checked, walked), reg)
	if slices.ContainsFunc(acts, func(act Action) bool { return act.Kind == ActStart }) {
		t.Errorf("with generation 1's walk ended, Decide = %+v, want no walk started", acts)
	}

	acts = Decide(flow, sendBackRows(reopened(taskDone, 2), rebuilt, checked, walked), reg)
	if !startsAt(acts, "plan/L1g2/check") {
		t.Errorf("with T1 done again at generation 2, Decide = %+v, want the late walk under %s started", acts,
			"plan/L1g2/")
	}
}

// TestSendBackBatchIntegratesEveryTaskOfTheFailedBatch runs the task-branches shape against the store and a
// repository: T1 and T2 are accepted and their settle walk's batch at S2 fails its Done gate. The planner sends T1 back,
// which clears its claim and the candidate, and T1 is accepted again with its correction. The next pass starts the late
// walk of T1's reopen generation, whose batch composes T1 and T2, the task the failed batch stranded, on the stored
// base and claims both. Once green it advances the branch with both tasks' work.
func TestSendBackBatchIntegratesEveryTaskOfTheFailedBatch(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	w.toGate()
	w.endGate(integrateBatch, 1, "lint failed")
	w.pass()
	if run := runAt(t, w.s, integrateBatch); run.State != store.NodeEnded || run.Outcome != outcomeFailed {
		t.Fatalf("settle batch = %+v, want ended failed", run)
	}
	w.carryOut(w.actionTurn(store.EpicActionInput{Kind: store.ActionSendBack, Task: 1, Reason: "x.go is wrong"}))
	w.pass()
	if w.hasRunAt(sendBackFirstBatch) {
		t.Fatalf("a batch started at %s while T1 is open", sendBackFirstBatch)
	}

	corrected := w.commit(w.base, "internal/a/x.go", "one corrected\n")
	w.reaccept(1, w.base, corrected)
	w.pass()
	if !w.hasRunAt(sendBackFirstBatch) {
		t.Fatalf("no batch at %s once T1 is done again; runs %+v", sendBackFirstBatch, runsOf(t, w.s))
	}
	batches := map[int64]string{}
	for _, task := range epicOf(t, w.s).Tasks {
		batches[task.Seq] = task.Batch
	}
	if batches[1] != sendBackFirstBatch || batches[2] != sendBackFirstBatch {
		t.Errorf("task batches = %v, want T1 and T2 claimed by %s", batches, sendBackFirstBatch)
	}
	w.green(sendBackFirstBatch)
	landed := w.requireAdvanced(sendBackFirstBatch)
	for path, text := range map[string]string{"internal/a/x.go": "one corrected", "b.go": "two"} {
		if got := w.file(landed, path); got != text {
			t.Errorf("%s on the branch = %q, want %q", path, got, text)
		}
	}
}

// reaccept stores the build of task seq, which a send_back reopened, as built on base at revision, and accepts the
// task again.
func (w *integrateWorld) reaccept(seq int64, base, revision string) {
	w.t.Helper()
	ctx := context.Background()
	var from store.TaskBuild
	for _, task := range epicOf(w.t, w.s).Tasks {
		if task.Seq == seq {
			from = task.Build
		}
	}
	built := store.TaskBuild{Stage: "green", Base: base, Revision: revision}
	if err := w.s.SetTaskBuild(ctx, runEpic, seq, from, built); err != nil {
		w.t.Fatalf("SetTaskBuild %d: %v", seq, err)
	}
	if err := w.s.AcceptTask(ctx, runEpic, seq, built, "reviewed"); err != nil {
		w.t.Fatalf("AcceptTask %d: %v", seq, err)
	}
}
