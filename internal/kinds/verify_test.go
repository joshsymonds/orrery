package kinds

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// verifyGates are the Done gates of integrateEpic as the floors read them: just check is declared always, just lint is
// not.
func verifyGates() []DoneGate {
	return []DoneGate{{Name: "just check", Always: true}, {Name: "just lint"}}
}

// verifyScope is a batch's scope over R1, R2 and R4, whose floor and fallback are R1.
func verifyScope() VerifyScope {
	return VerifyScope{Items: []string{"R1", "R2", "R4"}, Floor: []string{"R1"}, Fallback: []string{"R1"}}
}

// verifiedOne returns unverifiedOne with batch recorded as its verification.
func verifiedOne(batch Verification) string {
	return withRecorded(unverifiedOne, &batch, nil)
}

// TestSettleGatesFloors: a gate declared always runs whatever the step chose, a gate that does not resolve runs, a gate
// the step does not name runs, and a gate the step chose to skip that no floor holds is skipped. A name that is no Done
// gate is ignored and noted, and a gate named twice runs when either entry runs.
func TestSettleGatesFloors(t *testing.T) {
	t.Parallel()
	gates := []DoneGate{
		{Name: "check", Always: true}, {Name: "android"}, {Name: "web"}, {Name: "gone", Unresolved: "no gate gone"},
		{Name: "docs"},
	}
	chosen := []GateChoice{
		{Gate: "check", Run: false, Reason: "only docs changed"},
		{Gate: "android", Run: false, Reason: "nothing under android/ or proto/ changed"},
		{Gate: "gone", Run: false, Reason: "not needed"},
		{Gate: "lint", Run: false, Reason: "no such gate"},
		{Gate: "docs", Run: false, Reason: "first entry"},
		{Gate: "docs", Run: true, Reason: "docs changed"},
	}
	got, ignored := SettleGates(gates, chosen)
	want := []GateChoice{
		{Gate: "check", Run: true, Reason: "only docs changed", Floor: FloorAlways},
		{Gate: "android", Run: false, Reason: "nothing under android/ or proto/ changed"},
		{Gate: "web", Run: true, Floor: FloorUnmentioned},
		{Gate: "gone", Run: true, Reason: "not needed", Floor: FloorUnresolved},
		{Gate: "docs", Run: true, Reason: "docs changed"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SettleGates = %+v, want %+v", got, want)
	}
	wantIgnored := []string{"gate lint, which is not a Done gate of the epic", "a second entry for gate docs"}
	if !reflect.DeepEqual(ignored, wantIgnored) {
		t.Errorf("ignored = %q, want %q", ignored, wantIgnored)
	}
}

// TestSettleRequirementsFloors: a Requirement the floor holds is captured whatever the step chose, the step may add one
// the floor does not hold, one it chose not to capture is not, one it does not name is not captured, and a name that
// is no Requirement with an evidence item is ignored and noted.
func TestSettleRequirementsFloors(t *testing.T) {
	t.Parallel()
	chosen := []RequirementChoice{
		{Requirement: "R1", Capture: false, Reason: "untouched"},
		{Requirement: "R2", Capture: true, Reason: "the resolution rewrote its handler"},
		{Requirement: "R3", Capture: true, Reason: "names no item"},
	}
	got, ignored := SettleRequirements(verifyScope(), chosen)
	want := []RequirementChoice{
		{Requirement: "R1", Capture: true, Reason: "untouched", Floor: FloorClaimed},
		{Requirement: "R2", Capture: true, Reason: "the resolution rewrote its handler"},
		{Requirement: "R4", Capture: false, Floor: FloorUnmentioned},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SettleRequirements = %+v, want %+v", got, want)
	}
	if want := []string{
		"Requirement R3, which is not a Requirement of the epic that names an evidence item",
	}; !reflect.DeepEqual(ignored, want) {
		t.Errorf("ignored = %q, want %q", ignored, want)
	}
}

// TestSettleVerificationFallsBack: a step that did not end done, a return that does not decode, one that lacks a list,
// one with an entry that gives no reason, and one with an entry that leaves out run or capture each fall back: every
// Done gate runs, only the fallback Requirements are captured, and the fallback says why.
func TestSettleVerificationFallsBack(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		end  VerifyEnd
		why  string
	}{
		{"a step that did not end done", VerifyEnd{Step: "v", Why: "it ended stopped"},
			"the verification step did not end done: it ended stopped"},
		{"a return that does not decode", VerifyEnd{Step: "v", Done: true, Return: "not json"},
			"the return does not decode"},
		{"a return with no requirements", VerifyEnd{Step: "v", Done: true, Return: `{"status":"done","gates":[]}`},
			"the return lacks gates or requirements"},
		{"a gate with no reason", VerifyEnd{Step: "v", Done: true,
			Return: `{"gates":[{"gate":"just lint","run":false,"reason":" "}],"requirements":[]}`},
			"gates[0] (just lint) gives no reason"},
		{"a gate that says nothing of whether it runs", VerifyEnd{Step: "v", Done: true,
			Return: `{"gates":[{"gate":"just lint","reason":"x"}],"requirements":[]}`},
			"gates[0] (just lint) says nothing of whether it runs"},
		{"a Requirement with no reason", VerifyEnd{Step: "v", Done: true,
			Return: `{"gates":[],"requirements":[{"requirement":"R2","capture":true,"reason":""}]}`},
			"requirements[0] (R2) gives no reason"},
		{"a Requirement that says nothing of its capture", VerifyEnd{Step: "v", Done: true,
			Return: `{"gates":[],"requirements":[{"requirement":"R2","reason":"x"}]}`},
			"requirements[0] (R2) says nothing of whether it is captured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := SettleVerification(tc.end, verifyGates(), verifyScope())
			if !strings.HasPrefix(got.Fallback, tc.why) {
				t.Errorf("fallback = %q, want it to start %q", got.Fallback, tc.why)
			}
			wantGates := []GateChoice{
				{
					Gate:  "just check",
					Run:   true,
					Floor: FloorFallback,
				},
				{Gate: "just lint", Run: true, Floor: FloorFallback},
			}
			if !reflect.DeepEqual(got.Gates, wantGates) {
				t.Errorf("gates = %+v, want %+v", got.Gates, wantGates)
			}
			wantRequirements := []RequirementChoice{
				{Requirement: "R1", Capture: true, Floor: FloorFallback},
				{Requirement: "R2", Capture: false, Floor: FloorFallback},
				{Requirement: "R4", Capture: false, Floor: FloorFallback},
			}
			if !reflect.DeepEqual(got.Requirements, wantRequirements) {
				t.Errorf("requirements = %+v, want %+v", got.Requirements, wantRequirements)
			}
			if got.Step != "v" {
				t.Errorf("step = %q, want v", got.Step)
			}
		})
	}
}

// TestSettleVerificationReadsTheChoice: a done step whose return gives a reason for every entry is read, with the floors
// applied, and its ignored entries kept.
func TestSettleVerificationReadsTheChoice(t *testing.T) {
	t.Parallel()
	end := VerifyEnd{Step: "v", Done: true, Return: `{"status":"done",` +
		`"gates":[{"gate":"just check","run":false,"reason":"docs only"},{"gate":"just lint","run":false,` +
		`"reason":"no Go file changed"},{"gate":"just web","run":true,"reason":"stale"}],` +
		`"requirements":[{"requirement":"R2","capture":true,"reason":"its doc changed"}]}`}
	got := SettleVerification(end, verifyGates(), verifyScope())
	want := Verification{
		Step: "v",
		Gates: []GateChoice{
			{Gate: "just check", Run: true, Reason: "docs only", Floor: FloorAlways},
			{Gate: "just lint", Run: false, Reason: "no Go file changed"},
		},
		Requirements: []RequirementChoice{
			{Requirement: "R1", Capture: true, Floor: FloorClaimed},
			{Requirement: "R2", Capture: true, Reason: "its doc changed"},
			{Requirement: "R4", Capture: false, Floor: FloorUnmentioned},
		},
		Ignored: []string{"gate just web, which is not a Done gate of the epic"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SettleVerification = %+v, want %+v", got, want)
	}
}

// TestVerifyScopeOf: a batch's floor and fallback are the Requirements its composed tasks claim or contribute to; a
// round's floor adds the round's own task's, and its fallback is every Requirement that names an item.
func TestVerifyScopeOf(t *testing.T) {
	t.Parallel()
	epic := integrateEpic()
	epic.Requirements = append(epic.Requirements,
		store.EpicRequirement{ID: "R4", Text: "the round's task", Item: evidenceItem("the round holds")})
	epic.Tasks = []store.EpicTask{batchTask(1, []string{"R1"}, nil), batchTask(2, nil, []string{"R4"})}
	run := batchNode(testRev, unverifiedOne, false).Run
	batch := VerifyScopeOf(run, Rows{Epic: epic}, 0)
	if want := (VerifyScope{
		Items: []string{"R1", "R2", "R4"}, Floor: []string{"R1"}, Fallback: []string{"R1"},
	}); !reflect.DeepEqual(batch, want) {
		t.Errorf("batch scope = %+v, want %+v", batch, want)
	}
	round := VerifyScopeOf(run, Rows{Epic: epic}, 2)
	if want := (VerifyScope{
		Items: []string{"R1", "R2", "R4"}, Floor: []string{"R1", "R4"}, Fallback: []string{"R1", "R2", "R4"},
	}); !reflect.DeepEqual(round, want) {
		t.Errorf("round scope = %+v, want %+v", round, want)
	}
}

// TestIntegrateVerificationStep: a batch with no recorded verification starts its verification step before any Done
// gate, waits while it runs, and records it once it has ended. A recorded choice is read from the result, so the first
// Done gate starts.
func TestIntegrateVerificationStep(t *testing.T) {
	t.Parallel()
	epic := integrateEpic()
	id := VerifyID(batchNode(testRev, unverifiedOne, false).Run, 0, 0)
	cases := []decideCase{
		{"an unverified batch starts its verification step", batchNode(testRev, unverifiedOne, false),
			Rows{Epic: epic}, Action{Kind: ActVerify, IDs: []string{id}, Items: []string{VerifyStart}}},
		{"a running verification step waits", batchNode(testRev, unverifiedOne, false),
			Rows{Epic: epic, Steps: []store.StepRow{{ID: id}}}, Action{Kind: ActNone}},
		{"an ended verification step is recorded", batchNode(testRev, unverifiedOne, false),
			Rows{Epic: epic, Steps: []store.StepRow{{ID: id, End: "failed"}}},
			Action{Kind: ActVerify, IDs: []string{id}, Items: []string{VerifyRecord}}},
		{"a recorded choice starts the first Done gate", batchNode(testRev, composedOne(), false),
			Rows{Epic: epic}, Action{Kind: ActStart, IDs: []string{workID("done 1")}, Items: []string{"just check"}}},
	}
	checkDecisions(t, Integrate{}, cases)
	if want := store.NodeWorkID(testRunID, 1, "verify"); id != want {
		t.Errorf("VerifyID = %q, want %q", id, want)
	}
}

// TestIntegrateSkippedGates: a gate the choice skips starts no check and is passed over, the gate after it keeps its
// own check id, a failed gate that ran still fails the batch, and a batch whose only gates ran green advances with the
// choice's lines in its output. A skipped gate has no check, and its line says it was skipped, never that it passed.
func TestIntegrateSkippedGates(t *testing.T) {
	t.Parallel()
	epic := integrateEpic()
	skipFirst := verifiedOne(Verification{Step: "v", Gates: []GateChoice{
		{Gate: "just check", Run: false, Reason: "docs only"},
		{Gate: "just lint", Run: true, Reason: "lint reads docs"},
	}})
	skipBoth := verifiedOne(Verification{Step: "v", Gates: []GateChoice{
		{Gate: "just check", Run: false, Reason: "docs only"}, {Gate: "just lint", Run: false, Reason: "docs only"},
	}})
	lines := "verification skipped Done gate just check: docs only\nverification ran Done gate just lint: lint reads docs"
	cases := []decideCase{
		{"the gate after a skipped one starts under its own id", batchNode(testRev, skipFirst, false),
			Rows{Epic: epic}, Action{Kind: ActStart, IDs: []string{workID("done 2")}, Items: []string{"just lint"}}},
		{
			"a skipped gate's check is never started, so captures follow the gate that ran",
			batchNode(testRev, skipFirst, false),
			Rows{Epic: epic, Checks: []store.NodeCheck{passedCheck("done 2")}},
			Action{
				Kind:  ActCapture,
				IDs:   []string{workID("capture R1"), workID("capture R2")},
				Items: []string{"R1", "R2"},
			},
		},
		{"a gate that ran and failed starts its triage under its own id", batchNode(testRev, skipFirst, true),
			Rows{Epic: epic, Checks: []store.NodeCheck{failedCheck("done 2")}},
			Action{
				Kind: ActTriage, IDs: []string{workID("triage done 2"), workID("done 2")},
				Items: []string{TriageStart, "just lint", TriageEnvironment, TriageFlake},
			}},
		{
			"a gate that ran and failed fails the batch with the choice",
			batchNode(testRev, skipFirst, true),
			Rows{
				Epic: epic, Checks: []store.NodeCheck{failedCheck("done 2")},
				Triages: []store.GateTriage{changeTriaged(workID("done 2"))},
			},
			Action{
				Kind:    ActEnd,
				Outcome: outcomeFailed,
				Output:  "Done gate just lint failed; " + changeNote + "\n" + lines,
			},
		},
		{
			"a batch with every gate skipped goes to its captures",
			batchNode(testRev, skipBoth, false),
			Rows{Epic: epic},
			Action{
				Kind:  ActCapture,
				IDs:   []string{workID("capture R1"), workID("capture R2")},
				Items: []string{"R1", "R2"},
			},
		},
		{"a green batch advances with the choice's lines", batchNode(testRev, skipFirst, true),
			Rows{Epic: epic, Checks: []store.NodeCheck{passedCheck("done 2")}, Evidence: []store.EvidenceRun{
				greenCapture("capture R1", true), greenCapture("capture R2", true),
			}},
			Action{Kind: ActAdvance, Output: "R1 green\nR2 green\nR3 not yet green\n" + lines}},
	}
	checkDecisions(t, Integrate{}, cases)
}

// TestIntegrateRequirementFloor: a Requirement the batch's tasks claim or contribute to is captured though the choice
// says not to, a Requirement no task of the batch touches is captured when the choice adds it, and one the choice does
// not add is not.
func TestIntegrateRequirementFloor(t *testing.T) {
	t.Parallel()
	epic := integrateEpic()
	epic.Tasks = []store.EpicTask{batchTask(1, []string{"R1"}, nil)}
	refused := verifiedOne(Verification{Step: "v", Requirements: []RequirementChoice{
		{Requirement: "R1", Capture: false, Reason: "untouched"},
	}})
	added := verifiedOne(Verification{Step: "v", Requirements: []RequirementChoice{
		{Requirement: "R2", Capture: true, Reason: "the merge rewrote lint"},
	}})
	cases := []decideCase{
		{"a claimed Requirement is captured whatever the choice says", batchNode(testRev, refused, false),
			Rows{Epic: epic, Checks: gatesPassed()},
			Action{Kind: ActCapture, IDs: []string{workID("capture R1")}, Items: []string{"R1"}}},
		{
			"the choice adds a Requirement no task touches",
			batchNode(testRev, added, false),
			Rows{Epic: epic, Checks: gatesPassed()},
			Action{
				Kind:  ActCapture,
				IDs:   []string{workID("capture R1"), workID("capture R2")},
				Items: []string{"R1", "R2"},
			},
		},
	}
	checkDecisions(t, Integrate{}, cases)
}

// TestIntegrateRoundVerification: a merged round starts its own verification step at its commit before its Done gates,
// waits while it runs and records it once it ended. A round whose choice is recorded captures its floor, the round's
// task's Requirements and those of the tasks composed before it, rather than every Requirement, and its failed round's
// output carries the choice's lines.
func TestIntegrateRoundVerification(t *testing.T) {
	t.Parallel()
	epic := conflictEpic()
	epic.Tasks[1].Contributes = []string{"R2"}
	run := batchNode("cand-1", unverifiedConflict, true)
	id := VerifyID(run.Run, 2, 1)
	merged := []store.IntegrateRound{recordedRound(1, RoundMerged, "m1", "", "")}
	chosen := withRecorded(unverifiedConflict, &Verification{Step: "v"}, []RoundVerification{{
		Task: 2, Round: 1, Verification: Verification{Step: id, Gates: []GateChoice{
			{Gate: "just check", Run: true, Reason: "code changed"}, {Gate: "just lint", Run: false, Reason: "x"},
		}},
	}})
	cases := []decideCase{
		{"a merged round with no choice starts its verification at its commit", run,
			Rows{Epic: epic, Rounds: merged},
			Action{Kind: ActVerify, IDs: []string{id}, Items: []string{VerifyStart, "2", "1"}, Revision: "m1"}},
		{"a running round verification waits", run,
			Rows{Epic: epic, Rounds: merged, Steps: []store.StepRow{{ID: id}}}, Action{Kind: ActNone, Revision: "m1"}},
		{"an ended round verification is recorded", run,
			Rows{Epic: epic, Rounds: merged, Steps: []store.StepRow{{ID: id, End: "done"}}},
			Action{Kind: ActVerify, IDs: []string{id}, Items: []string{VerifyRecord, "2", "1"}, Revision: "m1"}},
		{
			"a recorded round captures its floor alone",
			batchNode("cand-1", chosen, true),
			Rows{
				Epic:   epic,
				Rounds: merged,
				Checks: []store.NodeCheck{{ID: roundWork("done 1"), State: store.NodeEnded}},
			},
			Action{Kind: ActCapture, IDs: []string{roundWork("capture R2")}, Items: []string{"R2"}, Revision: "m1"},
		},
		{"a failed round's output carries its choice", batchNode("cand-1", chosen, true),
			Rows{
				Epic: epic, Rounds: merged,
				Checks:  []store.NodeCheck{{ID: roundWork("done 1"), State: store.NodeEnded, Exit: 2}},
				Triages: []store.GateTriage{changeTriaged(roundWork("done 1"))},
			},
			Action{
				Kind: ActTaskIntegrator, Items: []string{TaskFail, "1"}, Tasks: []int64{2},
				Output: "Done gate just check failed on commit m1: it exited 2.\n" + changeNote + "\n" +
					"verification ran Done gate just check: code changed\nverification skipped Done gate just lint: x",
			}},
	}
	checkDecisions(t, Integrate{}, cases)
	if want := store.NodeWorkID(testRunID, 1, "verify T2 1"); id != want {
		t.Errorf("VerifyID = %q, want %q", id, want)
	}
}

// TestVerificationLines: a fallback gives one line; a read choice gives a line for each gate and for each Requirement
// it decided, with the floor that decided it, a Requirement no one named gives none, and an ignored entry its own line.
// A long reason is cut.
func TestVerificationLines(t *testing.T) {
	t.Parallel()
	fallback := VerificationLines(Verification{Step: "v", Fallback: "it ended stopped", Gates: []GateChoice{
		{Gate: "check", Run: true, Floor: FloorFallback},
	}})
	if want := []string{
		"verification v fell back to every Done gate and the default captures: it ended stopped",
	}; !reflect.DeepEqual(fallback, want) {
		t.Errorf("fallback lines = %q, want %q", fallback, want)
	}
	long := strings.Repeat("x", 300)
	got := VerificationLines(Verification{
		Step: "v",
		Gates: []GateChoice{
			{Gate: "check", Run: true, Reason: "docs only", Floor: FloorAlways},
			{Gate: "web", Run: true, Floor: FloorUnmentioned},
			{Gate: "gone", Run: true, Floor: FloorUnresolved},
			{Gate: "android", Run: false, Reason: long},
		},
		Requirements: []RequirementChoice{
			{Requirement: "R1", Capture: true, Floor: FloorClaimed},
			{Requirement: "R2", Capture: false, Reason: "untouched"},
			{Requirement: "R3", Capture: false, Floor: FloorUnmentioned},
		},
		Ignored: []string{"gate lint, which is not a Done gate of the epic"},
	})
	want := []string{
		"verification ran Done gate check: it is declared always; the step said: docs only",
		"verification ran Done gate web: the verification step did not name it",
		"verification ran Done gate gone: it does not resolve, so it runs and fails as an unresolved gate does",
		"verification skipped Done gate android: " + strings.Repeat("x", verifyReasonMax-3) + "...",
		"verification captured R1: a task it verifies claims or contributes to it",
		"verification did not capture R2: untouched",
		"verification ignored gate lint, which is not a Done gate of the epic",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if slices.ContainsFunc(got, func(line string) bool { return strings.Contains(line, "passed") }) {
		t.Errorf("lines %q say a gate passed", got)
	}
}
