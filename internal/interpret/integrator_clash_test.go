package interpret

import (
	"fmt"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// clashSentBackReason returns the reason task 2 was sent back for: the reason of its newest version, which must be a
// send_back.
func clashSentBackReason(t *testing.T, s *store.Store) string {
	t.Helper()
	task := mergeTask(t, s, 2)
	if len(task.Versions) == 0 {
		t.Fatal("task 2 has no versions, want a send_back")
	}
	newest := task.Versions[len(task.Versions)-1]
	if newest.Source != store.SourceSendBack {
		t.Fatalf("task 2 newest version = %+v, want a send_back", newest)
	}
	return newest.Reason
}

// requireClashSendBack fails unless task 2 of w is open on implement with no batch, its generation one more than it was
// before the send_back, and its failure is the reason it was sent back for. The reason must hold the candidate's change
// to b.go since the merge base. The batch ended with the epic's stored base at its candidate, so that stored base must
// hold the earlier task's change of b.go, composed in it. Its rounds are kept: each round's record is still stored with
// the verdict and outcome it was given.
func requireClashSendBack(t *testing.T, w roundWorld, reason string) {
	t.Helper()
	task := mergeTask(t, w.s, 2)
	if task.Status != taskOpen || task.Build.Stage != "implement" || task.Batch != "" {
		t.Errorf("task 2 = status %q, stage %q, batch %q, want open on implement with no batch",
			task.Status, task.Build.Stage, task.Batch)
	}
	if task.Generation != 1 {
		t.Errorf("task 2 generation = %d, want 1: a send_back moves the task to its next generation", task.Generation)
	}
	if task.Build.Failure != reason {
		t.Errorf("task 2 failure = %q, want the reason it was sent back for %q", task.Build.Failure, reason)
	}
	if !strings.Contains(reason, "the candidate's change to the conflicted paths since the merge base "+w.base) {
		t.Errorf("reason = %q, want the candidate's change since the merge base %s", reason, w.base)
	}
	if !strings.Contains(reason, "+one") {
		t.Errorf("reason = %q, want the candidate's change to b.go, which adds one", reason)
	}
	stored := epicOf(t, w.s).StoredBase
	if got := w.file(stored, "b.go"); got != "one" {
		t.Errorf("b.go on the stored base %s = %q, want the earlier task's %q composed", stored, got, "one")
	}
}

// TestIntegratorClashSendsTheTaskBackWithItsExplanationAndTheDiff: a clash return names the paths it conflicted at and
// its explanation, and the batch goes on at its candidate. The task is sent back to a new generation with the clash's
// explanation and the candidate's change to b.go since the merge base, and the earlier task stays composed.
func TestIntegratorClashSendsTheTaskBackWithItsExplanationAndTheDiff(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	endTaskStep(t, w.s, w.roundID(1), "done", "done", taskReturn(verdictClash, "", "both sides decide the title"))
	w.pass()
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	w.pass()

	reason := clashSentBackReason(t, w.s)
	if !strings.Contains(reason, "task integrator round 1 clashed: both sides decide the title") {
		t.Errorf("reason = %q, want the clash's explanation", reason)
	}
	requireClashSendBack(t, w, reason)
	if got := w.roundRecord(1); got.Verdict != verdictClash || got.Explanation != "both sides decide the title" {
		t.Errorf("round 1 = %+v, want the clash kept with its explanation", got)
	}
}

// TestIntegratorClashThirdFailedRoundSendsTheTaskBack: three rounds whose commits fail their Done gate leave the task out,
// and the batch goes on at its candidate. The task is sent back with the third round's failure, the candidate's change to
// b.go since the merge base, and the three rounds' records kept.
func TestIntegratorClashThirdFailedRoundSendsTheTaskBack(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	third := ""
	for round := 1; round <= 3; round++ {
		head := w.mergeRound(w.roundStart(round), fmt.Sprintf("round %d\n", round))
		endTaskStep(t, w.s, w.roundID(round), "done", "done", taskReturn(verdictMerged, head, ""))
		w.pass()
		w.endGateAt(head, 1, "still red")
		w.pass()
		third = head
	}
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	w.pass()

	reason := clashSentBackReason(t, w.s)
	want := "task integrator round 3 failed: Done gate lint failed on commit " + third
	if !strings.Contains(reason, want) {
		t.Errorf("reason = %q, want the third round's failure %q", reason, want)
	}
	requireClashSendBack(t, w, reason)
	for round := int64(1); round <= 3; round++ {
		if got := w.roundRecord(round); got.Outcome != outcomeFailed {
			t.Errorf("round %d = %+v, want its failed record kept", round, got)
		}
	}
}

// TestIntegratorClashEndedStepSendsTheTaskBack: a task integrator step that ends without a done return records its round
// ended with how it ended, and the batch goes on. The task is sent back with that account, the candidate's change to b.go
// since the merge base, and its round's record kept.
func TestIntegratorClashEndedStepSendsTheTaskBack(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	endTaskStep(t, w.s, w.roundID(1), "stopped", "", nil)
	w.pass()
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	w.pass()

	reason := clashSentBackReason(t, w.s)
	if !strings.Contains(reason, "task integrator round 1 ended: task integrator ended stopped") {
		t.Errorf("reason = %q, want how the step ended", reason)
	}
	requireClashSendBack(t, w, reason)
	if got := w.roundRecord(1); got.Verdict != "ended" {
		t.Errorf("round 1 = %+v, want its ended record kept", got)
	}
}

// TestIntegratorClashReasonIsCutToItsBounds: an explanation longer than sendBackNoteMax is cut to it, and a candidate change
// longer than sendBackDiffMax is cut to it, so the reason fits store.TurnReasonMax and keeps the start of the change.
func TestIntegratorClashReasonIsCutToItsBounds(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	w.mergeIdentity()
	w.runner.stateDir = t.TempDir()
	w.runner.getenv = integratorRoles(t)
	var long strings.Builder
	for i := range 300 {
		fmt.Fprintf(&long, "line %03d\n", i)
	}
	first := w.commit(w.base, "b.go", long.String())
	second := w.commit(w.base, "b.go", "two\n")
	w.pass()
	w.planTurn()
	w.accept(1, w.base, first)
	w.accept(2, w.base, second)
	w.pass()
	rw := roundWorld{integrateWorld: w, second: second}
	endTaskStep(t, w.s, rw.roundID(1), "done", "done",
		taskReturn(verdictClash, "", strings.Repeat("e", 1000)))
	w.pass()
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	w.pass()

	reason := clashSentBackReason(t, w.s)
	if len(reason) > store.TurnReasonMax {
		t.Errorf("reason is %d bytes, want at most %d", len(reason), store.TurnReasonMax)
	}
	if !strings.Contains(reason, "task integrator round 1 clashed: "+strings.Repeat("e", 300)) ||
		strings.Contains(reason, strings.Repeat("e", 400)) {
		t.Errorf("reason does not cut the explanation to its bound: %q", reason)
	}
	if !strings.Contains(reason, "+line 000") || strings.Contains(reason, "+line 299") {
		t.Errorf("reason does not cut the candidate's change to its bound: %q", reason)
	}
}
