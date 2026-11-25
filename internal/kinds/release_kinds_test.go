package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// kindStep returns a release step that names kind, with undo as its undo.
func kindStep(kind, undo string) store.EpicReleaseStep {
	return store.EpicReleaseStep{Step: kind + " the release", Target: "origin", Undo: undo, Kind: kind}
}

// TestReleaseKindSteps: a release step that names a kind is decided from its check, not from an agent step. It starts
// when it has no check, waits while its check runs, and is recorded once its check has ended. A release whose every step
// names a kind ends released after its last step passes, with no judge; one with a step that names no kind is judged.
func TestReleaseKindSteps(t *testing.T) {
	t.Parallel()
	coded := releasingEpic(kindStep("push-main", "push a revert"), kindStep("tag", "delete the tag"))
	mixed := releasingEpic(kindStep("push-main", "push a revert"), releaseStep("install on the phone", "reinstall"))
	candidate := candidateRun(1, "cand-1")
	stepID := attemptWorkID(1, "release 1")
	passed := func(position int64) store.ReleaseRun {
		return store.ReleaseRun{
			Part:     store.ReleasePartStep,
			Position: position,
			Attempt:  1,
			Head:     "cand-1",
			Passed:   true,
		}
	}
	cases := []decideCase{
		{
			"a step that names a kind with no check starts",
			releaseNode(1, "", ""),
			Rows{Epic: coded, Releases: []store.ReleaseRun{candidate}},
			Action{Kind: ActStep, IDs: []string{stepID}, Items: []string{"release 1", "cand-1"}},
		},
		{
			"a step that names a kind waits while its check runs",
			releaseNode(1, "", ""),
			Rows{
				Epic: coded, Releases: []store.ReleaseRun{candidate},
				Checks: []store.NodeCheck{{ID: stepID, State: store.NodeRunning}},
			},
			Action{Kind: ActNone},
		},
		{
			"a step that names a kind is recorded once its check has ended",
			releaseNode(1, "", ""),
			Rows{
				Epic: coded, Releases: []store.ReleaseRun{candidate},
				Checks: []store.NodeCheck{{ID: stepID, State: store.NodeEnded, Exit: 1}},
			},
			Action{Kind: ActRecord, IDs: []string{stepID}, Items: []string{"release 1", "cand-1"}},
		},
		{
			"a step that names a kind is not decided by an agent step under its id",
			releaseNode(1, "", ""),
			Rows{
				Epic: coded, Releases: []store.ReleaseRun{candidate},
				Steps: []store.StepRow{{ID: stepID, End: "done"}},
			},
			Action{Kind: ActStep, IDs: []string{stepID}, Items: []string{"release 1", "cand-1"}},
		},
		{
			"a release whose every step names a kind ends released with no judge",
			releaseNode(1, "", ""),
			Rows{Epic: coded, Releases: []store.ReleaseRun{candidate, passed(1), passed(2)}, Review: releaseFrozen()},
			Action{Kind: ActEnding, Outcome: outcomeReleased, Output: EndingBody(phaseReleased, nil, nil)},
		},
		{
			"a release with a step that names no kind is judged",
			releaseNode(1, "", ""),
			Rows{Epic: mixed, Releases: []store.ReleaseRun{candidate, passed(1), passed(2)}},
			Action{Kind: ActJudge, IDs: []string{attemptWorkID(1, "judge")}, Items: []string{"judge", "cand-1"}},
		},
	}
	checkDecisions(t, Release{}, cases)
}
