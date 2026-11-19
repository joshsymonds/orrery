package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// rerunReviewer is the reviewer the tests rerun.
const rerunReviewer = "test_parsimony"

// rerunDecided fails t unless RerunReview of rerunReviewer over run and rows gives want.
func rerunDecided(t *testing.T, run store.NodeRun, rows Rows, want Action) {
	t.Helper()
	if got := RerunReview(run, rerunReviewer, rows); !reflect.DeepEqual(got, want) {
		t.Errorf("RerunReview() = %+v, want %+v", got, want)
	}
}

func TestRerunReviewPreparesWithoutRevision(t *testing.T) {
	t.Parallel()
	rows := Rows{Reviewers: []Reviewer{{Name: "floor", Verify: true}, {Name: "test_parsimony"}}}
	rerunDecided(t, nodeWith("", false).Run, rows, Action{Kind: ActPrepare})
}

func TestRerunReviewStartsOnlyItsReviewer(t *testing.T) {
	t.Parallel()
	run := nodeWith(testRev, false).Run
	rows := Rows{Reviewers: []Reviewer{{Name: "floor", Verify: true}, {Name: "test_parsimony"}}}
	id := reviewTryID(run, "reviewer 2", 1)
	rerunDecided(t, run, rows, Action{
		Kind:  ActStart,
		IDs:   []string{id},
		Units: []Unit{{ID: id, Part: "reviewer 2", Reviewer: "test_parsimony"}},
	})
}

func TestRerunReviewEndsWithItsVerdictAndFindings(t *testing.T) {
	t.Parallel()
	run := nodeWith(testRev, false).Run
	rows := Rows{
		Reviewers: []Reviewer{{Name: "floor", Verify: true}, {Name: "test_parsimony"}},
		Steps: []store.StepRow{
			{ID: reviewTryID(run, "reviewer 2", 1), End: "gate_failed", Result: reviewOneFindingReturn},
		},
		Review: store.Review{Ledger: []store.LedgerFinding{reviewUnverifiedAt("floor", 1, 1)}},
	}
	rerunDecided(t, run, rows, Action{
		Kind: ActEnd, Outcome: outcomePassed, Output: "test_parsimony: fails",
		Verdicts: []store.ReviewVerdict{{
			Position: 2, Reviewer: "test_parsimony", Step: reviewTryID(run, "reviewer 2", 1), Verdict: "fails",
		}},
		Ledger: []store.LedgerFinding{reviewUnverifiedAt("test_parsimony", 1, 2)},
	})
}

func TestRerunReviewGivesGapAfterTwoCostlyEnds(t *testing.T) {
	t.Parallel()
	run := nodeWith(testRev, false).Run
	rows := Rows{
		Reviewers: []Reviewer{{Name: "floor", Verify: true}, {Name: "test_parsimony"}},
		Steps: []store.StepRow{
			{ID: reviewTryID(run, "reviewer 2", 1), End: "overtime", Reason: "ran long"},
			{ID: reviewTryID(run, "reviewer 2", 2), End: "failed", Reason: "exit 2"},
		},
	}
	rerunDecided(t, run, rows, Action{
		Kind: ActEnd, Outcome: outcomePassed, Output: "test_parsimony: gap",
		Verdicts: []store.ReviewVerdict{{
			Position: 2, Reviewer: "test_parsimony", Step: reviewTryID(run, "reviewer 2", 2), Verdict: "gap",
			Reason: "reviewer 2 gave no verdict twice: failed: exit 2",
		}},
	})
}

func TestRerunReviewEndsFailedForReviewerNotEnabled(t *testing.T) {
	t.Parallel()
	rows := Rows{Epic: store.EpicRow{ID: "epic-1"}, Reviewers: []Reviewer{{Name: "floor", Verify: true}}}
	rerunDecided(t, nodeWith(testRev, false).Run, rows, Action{
		Kind: ActEnd, Outcome: outcomeFailed, Output: "reviewer test_parsimony is not enabled for epic epic-1",
	})
}
