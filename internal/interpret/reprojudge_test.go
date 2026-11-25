package interpret

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// The ends a judge step stores: done when its one assertion holds, and gate_failed when its gate failed it.
const (
	judgeDone       = "done"
	judgeGateFailed = "gate_failed"
)

// reproJudgeStdout is the go test -v output a reproduction that exited 0 printed: its one test passed.
const reproJudgeStdout = "=== RUN   TestRunner\n--- PASS: TestRunner (0.00s)\nPASS\nok  \tinternal/a\t0.01s\n"

// judgeReturn returns the return of a judge step whose one assertion has result, with evidenceText as its evidence
// and, for a fails result, one finding that cites the assertion with claim and evidenceText.
func judgeReturn(result, evidenceText, claim string) map[string]any {
	findings := []any{}
	if result == evidence.VerdictFails {
		findings = append(findings, map[string]any{"citation": "a", "claim": claim, "evidence": evidenceText})
	}
	return map[string]any{
		"status":   "done",
		"results":  []any{map[string]any{"assertion": "a", "kind": "must", "result": result, "evidence": evidenceText}},
		"findings": findings,
	}
}

// judgeFails returns the return of a judge that ruled that a reproduction contradicts red, with claim as its finding's
// claim and TestRunner passes on the base as its evidence.
func judgeFails(claim string) map[string]any {
	return judgeReturn(evidence.VerdictFails, "TestRunner passes on the base", claim)
}

// toBuiltJudge runs a built-in repro world until the judge of its batch's reproduction of R1 has started: the
// reproduction exits 0 with reproJudgeStdout, as a capture whose tests pass on the base does, and the pass after it
// starts the judge. It returns the world and the first judge's id.
func toBuiltJudge(t *testing.T) (*integrateWorld, string) {
	t.Helper()
	w := toBuiltReproduction(t)
	end := store.NodeCheckEnd{Exit: 0, Stdout: reproJudgeStdout}
	if err := w.s.EndNodeCheck(context.Background(), reproCheckOf(t, w), end, runClock()); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}
	w.pass()
	return w, kinds.ReproJudgeID(runAt(t, w.s, reproBuiltBatch), "R1", 0)
}

// rulingsOf returns the rulings on the reproductions of the test epic.
func rulingsOf(t *testing.T, s *store.Store) []store.ReproRuling {
	t.Helper()
	rulings, err := s.ReproRulings(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("ReproRulings: %v", err)
	}
	return rulings
}

// TestIntegrateReproductionThatExitsZeroStartsItsJudge: a batch's reproduction that exits 0 starts one read-only judge
// step under its derived id, at the candidate, in the epic's repository, labeled with the batch's path, under the
// reproduction judge's gate for the judge R1's item names. Its prompt carries the batch's task that claims R1, the
// conclusion, red's verdict at the epic's base, the reproduction's command and logs, the test facts read from them, and
// the diff stat from the base to the candidate. The batch does not advance while the judge runs, and a pass while it
// runs starts nothing more.
func TestIntegrateReproductionThatExitsZeroStartsItsJudge(t *testing.T) {
	t.Parallel()
	w, id := toBuiltJudge(t)
	if len(w.fakes.steps.calls) != 1 {
		t.Fatalf("steps started = %d, want the one judge", len(w.fakes.steps.calls))
	}
	start := w.fakes.steps.calls[0]
	candidate := w.candidate(reproBuiltBatch)
	gate := enginehost.ReproJudgeGate("a judge")
	if start.ID != id || !start.ReadOnly || start.Workspace != w.repo || start.Revision != candidate ||
		start.Task != reproBuiltBatch || start.Epic != runEpic || !reflect.DeepEqual(start.AgentGate, &gate) ||
		start.Purpose != "Judge R1's reproduction." {
		t.Errorf("judge start = %+v, want read-only %s at %s in %s labeled %s under gate %+v", start, id, candidate,
			w.repo, reproBuiltBatch, gate)
	}
	for _, want := range []string{
		"Task T1: only\n",
		"Deliverable: the runner\n",
		"\nConclusion: the runner runs\n",
		"\nRed at the base " + w.base + ": judged fails against the conclusion",
		"\nReproduction command: [\"scripts/check\"]\n",
		"\n    --- PASS: TestRunner (0.00s)\n",
		"\n- Passed (1): TestRunner\n",
		"Diff stat (git diff --stat " + w.base + " " + candidate + "):\n",
		reproTestFile + " ",
	} {
		if !strings.Contains(start.Prompt, want) {
			t.Errorf("judge prompt lacks %q:\n%s", want, start.Prompt)
		}
	}
	if run := runAt(t, w.s, reproBuiltBatch); run.State != store.NodeRunning {
		t.Errorf("batch while its judge runs = %+v, want running", run)
	}
	w.pass()
	if got := len(w.fakes.steps.calls); got != 1 {
		t.Errorf("steps started after a pass while the judge runs = %d, want the one judge", got)
	}
}

// TestIntegrateJudgeRulingIsRecordedAndShownAsTheDisagreement: once the judge's step ends, its ruling is recorded
// under the reproduction and the judge, and the batch advances, since a ruling gates nothing. A ruling that the
// reproduction contradicts red is the Requirement's disagreement at the epic's base, with the judge's reason.
func TestIntegrateJudgeRulingIsRecordedAndShownAsTheDisagreement(t *testing.T) {
	t.Parallel()
	w, id := toBuiltJudge(t)
	endTaskStep(t, w.s, id, judgeGateFailed, "failed", judgeFails("it asserts nothing the base lacks"))
	w.pass()

	check := reproCheckOf(t, w)
	reason := "it asserts nothing the base lacks: TestRunner passes on the base"
	want := []store.ReproRuling{{
		Check: check, Judge: 0, Step: id, Verdict: evidence.VerdictFails, Reason: reason, Created: runClock(),
	}}
	if got := rulingsOf(t, w.s); !reflect.DeepEqual(got, want) {
		t.Errorf("rulings = %+v, want %+v", got, want)
	}
	run := runAt(t, w.s, reproBuiltBatch)
	if run.State != store.NodeEnded || run.Outcome != outcomePassed {
		t.Errorf("batch once its judge ruled = %+v, want ended passed", run)
	}
	flow, err := w.s.EpicFlow(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	rows := kinds.Rows{
		Epic: flow.Epic, Runs: flow.Runs, Checks: flow.Checks, Evidence: flow.Evidence, Rulings: flow.Rulings,
	}
	line := "R1: the judge ruled that its reproduction at the epic's base " + w.base + " contradicts red: " + reason
	if got := kinds.Disagreement(rows, "R1"); got != line {
		t.Errorf("Disagreement(R1) = %q, want %q", got, line)
	}
}

// TestIntegrateJudgeWithNoVerdictStartsASecond: a first judge whose step ends with no verdict is recorded as no
// verdict, and the second judge starts under its own id in the same pass; the reproduction does not run again. A
// second with no verdict settles the reproduction as no verdict, and the batch advances.
func TestIntegrateJudgeWithNoVerdictStartsASecond(t *testing.T) {
	t.Parallel()
	w, first := toBuiltJudge(t)
	checks := len(w.fakes.checks.calls)
	endTaskStep(t, w.s, first, "overtime", "failed", nil)
	w.pass()

	second := kinds.ReproJudgeID(runAt(t, w.s, reproBuiltBatch), "R1", 1)
	if got := w.fakes.steps.calls[len(w.fakes.steps.calls)-1].ID; got != second {
		t.Fatalf("last step started = %s, want the second judge %s", got, second)
	}
	if got := len(w.fakes.checks.calls); got != checks {
		t.Errorf("checks started after the first judge = %d, want %d: the reproduction does not run again", got,
			checks)
	}
	endTaskStep(t, w.s, second, "overtime", "failed", nil)
	w.pass()
	check := reproCheckOf(t, w)
	want := []store.ReproRuling{
		{Check: check, Judge: 0, Step: first, Reason: "overtime", Created: runClock()},
		{Check: check, Judge: 1, Step: second, Reason: "overtime", Created: runClock()},
	}
	if got := rulingsOf(t, w.s); !reflect.DeepEqual(got, want) {
		t.Errorf("rulings = %+v, want %+v", got, want)
	}
	if run := runAt(t, w.s, reproBuiltBatch); run.State != store.NodeEnded || run.Outcome != outcomePassed {
		t.Errorf("batch once its judges gave no verdict twice = %+v, want ended passed", run)
	}
}

// TestIntegrateJudgeThatDoesNotStartIsARuling: a judge whose start the engine refuses for good is recorded as a judge
// that did not start, with the refusal, and the batch advances in the same pass: a judge that cannot start holds the
// batch up for no pass. A start refused for a reason that may pass records nothing, and the next pass starts it again.
func TestIntegrateJudgeThatDoesNotStartIsARuling(t *testing.T) {
	t.Parallel()
	w := toBuiltReproduction(t)
	w.fakes.steps.refuse = errors.New("the engine is restarting")
	nodeEndCheck(t, w.s, reproCheckOf(t, w), 0, "")
	w.runner.Pass(context.Background())
	if !strings.Contains(w.logs.String(), "the engine is restarting") {
		t.Fatalf("pass logs = %q, want the start error that may pass", w.logs.String())
	}
	w.logs.Reset()
	if got := rulingsOf(t, w.s); got != nil {
		t.Fatalf("rulings after a start that may pass = %+v, want none", got)
	}
	refusal := fmt.Errorf("%w: agent a judge is not defined", enginehost.ErrBadAgent)
	w.fakes.steps.refuse = refusal
	w.pass()

	want := []store.ReproRuling{{
		Check: reproCheckOf(t, w), Judge: 0, Reason: refusal.Error(), Created: runClock(),
	}}
	if got := rulingsOf(t, w.s); !reflect.DeepEqual(got, want) {
		t.Errorf("rulings = %+v, want %+v", got, want)
	}
	if run := runAt(t, w.s, reproBuiltBatch); run.State != store.NodeEnded || run.Outcome != outcomePassed {
		t.Errorf("batch once its judge did not start = %+v, want ended passed", run)
	}
}

// TestIntegrateReproductionThatFailsStartsNoJudge: a reproduction that exits non-zero is what red predicts, so no judge
// starts and the batch advances with no ruling.
func TestIntegrateReproductionThatFailsStartsNoJudge(t *testing.T) {
	t.Parallel()
	w := toBuiltReproduction(t)
	nodeEndCheck(t, w.s, reproCheckOf(t, w), 1, "")
	w.pass()
	if got := len(w.fakes.steps.calls); got != 0 {
		t.Errorf("steps started = %d, want no judge", got)
	}
	if got := rulingsOf(t, w.s); got != nil {
		t.Errorf("rulings = %+v, want none", got)
	}
	if run := runAt(t, w.s, reproBuiltBatch); run.State != store.NodeEnded || run.Outcome != outcomePassed {
		t.Errorf("batch = %+v, want ended passed", run)
	}
}

// flows is the epic reader of a ReadoptGate test: the flow of each epic by its id.
type flows map[string]store.EpicFlow

// EpicFlow returns the flow of epic id, or an error when it has none.
func (f flows) EpicFlow(_ context.Context, id string) (store.EpicFlow, error) {
	flow, ok := f[id]
	if !ok {
		return store.EpicFlow{}, fmt.Errorf("epic %s: %w", id, store.ErrNoEpic)
	}
	return flow, nil
}

// TestReadoptGateRebuildsReproductionJudgeGates: a re-adopted judge of an integrate run's reproduction, labeled with
// the run's path, gets the reproduction judge's gate for the judge of the Requirement whose derived judge id is the
// row's, whichever judge it is; an amendment's judge, labeled with the amendment, gets the gate for the judge of the
// item the amendment asks for. A row whose id is no judge's of its run, and an amendment the epic does not hold, are
// errors. A row of another gate, and a task's judge labeled T<seq>, get no gate.
func TestReadoptGateRebuildsReproductionJudgeGates(t *testing.T) {
	t.Parallel()
	run := store.NodeRun{ID: "int-run", Path: reproBuiltBatch, Kind: kindIntegrate, Attempt: 2}
	r2 := evidence.Item{Kind: evidence.KindTest, Judge: "sonnet-high-ro"}
	epics := flows{runEpic: {
		Epic: store.EpicRow{
			ID: runEpic,
			Requirements: []store.EpicRequirement{
				{ID: "R1", Item: evidence.Item{Kind: evidence.KindTest, Judge: "haiku-high-ro"}}, {ID: "R2", Item: r2},
			},
			Amendments: []store.EvidenceAmendment{
				{Seq: 3, Requirement: "R1", Item: evidence.Item{Kind: evidence.KindTest, Judge: "opus-ro"}},
			},
		},
		Runs: []store.NodeRun{run},
	}}
	readopt := ReadoptGate(epics)
	row := func(id, task string) store.StepRow {
		return store.StepRow{
			ID: id, Epic: runEpic, Task: task, ReadOnly: true, GateName: enginehost.ReproJudgeGateName,
		}
	}
	for _, tc := range []struct {
		name  string
		row   store.StepRow
		judge string
	}{
		{"an integrate run's first judge", row(kinds.ReproJudgeID(run, "R2", 0), reproBuiltBatch), "sonnet-high-ro"},
		{"an integrate run's second judge", row(kinds.ReproJudgeID(run, "R1", 1), reproBuiltBatch), "haiku-high-ro"},
		{"an amendment's judge", row("amend-judge", "amendment A3"), "opus-ro"},
	} {
		gate, err := readopt(context.Background(), tc.row)
		if want := enginehost.ReproJudgeGate(tc.judge); err != nil || gate == nil || !reflect.DeepEqual(*gate, want) {
			t.Errorf("ReadoptGate(%s) = %+v, %v; want %+v", tc.name, gate, err, want)
		}
	}
	for name, bad := range map[string]store.StepRow{
		"a row whose id is no judge's of its run": row("other-step", reproBuiltBatch),
		"an amendment the epic does not hold":     row("amend-judge", "amendment A4"),
	} {
		if _, err := readopt(context.Background(), bad); err == nil {
			t.Errorf("ReadoptGate(%s) = nil error, want one", name)
		}
	}
	other := row(kinds.ReproJudgeID(run, "R1", 0), reproBuiltBatch)
	other.GateName = "review"
	for name, none := range map[string]store.StepRow{
		"a row of another gate":            other,
		"a task's judge labeled with T<n>": row("task-judge", "T1"),
	} {
		if gate, err := readopt(context.Background(), none); err != nil || gate != nil {
			t.Errorf("ReadoptGate(%s) = %+v, %v; want no gate and no error", name, gate, err)
		}
	}
}
