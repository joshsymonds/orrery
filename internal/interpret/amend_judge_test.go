package interpret

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// amendmentReproductionThatExitsZeroWaitsForItsJudge checks that a capturing amendment whose reproduction exits 0 is
// judged as an integrate run's reproduction is: the pass starts one read-only judge step under the amendment's derived
// judge id, at the stored base, labeled with the amendment, under the reproduction judge's gate for the judge of the
// item the amendment asks for, and the amendment stays capturing while it runs. Once the judge's step ends, its ruling
// is recorded and the amendment applies in the same pass, the ruling after the reproduction's exit in its trigger's
// cause.
func amendmentReproductionThatExitsZeroWaitsForItsJudge(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		w, candidate := images.integrateWorld(t, atCaptured)
		w.pass()
		item := amendedTestItem("b.go")
		amended := amendR1(t, w.s, item)
		drive := func() {
			t.Helper()
			epic, err := w.s.Epic(ctx, runEpic)
			if err != nil {
				t.Fatalf("Epic: %v", err)
			}
			w.runner.passAmendments(ctx, []store.EpicRow{epic})
			if w.logs.Len() > 0 {
				t.Fatalf("pass logged an error: %s", w.logs.String())
			}
		}
		drive()
		judge(t, w.s, amended.RedRun, 1, evidence.VerdictFails)
		drive()
		drive()
		stored := firstAmendment(t, w.s)
		judge(t, w.s, stored.GreenRun, 0, evidence.VerdictHolds)
		end := store.NodeCheckEnd{Exit: 0, Stdout: reproJudgeStdout}
		if err := w.s.EndNodeCheck(ctx, stored.ReproCheck, end, runClock()); err != nil {
			t.Fatalf("EndNodeCheck: %v", err)
		}
		started := len(w.fakes.steps.calls)
		drive()

		if got := len(w.fakes.steps.calls) - started; got != 1 {
			t.Fatalf("steps the pass started = %d, want the one judge", got)
		}
		start := w.fakes.steps.calls[started]
		id := store.AmendmentJudgeID(stored, 0)
		gate := enginehost.ReproJudgeGate(item.Judge)
		if start.ID != id || !start.ReadOnly || start.Revision != candidate || start.Task != "amendment A1" ||
			start.Workspace != w.repo || !reflect.DeepEqual(start.AgentGate, &gate) {
			t.Errorf("judge start = %+v, want read-only %s at %s labeled amendment A1 under gate %+v", start, id,
				candidate, gate)
		}
		for _, want := range []string{
			"Task T1: first\n", "Task T2: second\n", "\nConclusion: the runner runs\n",
			"\nRed at the base " + w.base + ": judged fails against the conclusion",
			"\n  Passed (1): TestRunner\n", "Diff stat (git diff --stat " + w.base + " " + candidate + "):\n",
		} {
			if !strings.Contains(start.Prompt, want) {
				t.Errorf("judge prompt lacks %q:\n%s", want, start.Prompt)
			}
		}
		if got := firstAmendment(t, w.s).State; got != store.AmendmentCapturing {
			t.Fatalf("amendment while its judge runs = %s, want capturing", got)
		}
		drive()
		if got := len(w.fakes.steps.calls) - started; got != 1 {
			t.Errorf("steps started after a pass while the judge runs = %d, want the one judge", got)
		}

		endTaskStep(t, w.s, id, judgeDone, "done",
			judgeReturn(evidence.VerdictHolds, "the tests prove what the amended item claims", ""))
		drive()

		want := []store.ReproRuling{{
			Check: stored.ReproCheck, Judge: 0, Step: id, Verdict: evidence.VerdictHolds,
			Reason: "the tests prove what the amended item claims", Created: runClock(),
		}}
		if got := rulingsOf(t, w.s); !reflect.DeepEqual(got, want) {
			t.Errorf("rulings = %+v, want %+v", got, want)
		}
		if got := firstAmendment(t, w.s).State; got != store.AmendmentApplied {
			t.Errorf("amendment once its judge ruled = %s, want applied", got)
		}
		cause, _ := amendTrigger(t, w.s)
		wantCause := "evidence of R1 amended by the owner (A1): the green run " + stored.GreenRun + " at the stored base " +
			candidate + " passed; its reproduction " + stored.ReproCheck + " at the epic's base exited 0; the judge " +
			"ruled that it does not contradict red: the tests prove what the amended item claims"
		if cause != wantCause {
			t.Errorf("trigger amend-1 = %q, want %q", cause, wantCause)
		}
	}
}
