package interpret

import (
	"context"
	"database/sql"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// The batches of a built-in world whose plan integrates two turns: the first turn's batch passes, and the second's
// fails its Done gate.
const (
	firstBuiltBatch  = "plan/S1/integrate"
	secondBuiltBatch = "plan/S2/integrate"
)

// toFailedSecondBatch runs a world on the built-in epic until its second batch has failed its Done gate. Red is judged
// fails at base, its cost check at base ends, and the plan's first task is built, accepted and integrated green, so R1
// is green at the stored base. A second turn plans one more task, which is built and accepted on that stored base, and
// its batch's Done gate fails. It returns the world and the second batch's candidate.
func toFailedSecondBatch(t *testing.T) (*integrateWorld, string) {
	t.Helper()
	w := newIntegrateWorld(t, pinnedTo(builtinPin(t)), viaRunnerPass())
	w.pass()
	w.endRed()
	w.pass()
	endCost(t, w.s)
	w.pass()
	completeTurn(t, w.s, store.EpicPlan{
		Decisions: []string{}, Tasks: []store.EpicTaskInput{runTask("only", "b.go")}, Outline: "one task",
		Outcome: "complete",
	})
	w.pass()
	w.accept(1, w.base, w.commit(w.base, "b.go", "one\n"))
	w.pass()
	stored := w.candidate(firstBuiltBatch)
	w.endGate(firstBuiltBatch, 0, "")
	w.pass()
	w.endCapture(firstBuiltBatch)
	w.pass()
	if epic := epicOf(t, w.s); epic.StoredBase != stored {
		t.Fatalf("stored base = %q after the first batch, want its candidate %s", epic.StoredBase, stored)
	}

	completeTurn(t, w.s, store.EpicPlan{
		Decisions: []string{}, Tasks: []store.EpicTaskInput{runTask("next", "c.go")}, Outline: "one more task",
		Outcome: "work",
	})
	w.pass()
	w.accept(2, stored, w.commit(stored, "c.go", "two\n"))
	w.pass()
	candidate := w.candidate(secondBuiltBatch)
	w.endGate(secondBuiltBatch, 1, "lint failed")
	w.pass()
	if run := runAt(t, w.s, secondBuiltBatch); run.State != store.NodeEnded || run.Outcome != outcomeFailed {
		t.Fatalf("second batch = %+v, want ended failed", run)
	}
	return w, candidate
}

// greenBatch passes the Done gate and the capture of the running batch at path, a pass apart, and passes once more so
// the batch checks its refs and advances.
func (w *integrateWorld) greenBatch(path string) {
	w.t.Helper()
	w.endGate(path, 0, "")
	w.pass()
	w.endCapture(path)
	w.pass()
}

// requireRerunGate fails unless the last check the world ran is the Done gate of the batch at path at attempt 1, at
// candidate.
func (w *integrateWorld) requireRerunGate(path, candidate string) {
	w.t.Helper()
	run := runAt(w.t, w.s, path)
	if run.State != store.NodeRunning || run.Attempt != 1 || run.Revision != candidate {
		w.t.Fatalf("batch = %+v, want running at attempt 1 on the candidate %s", run, candidate)
	}
	calls := w.fakes.checks.calls
	if len(calls) == 0 {
		w.t.Fatal("no check ran, want the rerun's Done gate")
	}
	if got := calls[len(calls)-1]; got.ID != workID(w.t, w.s, path, "done 1") || got.Revision != candidate {
		w.t.Fatalf("last check = %+v, want the rerun's Done gate at the candidate %s", got, candidate)
	}
}

// requirePassedRerun fails unless the batch at path ended passed at attempt 1, the epic's branch and stored base are at
// candidate, the epic has no candidate, and the pending triggers grew by one from pending.
func (w *integrateWorld) requirePassedRerun(path, candidate string, pending int) {
	w.t.Helper()
	if run := runAt(w.t, w.s, path); run.State != store.NodeEnded || run.Outcome != outcomePassed || run.Attempt != 1 {
		w.t.Errorf("batch = %+v, want ended passed at attempt 1", run)
	}
	if epic := epicOf(w.t, w.s); epic.StoredBase != candidate || epic.Candidate != "" {
		w.t.Errorf("stored base %q and candidate %q, want %q and none", epic.StoredBase, epic.Candidate, candidate)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != candidate {
		w.t.Errorf("branch = %s, want the candidate %s", tip, candidate)
	}
	if got := w.pending(); got != pending+1 {
		w.t.Errorf("pending triggers = %d, want %d: the passed rerun wakes the planner once", got, pending+1)
	}
}

// TestPlanWaitsForTheRetryItsCompleteTurnReturns: the planner's turn after a failed batch returns complete beside a
// retry_node of the integrate node. With R1 green at the stored base, the plan would end built at once, and the retry
// would then set the batch running under an ended plan, where nothing decides it. The plan waits while the action is
// pending instead. Once it is carried out, the batch reruns under its running plan, passes and advances the branch, and
// the next complete turn ends the plan built.
func TestPlanWaitsForTheRetryItsCompleteTurnReturns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w, candidate := toFailedSecondBatch(t)
	completeTurn(t, w.s, store.EpicPlan{
		Decisions: []string{}, Outline: "the gate failed on load alone; retry the batch", Outcome: "complete",
		Actions: []store.EpicActionInput{{
			Kind: store.ActionRetryNode, Node: "integrate", Reason: "the lint gate failed on load alone",
		}},
	})
	actions, err := w.s.PendingActions(ctx)
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("pending actions = %+v, want the turn's retry", actions)
	}

	w.pass()
	if plan := runAt(t, w.s, "plan"); plan.State != store.NodeRunning {
		t.Fatalf("plan = %+v while the turn's retry is pending, want running", plan)
	}
	w.carryOut(actions[0])
	w.pass()
	w.requireRerunGate(secondBuiltBatch, candidate)
	pending := w.pending()
	w.greenBatch(secondBuiltBatch)
	w.requirePassedRerun(secondBuiltBatch, candidate, pending)
	if plan := runAt(t, w.s, "plan"); plan.State != store.NodeRunning {
		t.Errorf("plan = %+v once the rerun passed, want running until the planner's next turn", plan)
	}

	completeTurn(t, w.s, store.EpicPlan{Decisions: []string{}, Outline: "the batch is green", Outcome: "complete"})
	w.pass()
	if plan := runAt(t, w.s, "plan"); plan.State != store.NodeEnded || plan.Outcome != "complete" {
		t.Errorf("plan = %+v, want ended complete", plan)
	}
	if got := epicOf(t, w.s).Phase; got != "built" {
		t.Errorf("epic phase = %q, want built", got)
	}
}

// toOrphanedRerun runs toFailedSecondBatch, ends the plan built on a complete turn, since R1 is green at the stored
// base, and then reruns the failed batch by the owner's retry of the integrate node, so the batch runs under the plan
// that ended. It returns the world and the second batch's candidate.
func toOrphanedRerun(t *testing.T) (*integrateWorld, string) {
	t.Helper()
	w, candidate := toFailedSecondBatch(t)
	completeTurn(t, w.s, store.EpicPlan{Decisions: []string{}, Outline: "R1 is green", Outcome: "complete"})
	w.pass()
	if plan := runAt(t, w.s, "plan"); plan.State != store.NodeEnded || plan.Outcome != "complete" {
		t.Fatalf("plan = %+v, want ended complete", plan)
	}
	if err := w.s.RetryNode(context.Background(), runEpic, "integrate"); err != nil {
		t.Fatalf("RetryNode of the failed batch: %v", err)
	}
	return w, candidate
}

// TestOrphanedBatchRerunIsDrivenToItsEnd: a batch rerun under its ended plan, the plan's walk passed, runs its Done gate
// and capture at its candidate under its new attempt, and advances the epic's branch, as its plan would have driven it.
// The review that runs after the plan holds while it runs.
func TestOrphanedBatchRerunIsDrivenToItsEnd(t *testing.T) {
	t.Parallel()
	w, candidate := toOrphanedRerun(t)
	review := runAt(t, w.s, "review")

	w.pass()
	w.requireRerunGate(secondBuiltBatch, candidate)
	pending := w.pending()
	w.greenBatch(secondBuiltBatch)
	w.requirePassedRerun(secondBuiltBatch, candidate, pending)
	if plan := runAt(t, w.s, "plan"); plan.State != store.NodeEnded || plan.Outcome != "complete" {
		t.Errorf("plan = %+v, want it kept ended complete", plan)
	}
	if got := runAt(t, w.s, "review"); got.State != review.State || got.Attempt != review.Attempt {
		t.Errorf("review = %+v, want it kept as %+v", got, review)
	}
}

// TestOrphanedBatchRerunAtTheLandedStoredBaseEndsGreenThere: the owner lands the failed batch's candidate by hand, as
// the epic's branch and stored base with no candidate, after its rerun was set running under the ended plan. The rerun
// runs its Done gate and capture there and ends passed without moving the branch, so R1 is green at the stored base
// and nothing is left unbuilt.
func TestOrphanedBatchRerunAtTheLandedStoredBaseEndsGreenThere(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w, candidate := toOrphanedRerun(t)
	w.git("update-ref", integrateBranch, candidate)
	db, err := sql.Open("sqlite", w.path)
	if err != nil {
		t.Fatalf("open %s: %v", w.path, err)
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `UPDATE epics SET stored_base = ?, candidate = '' WHERE id = ?`, candidate,
		runEpic); err != nil {
		t.Fatalf("land the candidate as the stored base: %v", err)
	}

	w.pass()
	w.requireRerunGate(secondBuiltBatch, candidate)
	pending := w.pending()
	w.greenBatch(secondBuiltBatch)
	w.requirePassedRerun(secondBuiltBatch, candidate, pending)
	read, err := w.s.EpicFlow(ctx, runEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	rows := flowRows(read)
	if green := kinds.GreenAt(rows, candidate); !green["R1"] {
		t.Errorf("green at the stored base %s = %v, want R1", candidate, green)
	}
	if unbuilt := kinds.Unbuilt(rows); len(unbuilt) != 0 {
		t.Errorf("unbuilt = %q, want none", unbuilt)
	}
}
