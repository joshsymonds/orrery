package interpret

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// integrateThirdBatch is the path of the integrate run that batches the two tasks of a third turn: the settle walk of
// task 6.
const integrateThirdBatch = "plan/S6/integrate"

// kick adds a pending trigger to the test epic, as an owner's kick does, so the planner can claim a turn while a batch
// is still running.
func (w *integrateWorld) kick() {
	w.t.Helper()
	if err := w.s.KickEpic(context.Background(), runEpic, "plan more", runClock()); err != nil {
		w.t.Fatalf("KickEpic: %v", err)
	}
}

// green ends the Done gate and the capture of the batch at path, both passing, so the pass after it advances the batch.
func (w *integrateWorld) green(path string) {
	w.t.Helper()
	w.endGate(path, 0, "")
	w.pass()
	w.endCapture(path)
	w.pass()
}

// holds reports whether commit is an ancestor of revision, or revision itself.
func (w *integrateWorld) holds(revision, commit string) bool {
	w.t.Helper()
	ok, err := isAncestor(context.Background(), w.repo, commit, revision)
	if err != nil {
		w.t.Fatalf("isAncestor %s %s: %v", commit, revision, err)
	}
	return ok
}

// file returns the text of path at revision.
func (w *integrateWorld) file(revision, path string) string {
	w.t.Helper()
	return w.git("show", revision+":"+path)
}

// plantCandidate stores candidate as the test epic's candidate directly, as a store written before candidates were
// checked against the stored base could hold one.
func (w *integrateWorld) plantCandidate(candidate string) {
	w.t.Helper()
	db, err := sql.Open("sqlite", w.path)
	if err != nil {
		w.t.Fatalf("open store file: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			w.t.Errorf("close store file: %v", err)
		}
	}()
	if _, err = db.ExecContext(context.Background(), "UPDATE epics SET candidate = ? WHERE id = ?", candidate,
		runEpic); err != nil {
		w.t.Fatalf("plant candidate: %v", err)
	}
}

// overtake ends a batch the test epic did not run as passed at revision and moves the branch there, as a newer batch
// that overtook the epic's candidate does. The stored base becomes revision and the candidate is cleared.
func (w *integrateWorld) overtake(revision string) {
	w.t.Helper()
	ctx := context.Background()
	const overtaking = "plan/S9/integrate"
	if err := w.s.StartNodes(ctx, runEpic, []store.NodeRun{{
		Path: overtaking, ID: "overtaking", Node: "integrate", Kind: kindIntegrate, Attempt: 1,
	}}, nil, runClock()); err != nil {
		w.t.Fatalf("StartNodes: %v", err)
	}
	if err := w.s.PrepareNode(ctx, runEpic, overtaking, 1, revision, ""); err != nil {
		w.t.Fatalf("PrepareNode: %v", err)
	}
	if err := w.s.EndBatch(ctx, runEpic, store.BatchEnd{
		Path: overtaking, Attempt: 1, Outcome: outcomePassed,
	}, nil, runClock()); err != nil {
		w.t.Fatalf("EndBatch: %v", err)
	}
	w.git("update-ref", integrateBranch, revision)
}

// requireAdvanced fails unless the batch at path ended passed and moved the branch and the stored base to its
// candidate, with no candidate left.
func (w *integrateWorld) requireAdvanced(path string) string {
	w.t.Helper()
	next := w.candidate(path)
	if run := runAt(w.t, w.s, path); run.State != store.NodeEnded || run.Outcome != outcomePassed {
		w.t.Errorf("batch %s = %+v, want ended passed", path, run)
	}
	if epic := epicOf(w.t, w.s); epic.StoredBase != next || epic.Candidate != "" || epic.Parked != "" {
		w.t.Errorf("stored base %q, candidate %q, parked %q, want %q, none and not parked", epic.StoredBase,
			epic.Candidate, epic.Parked, next)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != next {
		w.t.Errorf("branch = %s, want the candidate %s", tip, next)
	}
	return next
}

// TestIntegrateBatchFailingAfterANewerBatchPassedKeepsTheCandidateOnTheStoredBase: two batches are in flight at once,
// as a settle walk that starts while an older one still runs makes them, both composed on the stored base. The newer
// batch passes first and moves the stored base. The older batch then fails: the candidate it leaves must build on the
// new stored base and still hold the older batch's work, and the next batch composed on it advances the branch with
// every task's work.
func TestIntegrateBatchFailingAfterANewerBatchPassedKeepsTheCandidateOnTheStoredBase(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	older := w.toGate()

	w.kick()
	third := w.commit(w.base, "c.go", "three\n")
	fourth := w.commit(w.base, "d.go", "four\n")
	w.planTurn()
	w.accept(3, w.base, third)
	w.accept(4, w.base, fourth)
	w.pass()
	newer := w.candidate(integrateNextBatch)
	if !w.holds(newer, w.base) || w.holds(newer, older) {
		t.Fatalf("second batch %s, want it composed on the stored base %s while the first batch %s runs", newer,
			w.base, older)
	}
	w.green(integrateNextBatch)
	if got := epicOf(t, w.s).StoredBase; got != newer {
		t.Fatalf("stored base = %s, want the second batch %s", got, newer)
	}

	w.endGate(integrateBatch, 1, "lint failed")
	w.pass()
	if run := runAt(t, w.s, integrateBatch); run.State != store.NodeEnded || run.Outcome != outcomeFailed {
		t.Fatalf("first batch = %+v, want ended failed", run)
	}
	candidate := epicOf(t, w.s).Candidate
	if candidate == "" || !w.holds(candidate, newer) || !w.holds(candidate, older) {
		t.Fatalf("candidate %q, want one that builds on the stored base %s and holds the failed batch %s",
			candidate, newer, older)
	}

	fifth := w.commit(candidate, "e.go", "five\n")
	sixth := w.commit(candidate, "f.go", "six\n")
	w.planTurn()
	w.accept(5, candidate, fifth)
	w.accept(6, candidate, sixth)
	w.pass()
	w.green(integrateThirdBatch)
	landed := w.requireAdvanced(integrateThirdBatch)
	for path, text := range map[string]string{
		"internal/a/x.go": "one", "b.go": "two", "c.go": "three", "d.go": "four", "e.go": "five", "f.go": "six",
	} {
		if got := w.file(landed, path); got != text {
			t.Errorf("%s on the branch = %q, want %q", path, got, text)
		}
	}
}

// TestIntegrateBatchPassingAfterAnOlderBatchFailedKeepsTheOlderBatchsWork: two batches are in flight at once, both
// composed on the stored base. The older batch fails first and leaves its revision as the candidate. The newer batch,
// which was not composed on that candidate, then passes and moves the stored base: the candidate it leaves joins the
// failed batch's work with the new stored base, so that work is not dropped, and the next batch composed on it
// advances the branch with every task's work.
func TestIntegrateBatchPassingAfterAnOlderBatchFailedKeepsTheOlderBatchsWork(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	older := w.toGate()

	w.kick()
	third := w.commit(w.base, "c.go", "three\n")
	fourth := w.commit(w.base, "d.go", "four\n")
	w.planTurn()
	w.accept(3, w.base, third)
	w.accept(4, w.base, fourth)
	w.pass()
	newer := w.candidate(integrateNextBatch)

	w.endGate(integrateBatch, 1, "lint failed")
	w.pass()
	if got := epicOf(t, w.s).Candidate; got != older {
		t.Fatalf("candidate after the first batch failed = %q, want its revision %s", got, older)
	}
	w.green(integrateNextBatch)
	epic := epicOf(t, w.s)
	if epic.StoredBase != newer {
		t.Fatalf("stored base = %s, want the second batch %s", epic.StoredBase, newer)
	}
	if epic.Candidate == "" || !w.holds(epic.Candidate, newer) || !w.holds(epic.Candidate, older) {
		t.Fatalf("candidate %q, want one that builds on the stored base %s and holds the failed batch %s",
			epic.Candidate, newer, older)
	}

	fifth := w.commit(epic.Candidate, "e.go", "five\n")
	sixth := w.commit(epic.Candidate, "f.go", "six\n")
	w.planTurn()
	w.accept(5, epic.Candidate, fifth)
	w.accept(6, epic.Candidate, sixth)
	w.pass()
	w.green(integrateThirdBatch)
	landed := w.requireAdvanced(integrateThirdBatch)
	for path, text := range map[string]string{
		"internal/a/x.go": "one", "b.go": "two", "c.go": "three", "d.go": "four", "e.go": "five", "f.go": "six",
	} {
		if got := w.file(landed, path); got != text {
			t.Errorf("%s on the branch = %q, want %q", path, got, text)
		}
	}
}

// TestIntegrateComposesOnTheMergeOfAForkedCandidateAndTheStoredBase: an epic holds a candidate that does not build on
// its stored base, as a failed batch on an older base left it. The next batch composes on the merge of the stored base
// into that candidate, so its tasks, built on the candidate, compose, and its candidate builds on the stored base and
// advances the branch with the work of both lines.
func TestIntegrateComposesOnTheMergeOfAForkedCandidateAndTheStoredBase(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	forked := w.toGate()
	w.endGate(integrateBatch, 1, "lint failed")
	w.pass()
	newer := w.commit(w.base, "newer.go", "newer\n")
	w.overtake(newer)
	w.plantCandidate(forked)

	third := w.commit(forked, "c.go", "three\n")
	fourth := w.commit(forked, "d.go", "four\n")
	w.planTurn()
	w.accept(3, forked, third)
	w.accept(4, forked, fourth)
	w.pass()
	next := w.candidate(integrateNextBatch)
	if !w.holds(next, newer) || !w.holds(next, forked) {
		t.Fatalf("batch candidate %s, want one that builds on the stored base %s and the forked candidate %s", next,
			newer, forked)
	}
	if run := runAt(t, w.s, integrateNextBatch); strings.Contains(run.Result, "leftOut\":[{") {
		t.Errorf("batch result = %s, want no task left out", run.Result)
	}
	w.green(integrateNextBatch)
	landed := w.requireAdvanced(integrateNextBatch)
	for path, text := range map[string]string{
		"internal/a/x.go": "one", "b.go": "two", "c.go": "three", "d.go": "four", "newer.go": "newer",
	} {
		if got := w.file(landed, path); got != text {
			t.Errorf("%s on the branch = %q, want %q", path, got, text)
		}
	}
}

// TestIntegrateComposesAForkedCandidatesTasksOnTheStoredBaseWhenTheMergeConflicts: an epic holds a candidate that does
// not build on its stored base, and the stored base changed a path the candidate's work changed too, so the two do not
// merge. The next batch composes on the stored base and composes the failed batch's tasks again: a task whose path the
// stored base did not change composes, and the others are left out with their reasons, which the batch's output gives
// the planner. Its candidate builds on the stored base and advances the branch.
func TestIntegrateComposesAForkedCandidatesTasksOnTheStoredBaseWhenTheMergeConflicts(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	forked := w.toGate()
	w.endGate(integrateBatch, 1, "lint failed")
	w.pass()
	newer := w.commit(w.base, "b.go", "newer\n")
	w.overtake(newer)
	w.plantCandidate(forked)

	third := w.commit(w.base, "c.go", "three\n")
	fourth := w.commit(forked, "d.go", "four\n")
	w.planTurn()
	w.accept(3, w.base, third)
	w.accept(4, forked, fourth)
	w.pass()
	next := w.candidate(integrateNextBatch)
	if !w.holds(next, newer) || w.holds(next, forked) {
		t.Fatalf("batch candidate %s, want one composed on the stored base %s", next, newer)
	}
	w.green(integrateNextBatch)
	landed := w.requireAdvanced(integrateNextBatch)
	for path, text := range map[string]string{"internal/a/x.go": "one", "b.go": "newer", "c.go": "three"} {
		if got := w.file(landed, path); got != text {
			t.Errorf("%s on the branch = %q, want %q", path, got, text)
		}
	}
	output := runAt(t, w.s, integrateNextBatch).Output
	for _, left := range []string{
		"T2 was built on " + w.base + ", not on the batch base " + newer,
		"T4 was built on " + forked + ", not on the batch base " + newer,
	} {
		if !strings.Contains(output, left) {
			t.Errorf("batch output = %q, want it to name %q", output, left)
		}
	}
}
