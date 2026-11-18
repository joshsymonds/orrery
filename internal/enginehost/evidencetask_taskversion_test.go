package enginehost

import (
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// evidenceTaskGreenRunID is a second well-formed caller id for a task run, for the green run of the test that also runs
// the red one.
const evidenceTaskGreenRunID = "0123456789abcdef0123456789abcdef"

// TestTaskVersionRunTaskRunsTheCheckingVersionsItemAtRed: while task 1 has a version that changes its evidence and is
// checking, its red run runs the version's item, and its green run runs the item the task currently holds.
func TestTaskVersionRunTaskRunsTheCheckingVersionsItemAtRed(t *testing.T) {
	t.Parallel()
	repo, head := evidenceRunRepo(t, "exit 0\n")
	st := epicTestStore(t)
	evidenceRunSeed(t, st, repo, evidenceRunItem(), evidenceRunItem())
	checking := evidenceRunItem()
	checking.Conclusion = "the build passes at the base"
	err := st.RerunTask(t.Context(), evidenceRunEpic, 1, store.TaskBuild{}, store.TaskBuild{}, store.TaskVersionInput{
		Source: store.SourceOrchestrator, Step: "step-2", Reason: "the evidence moved", Evidence: &checking,
	}, nil, store.TaskDecision{ID: "T1-request-1", Text: "T1 rerun: the evidence moved"},
		time.Unix(1700000600, 0))
	if err != nil {
		t.Fatalf("RerunTask: %v", err)
	}
	steps := &evidenceRunSteps{judges: []evidenceRunJudge{evidenceRunHolds(), evidenceRunHolds()}}
	ev := evidenceRunNew(t, st, steps)

	red, err := ev.RunTask(t.Context(), evidenceTaskRunID, evidenceRunEpic, 1, head, evidence.PhaseRed)
	if err != nil {
		t.Fatalf("red RunTask: %v", err)
	}
	evidenceRunWantStarted(t, red, "T1", head, evidence.PhaseRed, evidence.SourceCaptured)
	if !reflect.DeepEqual(red.Spec, checking) {
		t.Fatalf("red run spec = %+v, want the checking version's item %+v", red.Spec, checking)
	}
	evidenceRunSettled(t, engineOf(ev), st, evidenceTaskRunID)

	green, err := ev.RunTask(t.Context(), evidenceTaskGreenRunID, evidenceRunEpic, 1, head, evidence.PhaseGreen)
	if err != nil {
		t.Fatalf("green RunTask: %v", err)
	}
	evidenceRunWantStarted(t, green, "T1", head, evidence.PhaseGreen, evidence.SourceCaptured)
	if want := evidenceRunItem(); !reflect.DeepEqual(green.Spec, want) {
		t.Fatalf("green run spec = %+v, want the task's current item %+v", green.Spec, want)
	}
	evidenceRunSettled(t, engineOf(ev), st, evidenceTaskGreenRunID)
}
