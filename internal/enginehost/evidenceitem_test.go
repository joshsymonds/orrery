package enginehost

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// evidenceItemRunID is a well-formed caller id for a Requirement run: 32 lowercase hex digits.
const evidenceItemRunID = "0123456789abcdef0123456789abcdef"

// TestEvidenceRunItemRunsOnceUnderID runs Requirement R1 of a seeded epic at a revision under a caller id. The run settles
// with Item R1, the id, the Requirement's item as its spec, and passed. A second run under the same id returns the stored run
// and starts no judge.
func TestEvidenceRunItemRunsOnceUnderID(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	requirement := evidenceRunItem("report.txt")
	evidenceRunSeed(t, st, repo, requirement, evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)

	started, err := ev.RunItem(t.Context(), evidenceItemRunID, evidenceRunEpic, "R1", head, evidence.PhaseGreen)
	if err != nil {
		t.Fatalf("RunItem: %v", err)
	}
	evidenceRunWantStarted(t, started, "R1", head, evidence.PhaseGreen, evidence.SourceCaptured)
	evidenceRunAttach(t, ev, st, started)
	if started.ID != evidenceItemRunID {
		t.Fatalf("started run id = %q, want %q", started.ID, evidenceItemRunID)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, evidenceItemRunID)
	if got.Item != "R1" || got.ID != evidenceItemRunID || got.Verdict != evidence.VerdictHolds || !got.Passed {
		t.Fatalf("run = item %q id %q verdict %q passed %t, want R1, the id, holds, passed",
			got.Item, got.ID, got.Verdict, got.Passed)
	}
	if !reflect.DeepEqual(got.Spec, requirement) {
		t.Fatalf("run spec = %+v, want the Requirement's item %+v", got.Spec, requirement)
	}

	again, err := ev.RunItem(t.Context(), evidenceItemRunID, evidenceRunEpic, "R1", head, evidence.PhaseGreen)
	if err != nil {
		t.Fatalf("second RunItem: %v", err)
	}
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("second RunItem = %+v, want the stored run %+v", again, got)
	}
	if n := steps.startCount(); n != 1 {
		t.Fatalf("%d judge steps started, want 1", n)
	}
}

// TestEvidenceRunItemRefusesABadRequest asks for each kind of bad request on a seeded epic. Each is refused with an error
// matching ErrEvidenceRefused that names the bad value where it has one, starts no judge, and stores no run.
func TestEvidenceRunItemRefusesABadRequest(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	item := evidenceRunItem()
	evidenceRunSeed(t, st, repo, item, item)
	steps := &evidenceRunSteps{}
	ev := evidenceRunNew(t, st, steps)
	refusals := []struct {
		name, id, item, phase string
		named                 string
	}{
		{"uppercase id", strings.ToUpper(evidenceItemRunID), "R1", evidence.PhaseGreen, ""},
		{"short id", evidenceItemRunID[1:], "R1", evidence.PhaseGreen, ""},
		{"long id", evidenceItemRunID + "0", "R1", evidence.PhaseGreen, ""},
		{"non-hex id", evidenceItemRunID[:31] + "g", "R1", evidence.PhaseGreen, ""},
		{"empty id", "", "R1", evidence.PhaseGreen, ""},
		{"task item", evidenceItemRunID, "T1", evidence.PhaseGreen, "T1"},
		{"lowercase item", evidenceItemRunID, "r1", evidence.PhaseGreen, "r1"},
		{"unknown requirement", evidenceItemRunID, "R9", evidence.PhaseGreen, "R9"},
		{"legacy requirement", evidenceItemRunID, "R2", evidence.PhaseGreen, "R2"},
		{"bad phase", evidenceItemRunID, "R1", "blue", "blue"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ev.RunItem(t.Context(), tc.id, evidenceRunEpic, tc.item, head, tc.phase)
			if !errors.Is(err, ErrEvidenceRefused) {
				t.Fatalf("RunItem = %v, want an error matching ErrEvidenceRefused", err)
			}
			if tc.named != "" && !strings.Contains(err.Error(), tc.named) {
				t.Fatalf("RunItem = %v, want the error to name %q", err, tc.named)
			}
			evidenceRunWantNoRuns(t, st)
			if n := steps.startCount(); n != 0 {
				t.Fatalf("%d judge steps started, want none", n)
			}
		})
	}
}
