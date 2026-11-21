package interpret

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// mergeTask returns task seq of the test epic, as stored.
func mergeTask(t *testing.T, s *store.Store, seq int64) store.EpicTask {
	t.Helper()
	epic := epicOf(t, s)
	i := slices.IndexFunc(epic.Tasks, func(task store.EpicTask) bool { return task.Seq == seq })
	if i < 0 {
		t.Fatalf("task %d of the test epic is not stored", seq)
	}
	return epic.Tasks[i]
}

// requireSentBack fails unless task seq is open on implement with no batch, built on the epic's candidate after the
// end, or on the stored base when there is none, failed for reason, and its newest version is a send_back with no
// decision and that reason, as a task a batch left out for a merge conflict is sent back.
func (w *integrateWorld) requireSentBack(seq int64, reason string) {
	w.t.Helper()
	epic := epicOf(w.t, w.s)
	base := epic.Candidate
	if base == "" {
		base = epic.StoredBase
	}
	task := mergeTask(w.t, w.s, seq)
	if task.Status != taskOpen || task.Build.Stage != "implement" || task.Batch != "" {
		w.t.Errorf("task %d = status %q, stage %q, batch %q, want open on implement with no batch",
			seq, task.Status, task.Build.Stage, task.Batch)
	}
	if task.Build.Base != base {
		w.t.Errorf("task %d builds on %q, want the epic's candidate or stored base %q", seq, task.Build.Base, base)
	}
	if task.Build.Failure != reason {
		w.t.Errorf("task %d failure = %q, want %q", seq, task.Build.Failure, reason)
	}
	if len(task.Versions) == 0 {
		w.t.Fatalf("task %d has no versions, want a send_back", seq)
	}
	newest := task.Versions[len(task.Versions)-1]
	if newest.Source != store.SourceSendBack || newest.Decision != "" || newest.Reason != reason {
		w.t.Errorf("task %d newest version = %+v, want a send_back with no decision and reason %q", seq, newest, reason)
	}
}

// TestIntegrateBranchMergeAdvancesBranchOnlyAfterGreenGate: two accepted tasks that change different files compose on
// the stored base. While the batch's Done gate runs the epic's branch stays at the stored base, and it stays there
// while the capture runs after a green gate. Once the capture ends the branch is at the candidate, and each task's
// revision is an ancestor of it.
func TestIntegrateBranchMergeAdvancesBranchOnlyAfterGreenGate(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	first := w.commit(w.base, "internal/a/x.go", "one\n")
	second := w.commit(w.base, "b.go", "two\n")
	w.pass()
	w.planTurn()
	w.accept(1, w.base, first)
	w.accept(2, w.base, second)
	w.pass()
	candidate := w.candidate(integrateBatch)

	if tip := w.git("rev-parse", integrateBranch); tip != w.base {
		t.Errorf("branch while the gate runs = %s, want the stored base %s", tip, w.base)
	}
	w.endGate(integrateBatch, 0, "")
	w.pass()
	if tip := w.git("rev-parse", integrateBranch); tip != w.base {
		t.Errorf("branch while the capture runs = %s, want the stored base %s", tip, w.base)
	}
	w.endCapture(integrateBatch)
	w.pass()

	if run := runAt(t, w.s, integrateBatch); run.State != store.NodeEnded || run.Outcome != outcomePassed {
		t.Errorf("batch = %+v, want ended passed", run)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != candidate {
		t.Errorf("branch = %s, want the candidate %s", tip, candidate)
	}
	if !w.holds(candidate, first) || !w.holds(candidate, second) {
		t.Errorf("candidate %s, want it to hold both tasks' revisions %s and %s", candidate, first, second)
	}
}

// TestIntegrateBranchMergeFailedBatchLeavesBranch: a batch whose Done gate fails ends failed, and the epic's branch and
// stored base stay at the stored base.
func TestIntegrateBranchMergeFailedBatchLeavesBranch(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	w.toGate()
	w.endGate(integrateBatch, 1, "lint failed")
	w.pass()

	if run := runAt(t, w.s, integrateBatch); run.State != store.NodeEnded || run.Outcome != outcomeFailed {
		t.Errorf("batch = %+v, want ended failed", run)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != w.base {
		t.Errorf("branch = %s, want it left at the stored base %s", tip, w.base)
	}
	if got := epicOf(t, w.s).StoredBase; got != w.base {
		t.Errorf("stored base = %s, want it left at %s", got, w.base)
	}
}

// TestIntegrateBranchMergeSendsConflictedTaskBackWithNoPlannerAction: two accepted tasks change the same line of b.go,
// so the second task's merge conflicts and it is left out, and the batch passes. The batch's end sends the task back
// with no planner turn: the first task is in the stored base, and the second is open on implement, built on the
// epic's candidate or the stored base, with its send_back.
func TestIntegrateBranchMergeSendsConflictedTaskBackWithNoPlannerAction(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	first := w.commit(w.base, "b.go", "one\n")
	second := w.commit(w.base, "b.go", "two\n")
	w.pass()
	w.planTurn()
	w.accept(1, w.base, first)
	w.accept(2, w.base, second)
	w.pass()
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	turns := len(epicOf(t, w.s).Turns)
	w.pass()

	if run := runAt(t, w.s, integrateBatch); run.State != store.NodeEnded || run.Outcome != outcomePassed {
		t.Fatalf("batch = %+v, want ended passed", run)
	}
	if stored := epicOf(t, w.s).StoredBase; !w.holds(stored, first) {
		t.Errorf("stored base %s does not hold the first task's revision %s", stored, first)
	}
	if got := len(epicOf(t, w.s).Turns); got != turns {
		t.Errorf("planner turns = %d after the batch ended, want %d: a send-back runs no turn", got, turns)
	}
	w.requireSentBack(2, "T2 conflicts with the candidate at b.go")
}

// TestIntegrateBranchMergeFailedBatchSendsConflictBack: the same conflict in a batch whose Done gate fails sends the
// conflicted task back too. The failed batch leaves its candidate on the epic, and the task is built on that candidate.
func TestIntegrateBranchMergeFailedBatchSendsConflictBack(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	first := w.commit(w.base, "b.go", "one\n")
	second := w.commit(w.base, "b.go", "two\n")
	w.pass()
	w.planTurn()
	w.accept(1, w.base, first)
	w.accept(2, w.base, second)
	w.pass()
	candidate := w.candidate(integrateBatch)
	w.endGate(integrateBatch, 1, "lint failed")
	w.pass()

	if run := runAt(t, w.s, integrateBatch); run.State != store.NodeEnded || run.Outcome != outcomeFailed {
		t.Fatalf("batch = %+v, want ended failed", run)
	}
	if got := epicOf(t, w.s).Candidate; got != candidate {
		t.Fatalf("candidate = %q, want the failed batch's candidate %s", got, candidate)
	}
	w.requireSentBack(2, "T2 conflicts with the candidate at b.go")
}
