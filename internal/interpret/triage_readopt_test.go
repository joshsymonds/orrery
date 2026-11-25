package interpret

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/hostexec"
	"github.com/joshsymonds/orrery/internal/kinds"
)

// TestTriageReadoptedStepKeepsItsReturnCheck: a triage step re-adopted after a restart gets the return check its start
// gave it, kinds.TriageFailures against the check it triages, its key lines included: it passes the quote and the
// citation the start's check passes, and refuses what that check refuses. A triage row whose check is not stored is
// an error, so it is not re-adopted.
func TestTriageReadoptedStepKeepsItsReturnCheck(t *testing.T) {
	t.Parallel()
	w, candidate, _ := longGateWorld(t)
	start := w.triageAt(candidate)
	row, err := w.s.Step(t.Context(), start.ID)
	if err != nil {
		t.Fatalf("Step %s: %v", start.ID, err)
	}
	readopt := ReadoptCheck(w.s, hostexec.LocalHosts(), w.runner.getenv)
	check, err := readopt(t.Context(), row)
	if err != nil || check == nil {
		t.Fatalf("ReadoptCheck of the triage row = %v, %v; want the triage's return check", check != nil, err)
	}
	for _, tc := range []struct {
		name     string
		ret      json.RawMessage
		failures int
	}{
		{"the load line", triageReturn(t, kinds.TriageEnvironment, loadLine, ""), 0},
		{"the failing test", triageReturn(t, kinds.TriageChange, "", longFailingTest), 0},
		{"a paraphrase", triageReturn(t, kinds.TriageEnvironment, "the load was high", ""), 1},
		{"an unknown verdict", json.RawMessage(`{"verdict":"maybe"}`), 1},
	} {
		got, want := check(tc.ret), start.ReturnCheck(tc.ret)
		if !slices.Equal(got, want) || len(got) != tc.failures {
			t.Errorf("re-adopted check of %s = %q, want %d failures as the start's check gives: %q", tc.name, got,
				tc.failures, want)
		}
	}

	row.ID = "0123456789abcdef0123456789abcdef"
	if other, otherErr := readopt(t.Context(), row); other != nil || otherErr == nil {
		t.Errorf("ReadoptCheck of a triage row that triages no stored check = %v, %v; want an error", other != nil,
			otherErr)
	}
}

// TestTriageReadoptedRoundStepKeepsItsReturnCheck: the triage of a Done gate a task integrator round ran at its merged
// commit, under the round's own ids, is re-adopted with its return check too.
func TestTriageReadoptedRoundStepKeepsItsReturnCheck(t *testing.T) {
	t.Parallel()
	w := newRoundWorld(t)
	head := w.mergeRound(w.roundStart(1), "both\n")
	endTaskStep(t, w.s, w.roundID(1), "done", "done", taskReturn(verdictMerged, head, ""))
	w.pass()
	w.endGateAt(head, 1, "lint: unused import\n")
	w.pass()
	start := w.triageAt(head)
	row, err := w.s.Step(t.Context(), start.ID)
	if err != nil {
		t.Fatalf("Step %s: %v", start.ID, err)
	}
	check, err := ReadoptCheck(w.s, hostexec.LocalHosts(), w.runner.getenv)(t.Context(), row)
	if err != nil || check == nil {
		t.Fatalf("ReadoptCheck of the round's triage row = %v, %v; want the triage's return check", check != nil, err)
	}
	cited := triageReturn(t, kinds.TriageChange, "", "lint: unused import")
	invented := triageReturn(t, kinds.TriageChange, "", "lint: unused variable")
	if got := check(cited); len(got) != 0 {
		t.Errorf("re-adopted check of the gate's line = %q, want it to pass", got)
	}
	if got := check(invented); len(got) != 1 {
		t.Errorf("re-adopted check of a line the gate did not print = %q, want one failure", got)
	}
}
