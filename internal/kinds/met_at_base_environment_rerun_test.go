package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// envRedExited returns an ended red run of part of the test run at attempt 1, at the epic's base, that exited
// store.EnvironmentExit as enginehost records it: not captured, with no verdict.
func envRedExited(part string) store.EvidenceRun {
	return store.EvidenceRun{
		ID: workID(part), Phase: evidence.PhaseRed, Revision: "base-1", State: runEnded,
		Exit: store.EnvironmentExit, NotCaptured: store.NotCapturedEnvironment,
	}
}

// envRedJudgedHolds returns an ended red run of part at the epic's base whose verdict holds, so its conclusion already
// holds at the base.
func envRedJudgedHolds(part string) store.EvidenceRun {
	return store.EvidenceRun{
		ID: workID(part), Phase: evidence.PhaseRed, Revision: "base-1", State: runEnded, Verdict: evidence.VerdictHolds,
	}
}

// envRedPassed returns an ended red run of part at the epic's base that passed: its verdict fails, as a red run's should.
func envRedPassed(part string) store.EvidenceRun {
	return store.EvidenceRun{
		ID: workID(part), Phase: evidence.PhaseRed, Revision: "base-1", State: runEnded,
		Verdict: evidence.VerdictFails, Passed: true,
	}
}

// envNodeRunAtBase returns an ended evidence node run of the test run at attempt 1 that passed, at the epic's base.
func envNodeRunAtBase() store.NodeRun {
	return store.NodeRun{
		ID: testRunID, Kind: "evidence", Outcome: outcomePassed, Attempt: 1, Revision: "base-1",
	}
}

// TestMetAtBaseQuestionEnvironmentExitRerunJudgedHolds checks that when a red run exited store.EnvironmentExit and its
// rerun was judged holds, MetAtBase names the Requirement, and Evidence.Decide ends the node failed on that rerun.
func TestMetAtBaseQuestionEnvironmentExitRerunJudgedHolds(t *testing.T) {
	t.Parallel()
	node := Node{Run: store.NodeRun{ID: testRunID, Kind: "evidence", Attempt: 1}}
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")}
	rows := Rows{
		Epic:     store.EpicRow{Requirements: []store.EpicRequirement{r1}},
		Evidence: []store.EvidenceRun{envRedExited("R1"), envRedJudgedHolds("R1 rerun")},
	}
	if got, want := MetAtBase(node, rows), []string{"R1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MetAtBase() = %v, want %v", got, want)
	}
	checkDecisions(t, Evidence{}, []decideCase{{
		"rerun judged holds ends the evidence node failed",
		nodeWith(testRev, true),
		rows,
		Action{Kind: ActEnd, Outcome: outcomeFailed, Output: "R1 red: failed"},
	}})
}

// TestMetAtBaseQuestionEnvironmentExitRerunPassedAtBase checks that when a red run exited store.EnvironmentExit and its
// rerun passed at the epic's base on an evidence node run that ended passed, Unbuilt does not say the Requirement has no
// red at the epic's base. The Requirement is green at the stored base, so it lacks no proof at all.
func TestMetAtBaseQuestionEnvironmentExitRerunPassedAtBase(t *testing.T) {
	t.Parallel()
	node := Node{Run: envNodeRunAtBase()}
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")}
	integrated := store.NodeRun{
		ID: "int-run", Kind: "integrate", Outcome: outcomePassed, Revision: "base-1", Attempt: 1,
	}
	green := store.EvidenceRun{
		ID: store.NodeWorkID("int-run", 1, "capture R1"), Phase: evidence.PhaseGreen, State: runEnded,
		Verdict: evidence.VerdictHolds, Passed: true,
	}
	rows := Rows{
		Epic:     store.EpicRow{Base: "base-1", StoredBase: "base-1", Requirements: []store.EpicRequirement{r1}},
		Runs:     []store.NodeRun{envNodeRunAtBase(), integrated},
		Evidence: []store.EvidenceRun{envRedExited("R1"), envRedPassed("R1 rerun"), green},
	}
	if got := Unbuilt(rows); got != nil {
		t.Errorf("Unbuilt() = %v, want nil: the rerun passed at the epic's base", got)
	}
	if got := MetAtBase(node, rows); got != nil {
		t.Errorf("MetAtBase() = %v, want nil: the rerun passed, so it does not hold at the base", got)
	}
}

// TestMetAtBaseQuestionEnvironmentExitThirdRunRead checks that after two runs exited store.EnvironmentExit, MetAtBase
// reads the third run, and names the Requirement when that run was judged holds.
func TestMetAtBaseQuestionEnvironmentExitThirdRunRead(t *testing.T) {
	t.Parallel()
	node := Node{Run: store.NodeRun{ID: testRunID, Kind: "evidence", Attempt: 1}}
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")}
	rows := Rows{
		Epic: store.EpicRow{Requirements: []store.EpicRequirement{r1}},
		Evidence: []store.EvidenceRun{
			envRedExited("R1"), envRedExited("R1 rerun"), envRedJudgedHolds("R1 rerun rerun"),
		},
	}
	if got, want := MetAtBase(node, rows), []string{"R1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MetAtBase() = %v, want %v", got, want)
	}
	checkDecisions(t, Evidence{}, []decideCase{{
		"third run judged holds ends the evidence node failed",
		nodeWith(testRev, true),
		rows,
		Action{Kind: ActEnd, Outcome: outcomeFailed, Output: "R1 red: failed"},
	}})
}

// TestMetAtBaseQuestionEnvironmentExitNoRerun checks that a first red run with no rerun is read as it always was: a run
// judged holds is named as met at the base.
func TestMetAtBaseQuestionEnvironmentExitNoRerun(t *testing.T) {
	t.Parallel()
	node := Node{Run: store.NodeRun{ID: testRunID, Kind: "evidence", Attempt: 1}}
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")}
	rows := Rows{
		Epic:     store.EpicRow{Requirements: []store.EpicRequirement{r1}},
		Evidence: []store.EvidenceRun{envRedJudgedHolds("R1")},
	}
	if got, want := MetAtBase(node, rows), []string{"R1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("MetAtBase() = %v, want %v", got, want)
	}
}

// TestMetAtBaseQuestionEnvironmentExitFirstRunPassedIsRedAtBase checks that a first red run with no rerun that passed at
// the epic's base still counts as red at the base, so Unbuilt does not name the Requirement for it.
func TestMetAtBaseQuestionEnvironmentExitFirstRunPassedIsRedAtBase(t *testing.T) {
	t.Parallel()
	r1 := store.EpicRequirement{ID: "R1", Text: "the suite passes", Item: evidenceItem("the suite passes")}
	integrated := store.NodeRun{
		ID: "int-run", Kind: "integrate", Outcome: outcomePassed, Revision: "base-1", Attempt: 1,
	}
	green := store.EvidenceRun{
		ID: store.NodeWorkID("int-run", 1, "capture R1"), Phase: evidence.PhaseGreen, State: runEnded,
		Verdict: evidence.VerdictHolds, Passed: true,
	}
	rows := Rows{
		Epic:     store.EpicRow{Base: "base-1", StoredBase: "base-1", Requirements: []store.EpicRequirement{r1}},
		Runs:     []store.NodeRun{envNodeRunAtBase(), integrated},
		Evidence: []store.EvidenceRun{envRedPassed("R1"), green},
	}
	if got := Unbuilt(rows); got != nil {
		t.Errorf("Unbuilt() = %v, want nil: the first red run passed at the epic's base", got)
	}
}
