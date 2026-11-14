package enginehost

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// returnCheckRefused is the note of a return the test return check refuses.
const returnCheckRefused = "refuse"

// The failure lines the test return check gives a refused return.
const (
	returnCheckNoFile        = "plan owns no file"
	returnCheckNoRequirement = "plan cites no requirement"
)

// returnCheckRecorder is a return check that refuses each return whose note is returnCheckRefused and records every
// value it is called with.
type returnCheckRecorder struct {
	values []string
}

func (c *returnCheckRecorder) check(value json.RawMessage) []string {
	c.values = append(c.values, string(value))
	if strings.Contains(string(value), `"note":"`+returnCheckRefused+`"`) {
		return []string{returnCheckNoFile, returnCheckNoRequirement}
	}
	return nil
}

func TestStepLoopReturnCheckRefusalIsSentBackThenPassingReturnEndsDone(t *testing.T) {
	check := &returnCheckRecorder{}
	f := newStepDoneFixture(t, func(cfg *stepRunConfig) { cfg.returnCheck = check.check })
	_, done := f.start(t)

	f.run.Return(judgeLoopDone(returnCheckRefused))
	f.run.Return(judgeLoopDone("fixed"))
	waitRunEnd(t, done)

	continued := f.sessions.continuedTexts()
	if len(continued) != 1 {
		t.Fatalf("Continue called %d times, want 1", len(continued))
	}
	if !strings.Contains(continued[0], "- return: plan owns no file; plan cites no requirement") {
		t.Errorf("Continue text %q does not carry the return check's failures", continued[0])
	}
	row := f.row(t)
	if row.End != stepLoopEndDone || row.Status != stepLoopDone {
		t.Fatalf("end = %q status %q, want done DONE", row.End, row.Status)
	}
	wantRefused := store.StepCheck{
		Round: 1, Name: stepLoopReturnName, OK: false, Detail: "plan owns no file; plan cites no requirement",
	}
	if got := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 1), stepLoopReturnName); got != wantRefused {
		t.Errorf("round 1 return check = %+v, want %+v", got, wantRefused)
	}
	wantPassed := store.StepCheck{Round: 2, Name: stepLoopReturnName, OK: true}
	if got := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, 2), stepLoopReturnName); got != wantPassed {
		t.Errorf("round 2 return check = %+v, want %+v", got, wantPassed)
	}
}

func TestStepLoopReturnCheckFourRefusalsEndChecksFailed(t *testing.T) {
	check := &returnCheckRecorder{}
	f := newStepDoneFixture(t, func(cfg *stepRunConfig) { cfg.returnCheck = check.check })
	_, done := f.start(t)

	for range stepLoopMaxContinues + 1 {
		f.run.Return(judgeLoopDone(returnCheckRefused))
	}
	waitRunEnd(t, done)

	if got := len(f.sessions.continuedTexts()); got != stepLoopMaxContinues {
		t.Fatalf("Continue called %d times, want %d", got, stepLoopMaxContinues)
	}
	row := f.row(t)
	if row.End != stepLoopEndChecksFailed {
		t.Fatalf("end = %q, want checks_failed", row.End)
	}
	const reason = "return: plan owns no file; plan cites no requirement"
	if row.Reason != reason {
		t.Errorf("reason = %q, want %q", row.Reason, reason)
	}
	for round := 1; round <= stepLoopMaxContinues+1; round++ {
		got := stepDoneCheck(t, stepDoneRoundChecks(row.Checks, round), stepLoopReturnName)
		if got.OK || got.Detail != "plan owns no file; plan cites no requirement" {
			t.Errorf("round %d return check = %+v, want it failing with the failure lines", round, got)
		}
	}
}

func TestStepLoopReturnCheckEndsDoneWithoutHaikuJudge(t *testing.T) {
	check := &returnCheckRecorder{}
	haiku := &judgeLoopHaiku{}
	f := newJudgeLoopFixture(t, haiku, func(cfg *stepRunConfig) { cfg.returnCheck = check.check })
	_, done := f.start(t)

	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":1}`)})
	f.run.Return(judgeLoopDone("fine"))
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndDone || row.Status != stepLoopDone {
		t.Fatalf("end = %q status %q, want done DONE", row.End, row.Status)
	}
	if got := haiku.callInputs(); len(got) != 0 {
		t.Errorf("judge called %d times for a return with a return check, want 0", len(got))
	}
	if len(row.Verdicts) != 0 {
		t.Errorf("stored %d verdicts, want 0: %+v", len(row.Verdicts), row.Verdicts)
	}
	wantValues := []string{`{"status":"DONE","note":"fine"}`}
	if !reflect.DeepEqual(check.values, wantValues) {
		t.Errorf("return check called with %q, want only the schema-valid return %q", check.values, wantValues)
	}
}

func TestStepsStartHandsItsRunTheReturnCheck(t *testing.T) {
	f := newStepStartFixture(t)
	start := stepStartWriting(stepWorktreeTestAddLinked(t, stepGitTestRepo(t)))
	start.ReturnCheck = func(json.RawMessage) []string { return []string{"from start"} }

	if _, err := f.steps.Start(t.Context(), start); err != nil {
		t.Fatalf("Start: %v", err)
	}

	run, ok := f.sessions.first(t).Observer.(*stepRun)
	if !ok {
		t.Fatal("StartStep Observer is not a *stepRun")
	}
	if run.returnCheck == nil {
		t.Fatal("step run return check is nil, want the start's return check")
	}
	if got := run.returnCheck(nil); !reflect.DeepEqual(got, []string{"from start"}) {
		t.Errorf("step run return check gives %q, want the start's", got)
	}
}
