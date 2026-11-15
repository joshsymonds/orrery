package enginehost

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// evidenceTaskRunID is a well-formed caller id for a task run: 32 lowercase hex digits.
const evidenceTaskRunID = "fedcba9876543210fedcba9876543210"

// TestEvidenceRunTaskRunsOnceUnderID runs task T1 of a seeded epic at a revision under a caller id. The run settles with
// Item T1, the id and passed. A second run under the same id returns the stored run and starts no judge.
func TestEvidenceRunTaskRunsOnceUnderID(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)

	started, err := ev.RunTask(t.Context(), evidenceTaskRunID, evidenceRunEpic, 1, head, evidence.PhaseGreen)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	evidenceRunWantStarted(t, started, "T1", head, evidence.PhaseGreen, evidence.SourceCaptured)
	if started.ID != evidenceTaskRunID {
		t.Fatalf("started run id = %q, want %q", started.ID, evidenceTaskRunID)
	}
	got := evidenceRunSettled(t, engineOf(ev), st, evidenceTaskRunID)
	if got.Item != "T1" || got.ID != evidenceTaskRunID || got.Verdict != evidence.VerdictHolds || !got.Passed {
		t.Fatalf("run = item %q id %q verdict %q passed %t, want T1, the id, holds, passed",
			got.Item, got.ID, got.Verdict, got.Passed)
	}

	again, err := ev.RunTask(t.Context(), evidenceTaskRunID, evidenceRunEpic, 1, head, evidence.PhaseGreen)
	if err != nil {
		t.Fatalf("second RunTask: %v", err)
	}
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("second RunTask = %+v, want the stored run %+v", again, got)
	}
	if n := steps.startCount(); n != 1 {
		t.Fatalf("%d judge steps started, want 1", n)
	}
}

// TestEvidenceRunTaskRefusesABadRequest asks for each kind of bad request on a seeded task. Each is refused with an error
// matching ErrEvidenceRefused that names the bad value where it has one, starts no judge, and stores no run.
func TestEvidenceRunTaskRefusesABadRequest(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	item := evidenceRunItem()
	evidenceRunSeed(t, st, repo, item, item)
	steps := &evidenceRunSteps{}
	ev := evidenceRunNew(t, st, steps)
	refusals := []struct {
		name, id, phase string
		task            int64
		named           string
	}{
		{"uppercase id", strings.ToUpper(evidenceTaskRunID), evidence.PhaseGreen, 1, ""},
		{"short id", evidenceTaskRunID[1:], evidence.PhaseGreen, 1, ""},
		{"long id", evidenceTaskRunID + "0", evidence.PhaseGreen, 1, ""},
		{"non-hex id", evidenceTaskRunID[:31] + "g", evidence.PhaseGreen, 1, ""},
		{"empty id", "", evidence.PhaseGreen, 1, ""},
		{"unknown task", evidenceTaskRunID, evidence.PhaseGreen, 9, "T9"},
		{"bad phase", evidenceTaskRunID, "blue", 1, "blue"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ev.RunTask(t.Context(), tc.id, evidenceRunEpic, tc.task, head, tc.phase)
			if !errors.Is(err, ErrEvidenceRefused) {
				t.Fatalf("RunTask = %v, want an error matching ErrEvidenceRefused", err)
			}
			if tc.named != "" && !strings.Contains(err.Error(), tc.named) {
				t.Fatalf("RunTask = %v, want the error to name %q", err, tc.named)
			}
			evidenceRunWantNoRuns(t, st)
			if n := steps.startCount(); n != 0 {
				t.Fatalf("%d judge steps started, want none", n)
			}
		})
	}
}

// TestEvidenceRunTaskRefusesAZeroItem runs a task whose planner turn gave it no evidence item. The run is refused with an
// error matching ErrEvidenceRefused that names the task, and no run is stored.
func TestEvidenceRunTaskRefusesAZeroItem(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidence.Item{})
	steps := &evidenceRunSteps{}
	ev := evidenceRunNew(t, st, steps)

	_, err := ev.RunTask(t.Context(), evidenceTaskRunID, evidenceRunEpic, 1, head, evidence.PhaseGreen)
	if !errors.Is(err, ErrEvidenceRefused) || !strings.Contains(err.Error(), "T1") {
		t.Fatalf("RunTask = %v, want an error matching ErrEvidenceRefused that names T1", err)
	}
	evidenceRunWantNoRuns(t, st)
	if n := steps.startCount(); n != 0 {
		t.Fatalf("%d judge steps started, want none", n)
	}
}
