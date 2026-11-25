package interpret

import (
	"errors"
	"os/exec"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
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
// so the release stops at it. That holds while origin's main is in the candidate the step started from.
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
// so the candidate is the merge of origin's main the release integrator makes. It returns the candidate.
func startReleaseWithCandidate(t *testing.T, w *releaseWorld) string {
	t.Helper()
	w.createEpic(runEpic, store.PhaseClean, rollbackTagStep())
	w.pass()
	candidate := w.integrateClean(w.integratorID())
	if step := w.started(w.stepID(1)); step.Revision != candidate {
		t.Fatalf("step 1 start = %+v, want it started at candidate %s", step, candidate)
	}
	return candidate
}

// releaseStepStarted reports whether the world's fake started a step with id.
func (w *releaseWorld) releaseStepStarted(id string) bool {
	return slices.ContainsFunc(w.fakes.steps.calls, func(start enginehost.StepStart) bool { return start.ID == id })
}

// TestReleaseRepreparesCandidateOriginMovedPastIt: a release prepared against origin's main O1 stops at its first step.
// Origin then moves to O2, which the candidate does not contain, and ReleaseEpic resumes the release. The new attempt
// hands the merge of O2 into the tip to the release integrator, under the generation of a candidate prepared again,
// records the merge it makes as the candidate with no Done gate run, and starts the first step there. The candidate it
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
	if err := w.s.ReleaseEpic(w.t.Context(), runEpic, nil, store.Maker{}, runClock()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	w.pass()

	run := runAt(t, w.s, releaseNodePath)
	if run.Attempt != firstAttempt+1 {
		t.Fatalf("attempt = %d after the resume, want %d", run.Attempt, firstAttempt+1)
	}
	if merge := decodeMerge(t, run.Result); merge.Tip != tip || merge.Origin != second || run.Revision != "" {
		t.Fatalf("Result = %s with revision %q, want the merge of origin's main %s into the tip %s, with no candidate",
			run.Result, run.Revision, second, tip)
	}
	// The round runs under the candidate's generation, so the round the replaced candidate ran cannot decide it.
	round := w.generationID("integrator 1")
	if starts := w.integratorStarts(); len(starts) != 2 || starts[1].ID != round {
		t.Fatalf("integrator starts = %+v, want the round of generation 1 under %s", starts, round)
	}
	if w.releaseStepStarted(w.generationID("release 1")) {
		t.Fatalf(
			"step starts = %+v, want the first step not yet started at attempt %d",
			w.fakes.steps.calls,
			run.Attempt,
		)
	}

	prepared := w.integrateClean(round)
	if prepared == first || !containsCommit(t, w.repo, second, prepared) {
		t.Fatalf("candidate = %s, want a new candidate that contains origin's main %s, not %s", prepared, second, first)
	}
	if again := w.started(w.generationID("release 1")); again.Revision != prepared {
		t.Errorf("first step start = %+v, want it at the new candidate %s under generation 1", again, prepared)
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
	if len(w.fakes.checks.calls) != 0 {
		t.Errorf("checks = %+v, want no Done gate run on either candidate", w.fakes.checks.calls)
	}
}

// TestReleaseRepreparesKeepsCandidateOriginMainIsIn: a release stopped at its first step resumes after origin's main
// did not move. The candidate of the earlier attempt contains origin's main, so it is reused as it always was: the first
// step starts at it again, with no new candidate, no release integrator round, no Done gate and no verify run.
func TestReleaseRepreparesKeepsCandidateOriginMainIsIn(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	origin := mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	mergeOriginCommit(t, origin, "origin-1.txt")
	candidate := startReleaseWithCandidate(t, w)
	w.stopAtFirstStep()

	if err := w.s.ReleaseEpic(w.t.Context(), runEpic, nil, store.Maker{}, runClock()); err != nil {
		t.Fatalf("ReleaseEpic: %v", err)
	}
	w.pass()

	calls := w.fakes.steps.calls
	if len(calls) != 3 {
		t.Fatalf("step starts = %+v after the resume, want the integrator, the first step, then the first step again",
			calls)
	}
	if again := calls[2]; again.ID != w.stepID(1) || again.Revision != candidate {
		t.Errorf("first step start = %+v, want it at the reused candidate %s", again, candidate)
	}
	if starts := w.integratorStarts(); len(starts) != 1 {
		t.Errorf("integrator starts = %d after the resume, want no new round", len(starts))
	}
	if len(w.fakes.checks.calls) != 0 {
		t.Errorf("checks = %+v, want no Done gate run", w.fakes.checks.calls)
	}
	if verify := w.releaseVerifyRuns(); len(verify) != 0 {
		t.Errorf("verify runs = %+v, want none: origin's main is in the candidate", verify)
	}
	candidates := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartCandidate)
	if len(candidates) != 1 || candidates[0].Head != candidate {
		t.Errorf("candidate runs = %+v, want the one candidate %s", candidates, candidate)
	}
}

// generationID returns the id of part of the world's release run's work under generation 1, as the executor runs it
// once the run's candidate was prepared again at its attempt: the id ReleaseGatedID gives the run at that generation.
func (w *releaseWorld) generationID(part string) string {
	w.t.Helper()
	run := runAt(w.t, w.s, releaseNodePath)
	return store.NodeWorkID(store.ReleaseGatedID(run.ID, 1), run.Attempt, part)
}

// TestReleaseRepreparesSameAttemptCandidate: origin's main moves while the release integrator's round of the candidate
// this attempt prepared runs. The merge it makes is recorded as the candidate once it returns done, and then, before
// the first step starts, origin's main is found not in it. The attempt has the release integrator make the merge again
// under the next generation, records that merge, and starts the first step there. The candidate it replaces stays
// recorded, and is marked stale at the attempt.
func TestReleaseRepreparesSameAttemptCandidate(t *testing.T) {
	t.Parallel()
	w := newReleaseWorld(t, &recordingEnding{})
	origin := mergeRemote(t, w.repo)
	nodeEpicBranch(t, w.repo, "epic.txt")
	mergeOriginCommit(t, origin, "origin-1.txt")
	w.createEpic(runEpic, store.PhaseClean, rollbackTagStep())
	w.pass()
	run := runAt(t, w.s, releaseNodePath)
	if starts := w.integratorStarts(); len(starts) != 1 || starts[0].ID != w.integratorID() {
		t.Fatalf("integrator starts = %+v, want the round of the first candidate", starts)
	}

	second := mergeOriginCommit(t, origin, "origin-2.txt")
	first := w.integrateClean(w.integratorID())
	if w.releaseStepStarted(w.stepID(1)) {
		t.Fatalf("step starts = %+v after origin moved, want the first step not started at the stale candidate",
			w.fakes.steps.calls)
	}
	round := w.generationID("integrator 1")
	if starts := w.integratorStarts(); len(starts) != 2 || starts[1].ID != round {
		t.Fatalf("integrator starts = %+v, want the round of generation 1 under %s", starts, round)
	}

	prepared := w.integrateClean(round)
	if prepared == first || !containsCommit(t, w.repo, second, prepared) {
		t.Fatalf("candidate = %s, want a new candidate that contains origin's main %s, not %s", prepared, second, first)
	}
	if start := w.started(w.generationID("release 1")); start.Revision != prepared {
		t.Errorf("first step start = %+v, want it at the new candidate %s under generation 1", start, prepared)
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
	if len(w.fakes.checks.calls) != 0 {
		t.Errorf("checks = %+v, want no Done gate run on either candidate", w.fakes.checks.calls)
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
