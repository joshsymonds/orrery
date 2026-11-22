package build

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// sharedFile is a file another task of the epic holds, which an implementer's needs_context request names.
const sharedFile = "internal/other/shared.go"

// TestSharedFilesAcceptedRunTimeRequest checks that an implementer's needs_context request for a file another task
// holds is granted by rule, as a free file is: a file an open task owns, and a file an accepted task changed and has
// not integrated, are each granted, and the implementer runs again at implement.
func TestSharedFilesAcceptedRunTimeRequest(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		peer Peer
	}{
		{"an open task owns the file", openPeer(4, sharedFile)},
		{"an accepted task changed the file and has not integrated", acceptedPeer(4, sharedFile)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			view := needsFilesView("needs_context", repairReturn(`["`+sharedFile+`"]`))
			view.Peers = []Peer{tc.peer}
			got := Decide(view)
			if got.Kind != ActGrant {
				t.Fatalf("decision = %s, want a grant of %s by rule", got.Kind, sharedFile)
			}
			if !reflect.DeepEqual(got.NeedsFiles, []string{sharedFile}) {
				t.Errorf("granted files = %v, want [%s]", got.NeedsFiles, sharedFile)
			}
			if got.Next.Stage != StageImplement {
				t.Errorf("build at stage %s, want the implementer rerun at %s", got.Next.Stage, StageImplement)
			}
		})
	}
}

// TestSharedFilesAcceptedRunTimeRequestThroughTheRunner runs an implementer's needs_context request through the runner.
// Task 2 is accepted and not yet integrated, and its change touched the file task 1's implementer names, so the file is
// held by it and is granted to task 1 by rule: task 1 gains the file and its build is at implement.
func TestSharedFilesAcceptedRunTimeRequestThroughTheRunner(t *testing.T) {
	t.Parallel()
	plan := passTwoTaskPlan()
	plan.Tasks[1].Files = []string{"internal/other/b.go"}
	f := newPlanFixture(t, plan)
	identify(t, f.repo)
	acceptChanging(t, f, 2, sharedFile)
	first := []int64{1}
	f.runner.PassTasks(f.ctx, passEpic, first)
	f.addRun(RunID(passEpic, 1, StageRed, 0, 0), evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.runner.PassTasks(f.ctx, passEpic, first)
	f.steps.rows[passStep(StageImplement, 1, 0)] = store.StepRow{
		ID: passStep(StageImplement, 1, 0), End: endReturned, Status: statusNeedsContext,
		Result: repairReturn(`["` + sharedFile + `"]`),
	}
	f.runner.PassTasks(f.ctx, passEpic, first)
	task := f.task()
	if want := []string{"thing.go", sharedFile}; !reflect.DeepEqual(task.Files, want) {
		t.Errorf("Files = %v, want %v: the file an accepted task changed was granted by rule", task.Files, want)
	}
	if task.Build.Stage != StageImplement {
		t.Errorf("build at stage %s, want the implementer rerun at %s", task.Build.Stage, StageImplement)
	}
}
