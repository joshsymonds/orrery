package kinds

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

func TestGapListKeysEachGapSource(t *testing.T) {
	t.Parallel()
	undetermined := closureFinding(1, "undetermined")
	undetermined.Reason = "cannot tell"
	undeterminedClosure := closureFinding(2, "confirmed", store.Closure{
		Finding: "security-2", Round: 1, Step: "close-2", Result: "undetermined", Reason: "still unclear",
	})
	openEveryRound := closureFinding(3, "confirmed", store.Closure{Finding: "security-3", Round: 1, Result: "open"})
	review := store.Review{
		Verdicts: []store.ReviewVerdict{{
			Position: 1, Reviewer: "test_parsimony", Verdict: "gap", Reason: "gave no verdict",
		}},
		Ledger: []store.LedgerFinding{undetermined, undeterminedClosure, openEveryRound},
		Rounds: []store.ReviewRound{{Round: 1, Revision: testRev, Outcome: outcomeFailed}},
	}
	tasks := []store.EpicTask{{
		Seq: 4, Status: taskGap, Requirements: []string{"R1"}, Build: store.TaskBuild{Failure: "boom"},
	}}

	got := GapList(review, tasks)
	gotKeys, gotReasons := make([]string, 0, len(got)), make([]string, 0, len(got))
	for _, gap := range got {
		gotKeys = append(gotKeys, gap.Key)
		gotReasons = append(gotReasons, gap.Reason)
	}
	wantKeys := []string{
		"review", "reviewer:test_parsimony", "verify:security-1", "closure:security-2:1", "open:security-3", "task:T4",
	}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("GapList() keys = %q, want %q", gotKeys, wantKeys)
	}
	wantReasons := slices.Concat(Gaps(review), TaskGaps(tasks))
	if !slices.Equal(gotReasons, wantReasons) {
		t.Errorf("GapList() reasons = %q, want the lines Gaps and TaskGaps print, %q", gotReasons, wantReasons)
	}
}

func TestGapCauseHoldsReasonAndLedger(t *testing.T) {
	t.Parallel()
	confirmed := closureFinding(1, "confirmed",
		store.Closure{Finding: "security-1", Round: 1, Result: "closed", Reason: "closed by fix"})
	confirmed.Claim = "the claim"
	confirmed.Reason = "the claim holds"
	verdicts := []store.ReviewVerdict{
		{Position: 1, Reviewer: "security", Verdict: "holds"},
		{Position: 2, Reviewer: "test_parsimony", Verdict: "gap", Reason: "gave no verdict twice"},
	}

	tests := []struct {
		name   string
		review store.Review
		want   string
	}{
		{
			name: "a ledger holds each finding and its closures",
			review: store.Review{
				Frozen: testRev, Verdicts: verdicts, Ledger: []store.LedgerFinding{confirmed},
				Rounds: []store.ReviewRound{{Round: 1, Revision: testRev, Outcome: outcomePassed}},
			},
			want: "gap reviewer:test_parsimony: reviewer test_parsimony: gave no verdict twice\n" +
				"verdicts:\n" +
				"- security: holds\n" +
				"- test_parsimony: gap: gave no verdict twice\n" +
				"ledger:\n" +
				"- security-1 (security): confirmed: claim: the claim; reason: the claim holds\n" +
				"  closure round 1: closed: closed by fix",
		},
		{
			name:   "an empty ledger says so",
			review: store.Review{Frozen: testRev, Verdicts: verdicts},
			want: "gap reviewer:test_parsimony: reviewer test_parsimony: gave no verdict twice\n" +
				"verdicts:\n" +
				"- security: holds\n" +
				"- test_parsimony: gap: gave no verdict twice\n" +
				"ledger: empty",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gaps := GapList(tc.review, nil)
			if len(gaps) != 1 {
				t.Fatalf("GapList() = %+v, want the one reviewer gap", gaps)
			}
			if got := GapCause(gaps[0], tc.review); got != tc.want {
				t.Errorf("GapCause() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLedgerCauseListsReviewGaps(t *testing.T) {
	t.Parallel()
	gapOnly := store.Review{Frozen: testRev, Verdicts: []store.ReviewVerdict{{
		Position: 1, Reviewer: "test_parsimony", Verdict: "gap", Reason: "gave no verdict twice",
	}}}
	holdsOnly := store.Review{Frozen: testRev, Verdicts: []store.ReviewVerdict{{
		Position: 1, Reviewer: "security", Verdict: "holds",
	}}}

	tests := []struct {
		name   string
		review store.Review
		round  int64
		want   string
	}{
		{
			name:   "round 1 with only a reviewer gap names the gap to reconcile",
			review: gapOnly,
			round:  1,
			want: "ledger round 1: no open findings; gaps to reconcile\n" +
				"gap reviewer:test_parsimony: reviewer test_parsimony: gave no verdict twice",
		},
		{
			name:   "round 1 with no finding and no gap names nothing to correct",
			review: holdsOnly,
			round:  1,
			want:   "ledger round 1: no confirmed findings; nothing to correct",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := LedgerCause(tc.review, tc.round); got != tc.want {
				t.Errorf("LedgerCause() = %q, want %q", got, tc.want)
			}
		})
	}
}
