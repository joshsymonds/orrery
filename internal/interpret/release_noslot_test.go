package interpret

import (
	"fmt"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestReleaseNoSlot: two clean epics with release steps, in one repository, both move to releasing, and each starts its
// release step 1 while the other's step 1 has not ended, so neither waits on the other.
func TestReleaseNoSlot(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	nodeGit(t, w.repo, "branch", releaseSecondBranch, "main")
	w.createEpic(runEpic, store.PhaseClean, tagStep())
	w.createEpic(releaseSecondEpic, store.PhaseClean, tagStep())
	w.pass()
	w.pass()

	epics := []string{runEpic, releaseSecondEpic}
	for _, epic := range epics {
		if phase := w.flowOf(epic).Epic.Phase; phase != store.PhaseReleasing {
			t.Errorf("phase of epic %s = %q, want releasing", epic, phase)
		}
	}
	if got := len(w.fakes.steps.calls); got != len(epics) {
		t.Fatalf("step starts = %d, want one release step 1 for each epic", got)
	}
	for _, epic := range epics {
		id, started, ended := releaseStepState(t, w, epic, 1)
		if !started {
			t.Errorf("release step 1 of epic %s is not started, want it started while the other epic releases", epic)
		}
		if ended {
			t.Errorf("release step 1 of epic %s has ended, want it running: nothing ends a step in this test", epic)
		}
		if !slices.ContainsFunc(w.fakes.steps.calls, func(call enginehost.StepStart) bool { return call.ID == id }) {
			t.Errorf("step %s of epic %s was not started by the runner", id, epic)
		}
	}
}

// releaseStepState returns the id of release step position of epic's release run, and whether that step is stored and
// whether it has ended.
func releaseStepState(t *testing.T, w *releaseWorld, epic string, position int) (id string, started, ended bool) {
	t.Helper()
	flow := w.flowOf(epic)
	i := slices.IndexFunc(flow.Runs, func(run store.NodeRun) bool { return run.Path == releaseNodePath })
	if i < 0 {
		t.Fatalf("epic %s has no release node run", epic)
	}
	run := flow.Runs[i]
	id = store.NodeWorkID(run.ID, run.Attempt, fmt.Sprintf("release %d", position))
	j := slices.IndexFunc(flow.Steps, func(step store.StepRow) bool { return step.ID == id })
	if j < 0 {
		return id, false, false
	}
	return id, true, flow.Steps[j].End != ""
}
