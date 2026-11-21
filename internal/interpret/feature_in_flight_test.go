package interpret

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestFeatureInFlightEpicIntegratesAndReleases covers R3: an epic created before task branches, whose two accepted tasks
// are pinned at refs/orrery/epic-runner/T<n> with no task branch under refs/heads/orrery/, integrates its batch into a
// candidate that holds both revisions, advances epic/epic-runner to it, and then, once its plan completes and its review
// settles clean, releases: with no release steps it ends done through the store as its Ending.
func TestFeatureInFlightEpicIntegratesAndReleases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newIntegrateWorld(t, pinnedTo(builtinPin(t)), viaRunnerPass())
	// The runner is given the review's seams, so its review, closure and conclude kinds run, and the store is the Ending.
	cfg := nodeConfig(w.s, w.fakes)
	cfg.Reviewers, cfg.Ending = runnerReviewers{}, w.s
	w.runner = New(cfg)
	w.runner.logger = slog.New(slog.NewTextHandler(w.logs, nil))

	w.pass()
	w.endRed()
	w.pass()
	endCost(t, w.s)
	w.pass()

	first := w.commit(w.base, "internal/a/x.go", "one\n")
	second := w.commit(w.base, "b.go", "two\n")
	completeTurn(t, w.s, runPlan("complete"))
	w.pass()
	w.git("update-ref", "refs/orrery/epic-runner/T1", first)
	w.git("update-ref", "refs/orrery/epic-runner/T2", second)
	w.accept(1, w.base, first)
	w.accept(2, w.base, second)
	w.pass()
	candidate := w.candidate(integrateBatch)
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	w.pass()

	if run := runAt(t, w.s, integrateBatch); run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Errorf("batch = %+v, want ended passed", run)
	}
	if epic := epicOf(t, w.s); epic.StoredBase != candidate || epic.Candidate != "" {
		t.Errorf("stored base %q and candidate %q, want %q and none", epic.StoredBase, epic.Candidate, candidate)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != candidate {
		t.Errorf("branch = %s, want the candidate %s", tip, candidate)
	}
	if got := w.git("diff", "--name-only", w.base, candidate); got != "b.go\ninternal/a/x.go" {
		t.Errorf("paths the candidate changes over the base = %q, want both tasks' paths", got)
	}
	if got := w.git("rev-parse", "refs/orrery/epic-runner/T1"); got != first {
		t.Errorf("pin T1 = %s, want %s", got, first)
	}
	if got := w.git("rev-parse", "refs/orrery/epic-runner/T2"); got != second {
		t.Errorf("pin T2 = %s, want %s", got, second)
	}

	// The review is settled clean at the candidate: its frozen revision is seeded there, as the release tests seed a
	// clean epic's review, and a holding verdict is seeded for its reviewer. The review then runs for real.
	freezeReviewRow(t, w.path, runEpic, candidate)
	seedVerdict(t, w.path, runEpic, store.ReviewVerdict{
		Position: 1, Reviewer: "security", Step: "review security 1", Verdict: "holds",
	})
	completeTurn(t, w.s, store.EpicPlan{Decisions: []string{}, Outline: "the batch is green", Outcome: "complete"})
	reviewLoopDrive(t, w.s, w.runner, nil, reviewLoopPlanTurn)
	if w.logs.Len() > 0 {
		t.Fatalf("pass logged an error: %s", w.logs.String())
	}

	if plan := runAt(t, w.s, "plan"); plan.State != store.NodeEnded || plan.Outcome != "complete" {
		t.Errorf("plan = %+v, want ended complete", plan)
	}
	if review := runAt(t, w.s, "review"); review.State != store.NodeEnded || review.Outcome != "passed" {
		t.Errorf("review = %+v, want ended passed", review)
	}
	read, err := w.s.EpicReview(ctx, runEpic)
	if err != nil {
		t.Fatalf("EpicReview: %v", err)
	}
	if read.Clean != candidate || read.Frozen != candidate || len(read.Ledger) != 0 {
		t.Errorf("review clean %q, frozen %q and ledger %+v, want clean and frozen at %s with no ledger",
			read.Clean, read.Frozen, read.Ledger, candidate)
	}
	if release := runAt(t, w.s, releaseNodePath); release.State != store.NodeEnded {
		t.Errorf("release = %+v, want it begun and ended", release)
	}
	if phase := epicOf(t, w.s).Phase; phase != "done" {
		t.Errorf("epic phase = %q, want done", phase)
	}
	if want := kinds.EndingBody("done", nil, nil); !slices.Contains(nodeTells(t, w.path), want) {
		t.Errorf("tells = %q, want %q among them", nodeTells(t, w.path), want)
	}
	if got := w.git("for-each-ref", "refs/heads/orrery/"); got != "" {
		t.Errorf("task branches = %q, want none", got)
	}
}
