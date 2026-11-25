package interpret

import (
	"errors"
	"io/fs"
	"os"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// startMainMovedRelease stores the release epic runEpic with its release reviewer enabled and tagStep as its one
// release step, and passes it until its candidate is held by the reviewer and its first step has started at that
// candidate. Origin gets one commit beyond main first, so the candidate is the merge of it the release integrator
// makes. It returns the candidate and the reviewer the world runs.
func startMainMovedRelease(t *testing.T, w *releaseWorld, origin string) (string, *releaseReviewers) {
	t.Helper()
	nodeEpicBranch(t, w.repo, "epic.txt")
	mergeOriginCommit(t, origin, "origin-1.txt")
	reviewer := &releaseReviewers{}
	w.runner.reviewers = reviewer
	w.createReviewedEpic(defaultReleaseReviewer(t, true), tagStep())
	w.pass()
	candidate := w.integrateClean(w.integratorID())
	w.endStep(w.reviewID(), "done", "done", map[string]any{"status": "done"})
	w.pass()
	calls := w.fakes.steps.calls
	if len(calls) != 3 || calls[0].ID != w.integratorID() || calls[1].ID != w.reviewID() ||
		calls[2].ID != w.stepID(1) || calls[2].Revision != candidate {
		t.Fatalf("step starts = %+v, want the release integrator, the release reviewer, then the first step "+
			"started at %s", calls, candidate)
	}
	return candidate, reviewer
}

// TestReleaseMainMoved: a release whose first step fails after origin's main moved past its candidate is not stopped
// by that failure. The candidate is marked stale at the attempt, so the release prepares it again: its release
// integrator merges origin's main again and finds the release reviewer's earlier verdict no longer stands, so the
// reviewer holds the merge again, both under the new generation, with no Done gate run, and its first step starts again
// under the id of that generation. A first step that fails while origin's main is unmoved still stops the release.
func TestReleaseMainMoved(t *testing.T) {
	t.Parallel()
	t.Run("step 1 fails after origin's main moved past the candidate", func(t *testing.T) {
		t.Parallel()
		w := newReleaseWorld(t, &recordingEnding{})
		origin := mergeRemote(t, w.repo)
		candidate, reviewer := startMainMovedRelease(t, w, origin)
		attempt := runAt(t, w.s, releaseNodePath).Attempt
		moved := mergeOriginCommit(t, origin, "origin-2.txt")
		nodeGit(t, w.repo, "fetch", "-q", "origin")
		if containsCommit(t, w.repo, moved, candidate) {
			t.Fatalf("candidate %s contains origin's main %s, want origin to have moved past it", candidate, moved)
		}

		w.endStep(w.stepID(1), "returned", "blocked", map[string]any{
			"status": "blocked", "evidence": "the tag was refused",
		})
		w.pass()

		wantReason := "release step 1 failed after origin's main moved past candidate " + candidate +
			": step ended returned with status blocked"
		verify := w.releaseVerifyRuns()
		if len(verify) != 1 || verify[0].Head != candidate || verify[0].Attempt != attempt || verify[0].Passed ||
			verify[0].Reason != wantReason {
			t.Errorf("verify runs = %+v, want the one mark of candidate %s at attempt %d with reason %q",
				verify, candidate, attempt, wantReason)
		}
		flow := w.flowOf(runEpic)
		if failed := releaseRunsOf(flow.Releases, store.ReleasePartStep); len(failed) != 0 {
			t.Errorf("step runs = %+v, want none: the failure is not a failure of the release", failed)
		}
		if flow.Epic.Phase != store.PhaseReleasing {
			t.Errorf("epic phase = %q, want releasing: the release goes on", flow.Epic.Phase)
		}
		if _, err := os.Stat(w.stepWorktree(w.stepID(1))); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("first step worktree stat err = %v, want it removed", err)
		}

		if merge := decodeMerge(t, runAt(t, w.s, releaseNodePath).Result); merge.Origin != moved {
			t.Fatalf("Result origin = %s, want origin's main %s merged again", merge.Origin, moved)
		}
		round := w.generationID("integrator 1")
		if starts := w.integratorStarts(); len(starts) != 2 || starts[1].ID != round {
			t.Fatalf("integrator starts = %+v, want the round of generation 1 under %s", starts, round)
		}
		prepared := w.integrateCleanSaying(round, reviewSaid(false, "origin-2.txt is new beside what the tag reads"))
		if prepared == candidate || !containsCommit(t, w.repo, moved, prepared) {
			t.Fatalf("candidate = %s, want a new candidate that contains origin's main %s", prepared, moved)
		}

		candidates := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartCandidate)
		if len(candidates) != 2 || candidates[1].Head != prepared || candidates[1].Attempt != attempt ||
			!candidates[1].Passed {
			t.Errorf("candidate runs = %+v, want %s recorded passed at attempt %d", candidates, prepared, attempt)
		}
		if len(reviewer.calls) != 2 || reviewer.calls[1].revision != prepared {
			t.Fatalf("reviewer starts = %+v, want the release reviewer run again over %s", reviewer.calls, prepared)
		}
		review := w.generationID("review " + releaseReviewerName)
		if got := w.fakes.steps.calls; len(got) != 5 || got[4].ID != review || got[4].Revision != prepared {
			t.Fatalf("step starts = %+v, want the reviewer started under generation 1 at %s", got, prepared)
		}

		w.endStep(review, "done", "done", map[string]any{"status": "done"})
		w.pass()

		reviews := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartReview)
		if len(reviews) != 2 || !reviews[1].Passed || reviews[1].Head != prepared || reviews[1].Position != 2 {
			t.Errorf("review runs = %+v, want the reviewer's pass of %s at position 2", reviews, prepared)
		}
		again := w.fakes.steps.calls
		stepID := w.generationID("release 1")
		if len(again) != 6 || again[5].ID != stepID || again[5].Revision != prepared ||
			again[5].Task != releaseNodePath {
			t.Fatalf("step starts = %+v, want the first step started again under %s at %s", again, stepID, prepared)
		}
		flow = w.flowOf(runEpic)
		if !slices.ContainsFunc(flow.Steps, func(row store.StepRow) bool { return row.ID == stepID }) {
			t.Errorf("steps = %+v, want the first step's row under %s read", flow.Steps, stepID)
		}
		if state := store.ReleaseProofs(flow)[0].State; state != store.ReleaseRunning {
			t.Errorf("release step 1 state = %q, want running under its generation id", state)
		}
		if got := len(w.fakes.checks.calls); got != 0 {
			t.Errorf("Done gate checks = %d, want none at either candidate", got)
		}
	})
	t.Run("step 1 fails while origin's main is unmoved", func(t *testing.T) {
		t.Parallel()
		w := newReleaseWorld(t, &recordingEnding{})
		origin := mergeRemote(t, w.repo)
		candidate, _ := startMainMovedRelease(t, w, origin)

		w.endStep(w.stepID(1), "returned", "blocked", map[string]any{
			"status": "blocked", "evidence": "the tag was refused",
		})
		w.pass()

		runs := releaseRunsOf(w.flowOf(runEpic).Releases, store.ReleasePartStep)
		if len(runs) != 1 || runs[0].Passed || runs[0].Position != 1 ||
			runs[0].Reason != "step ended returned with status blocked" {
			t.Errorf("step runs = %+v, want the one failed run of step 1 with its reason", runs)
		}
		flow := w.flowOf(runEpic)
		want := "release step 1 (tag the release) failed: step ended returned with status blocked"
		if flow.Epic.Phase != store.PhaseStopped || flow.Epic.Parked != want {
			t.Errorf("epic phase = %q and parked = %q, want stopped and parked with %q",
				flow.Epic.Phase, flow.Epic.Parked, want)
		}
		if verify := w.releaseVerifyRuns(); len(verify) != 0 {
			t.Errorf("verify runs = %+v, want none: origin's main is in the candidate", verify)
		}
		if candidates := releaseRunsOf(flow.Releases, store.ReleasePartCandidate); len(candidates) != 1 ||
			candidates[0].Head != candidate {
			t.Errorf("candidate runs = %+v, want the one candidate %s", candidates, candidate)
		}
		if got := len(w.fakes.steps.calls); got != 3 {
			t.Errorf("step starts = %d, want no start after the stop", got)
		}
	})
}
