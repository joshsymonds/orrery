package interpret

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// rollbackTagStep returns the release step the re-prepare tests name: a tag on main that its undo takes back. A step
// whose undo is none would need naming before ReleaseEpic runs it again, and these tests resume without naming one.
func rollbackTagStep() store.EpicReleaseStep {
	return store.EpicReleaseStep{Step: "tag the release", Target: "main", Undo: "delete the tag"}
}

// containsCommit reports whether ancestor is an ancestor of descendant, or the commit itself, in the repository at repo.
func containsCommit(t *testing.T, repo, ancestor, descendant string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "merge-base", "--is-ancestor", ancestor, descendant)
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true
	case errors.As(err, &exit) && exit.ExitCode() == notAncestor:
		return false
	default:
		t.Fatalf("merge-base --is-ancestor %s %s: %v", ancestor, descendant, err)
		return false
	}
}

// stopAtFirstStep ends the first release step of the world's release as a step that returned blocked does, and passes,
// so the release stops at it.
func (w *releaseWorld) stopAtFirstStep() {
	w.t.Helper()
	w.endStep(w.stepID(1), "returned", "blocked", map[string]any{
		"status": "blocked", "evidence": "the tag was refused",
	})
	w.pass()
	if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
		w.t.Fatalf("epic phase = %q after the first step stopped, want stopped", phase)
	}
}

// releaseVerifyRuns returns the verify runs of the world's epic, in the order the store holds them.
func (w *releaseWorld) releaseVerifyRuns() []store.ReleaseRun {
	w.t.Helper()
	return releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartVerify)
}

// startReleaseWithCandidate stores the test epic clean with rollbackTagStep, and passes it until its candidate is
// recorded and its first step has started at that candidate. The caller has moved origin's main past the epic's branch,
// so the candidate is a merge of origin's main that its Done gate passes. It returns the candidate.
func startReleaseWithCandidate(t *testing.T, w *releaseWorld) string {
	t.Helper()
	w.createEpic(runEpic, store.PhaseClean, rollbackTagStep())
	w.pass()
	w.endGate(0)
	w.pass()
	candidate := runAt(t, w.s, releaseNodePath).Revision
	if got := len(w.fakes.steps.calls); got != 1 || w.fakes.steps.calls[0].Revision != candidate {
		t.Fatalf("step starts = %+v, want the first step started at candidate %s", w.fakes.steps.calls, candidate)
	}
	return candidate
}

// TestReleaseRepreparesCandidateOriginMovedPastIt: a release prepared against origin's main O1 stops at its first step.
// Origin then moves to O2, which the candidate does not contain, and ReleaseEpic resumes the release. The new attempt
// prepares a candidate from O2, runs the Done gate on it, records it, and starts the first step there. The candidate it
// replaces is marked stale at the new attempt, and the step is never started from it again.
func TestReleaseRepreparesCandidateOriginMovedPastIt(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	origin := mergeRemote(t, w.repo)
	tip := nodeEpicBranch(t, w.repo, "epic.txt")
	mergeOriginCommit(t, origin, "origin-1.txt")
	first := startReleaseWithCandidate(t, w)
	if first == tip {
		t.Fatalf("candidate = the tip %s, want a merge of origin's main", tip)
	}
	firstAttempt := runAt(t, w.s, releaseNodePath).Attempt
	w.stopAtFirstStep()

	second := mergeOriginCommit(t, origin, "origin-2.txt")
	nodeGit(t, w.repo, "fetch", "-q", "origin")
	if containsCommit(t, w.repo, second, first) {
		t.Fatalf("origin %s contains the candidate %s, want origin to have moved past it", second, first)
	}
	if err := w.s.ReleaseEpic(w.t.Context(), runEpic, nil, runClock()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	if run.Attempt != firstAttempt+1 {
		t.Fatalf("attempt = %d after the resume, want %d", run.Attempt, firstAttempt+1)
	}
	prepared := run.Revision
	if prepared == first || !containsCommit(t, w.repo, second, prepared) {
		t.Fatalf("candidate = %s, want a new candidate that contains origin's main %s, not %s", prepared, second, first)
	}
	// The gate runs under the candidate's generation, so the gate the replaced candidate ran at its attempt cannot decide it.
	replaced := store.NodeWorkID(run.ID, firstAttempt, "done 1")
	last := w.lastCheck()
	if last.Revision != prepared || last.ID == replaced || last.ID == workID(t, w.s, releaseNodePath, "done 1") {
		t.Fatalf("last Done gate check = %+v, want the gate run at the new candidate %s under its own id",
			last, prepared)
	}
	if got := len(w.fakes.steps.calls); got != 1 {
		t.Fatalf("step starts = %d before the gate ends, want the first step not yet started", got)
	}

	w.endLastCheck(0)
	w.pass()
	if got := len(w.fakes.steps.calls); got != 2 {
		t.Fatalf("step starts = %d, want the first step started again once the new candidate is recorded", got)
	}
	again := w.fakes.steps.calls[1]
	if again.ID != w.stepID(1) || again.Revision != prepared {
		t.Errorf("first step start = %+v, want it at the new candidate %s", again, prepared)
	}

	verify := w.releaseVerifyRuns()
	if len(verify) != 1 || verify[0].Head != first || verify[0].Attempt != run.Attempt || verify[0].Passed {
		t.Errorf("verify runs = %+v, want the one stale mark of candidate %s at attempt %d", verify, first, run.Attempt)
	}
	candidates := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartCandidate)
	if len(candidates) != 2 || candidates[1].Head != prepared || candidates[1].Attempt != run.Attempt ||
		!candidates[1].Passed {
		t.Errorf("candidate runs = %+v, want the new candidate %s recorded at attempt %d",
			candidates, prepared, run.Attempt)
	}
}

// TestReleaseRepreparesKeepsCandidateOriginMainIsIn: a release stopped at its first step resumes after origin's main
// did not move. The candidate of the earlier attempt contains origin's main, so it is reused as it always was: the first
// step starts at it again, with no new candidate, no Done gate and no verify run.
func TestReleaseRepreparesKeepsCandidateOriginMainIsIn(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	origin := mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	mergeOriginCommit(t, origin, "origin-1.txt")
	candidate := startReleaseWithCandidate(t, w)
	checks := len(w.fakes.checks.calls)
	w.stopAtFirstStep()

	if err := w.s.ReleaseEpic(w.t.Context(), runEpic, nil, runClock()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	w.pass()

	if got := len(w.fakes.steps.calls); got != 2 {
		t.Fatalf("step starts = %d after the resume, want the first step started again", got)
	}
	again := w.fakes.steps.calls[1]
	if again.ID != w.stepID(1) || again.Revision != candidate {
		t.Errorf("first step start = %+v, want it at the reused candidate %s", again, candidate)
	}
	if got := len(w.fakes.checks.calls); got != checks {
		t.Errorf("Done gate checks = %d after the resume, want the gate not run again", got)
	}
	if verify := w.releaseVerifyRuns(); len(verify) != 0 {
		t.Errorf("verify runs = %+v, want none: origin's main is in the candidate", verify)
	}
	candidates := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartCandidate)
	if len(candidates) != 1 || candidates[0].Head != candidate {
		t.Errorf("candidate runs = %+v, want the one candidate %s", candidates, candidate)
	}
}

// lastCheck returns the check the world's fake was last asked to run.
func (w *releaseWorld) lastCheck() store.NodeCheck {
	w.t.Helper()
	if len(w.fakes.checks.calls) == 0 {
		w.t.Fatalf("no check was run, want one")
	}
	return w.fakes.checks.calls[len(w.fakes.checks.calls)-1]
}

// endLastCheck ends the check the world's fake was last asked to run with exit, whatever its id is.
func (w *releaseWorld) endLastCheck(exit int) {
	w.t.Helper()
	nodeEndCheck(w.t, w.s, w.lastCheck().ID, exit, "")
}

// TestReleaseRepreparesSameAttemptCandidate: origin's main moves while the Done gates of the candidate this attempt
// prepared run. The candidate is recorded once they pass, and then, before the first step starts, origin's main is found
// not in it. The attempt prepares the candidate again, walks its Done gates under ids of their own, records it, and starts
// the first step there. The candidate it replaces stays recorded, and is marked stale at the attempt.
func TestReleaseRepreparesSameAttemptCandidate(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	origin := mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	mergeOriginCommit(t, origin, "origin-1.txt")
	w.createEpic(runEpic, store.PhaseClean, rollbackTagStep())
	w.pass()
	run := runAt(t, w.s, releaseNodePath)
	first := run.Revision
	firstGate := w.lastCheck()
	if firstGate.Revision != first || firstGate.ID != workID(t, w.s, releaseNodePath, "done 1") {
		t.Fatalf("first Done gate check = %+v, want the gate run at candidate %s", firstGate, first)
	}

	second := mergeOriginCommit(t, origin, "origin-2.txt")
	w.endGate(0)
	w.pass()
	if got := len(w.fakes.steps.calls); got != 0 {
		t.Fatalf("step starts = %d after origin moved, want the first step not started at the stale candidate", got)
	}

	prepared := runAt(t, w.s, releaseNodePath).Revision
	if prepared == first || !containsCommit(t, w.repo, second, prepared) {
		t.Fatalf("candidate = %s, want a new candidate that contains origin's main %s, not %s", prepared, second, first)
	}
	last := w.lastCheck()
	if last.Revision != prepared || last.ID == firstGate.ID {
		t.Fatalf("last Done gate check = %+v, want the gate run at %s under an id of its own", last, prepared)
	}

	w.endLastCheck(0)
	w.pass()
	if got := len(w.fakes.steps.calls); got != 1 {
		t.Fatalf("step starts = %d, want the first step started once the new candidate is recorded", got)
	}
	if start := w.fakes.steps.calls[0]; start.ID != w.stepID(1) || start.Revision != prepared {
		t.Errorf("first step start = %+v, want it at the new candidate %s", start, prepared)
	}

	verify := w.releaseVerifyRuns()
	if len(verify) != 1 || verify[0].Head != first || verify[0].Attempt != run.Attempt || verify[0].Passed {
		t.Errorf("verify runs = %+v, want the one stale mark of candidate %s at attempt %d", verify, first, run.Attempt)
	}
	candidates := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartCandidate)
	if len(candidates) != 2 || candidates[0].Head != first || candidates[0].Position != 0 ||
		candidates[1].Head != prepared || candidates[1].Position != 1 || !candidates[1].Passed {
		t.Errorf("candidate runs = %+v, want %s at position 0 and %s at position 1, both passed",
			candidates, first, prepared)
	}
}

// TestReleaseRepreparesKeepsCandidateOfThisAttemptOriginMainIsIn: a candidate this attempt recorded is checked before the
// first step starts, and origin's main is still in it. The first step starts at it, with no mark and no new candidate.
func TestReleaseRepreparesKeepsCandidateOfThisAttemptOriginMainIsIn(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	origin := mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	mergeOriginCommit(t, origin, "origin-1.txt")
	candidate := startReleaseWithCandidate(t, w)

	if verify := w.releaseVerifyRuns(); len(verify) != 0 {
		t.Errorf("verify runs = %+v, want none: origin's main is in the candidate", verify)
	}
	candidates := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartCandidate)
	if len(candidates) != 1 || candidates[0].Head != candidate || candidates[0].Position != 0 {
		t.Errorf("candidate runs = %+v, want the one candidate %s", candidates, candidate)
	}
}
