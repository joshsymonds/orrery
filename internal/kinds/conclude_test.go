package kinds

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

var _ Kind = Conclude{}

// The epic the conclude tests end, and the stored base it ends clean at.
const (
	concludeEpicID = "epic-1"
	concludeBase   = "base-1"
)

// concludeRows returns the rows of a conclude run over review, of the test epic.
func concludeRows(review store.Review) Rows {
	return Rows{Epic: store.EpicRow{ID: concludeEpicID, StoredBase: concludeBase}, Review: review}
}

// concludeEnd returns the end of a conclude run in phase with output. A conclusion always ends passed.
func concludeEnd(phase, output string) Action {
	return Action{Kind: ActEnd, Outcome: outcomePassed, Phase: phase, Output: output}
}

func TestConcludeDecide(t *testing.T) {
	t.Parallel()
	node := nodeWith(testRev, false)
	fails := store.ReviewVerdict{Position: 1, Reviewer: "security", Verdict: "fails"}

	undetermined := closureFinding(1, "undetermined")
	undetermined.Reason = "cannot tell"

	undeterminedClosure := closureFinding(1, "confirmed",
		store.Closure{Finding: "security-1", Round: 1, Step: "close-1", Result: "undetermined", Reason: "cannot tell"})

	openTwice := closureFinding(2, "confirmed",
		store.Closure{Finding: "security-2", Round: 1, Result: "open"},
		store.Closure{Finding: "security-2", Round: 2, Result: "open"})

	openOnce := closureFinding(2, "confirmed", store.Closure{Finding: "security-2", Round: 1, Result: "open"})

	checkDecisions(t, Conclude{}, []decideCase{
		{
			"no review ran ends with gaps",
			node,
			concludeRows(store.Review{}),
			concludeEnd(phaseEndedWithGaps, "Ended with 1 gap:\n- no review ran"),
		},
		{
			"all holds end clean at the stored base",
			node,
			concludeRows(store.Review{
				Frozen:   testRev,
				Verdicts: []store.ReviewVerdict{{Position: 1, Reviewer: "security", Verdict: "holds"}},
			}),
			concludeEnd(phaseClean, "epic epic-1 ended clean at base-1"),
		},
		{
			"dropped findings end clean",
			node,
			concludeRows(store.Review{
				Frozen:   testRev,
				Verdicts: []store.ReviewVerdict{fails},
				Ledger:   []store.LedgerFinding{closureFinding(1, "dropped")},
			}),
			concludeEnd(phaseClean, "epic epic-1 ended clean at base-1"),
		},
		{
			"a confirmed finding closed in its round ends clean",
			node,
			concludeRows(store.Review{
				Frozen:   testRev,
				Verdicts: []store.ReviewVerdict{fails},
				Ledger: []store.LedgerFinding{closureFinding(1, "confirmed",
					store.Closure{Finding: "security-1", Round: 1, Step: "close-1", Result: "closed"})},
				Rounds: []store.ReviewRound{{Round: 1, Revision: testRev, Outcome: outcomePassed}},
			}),
			concludeEnd(phaseClean, "epic epic-1 ended clean at base-1"),
		},
		{
			"a confirmed finding open in an earlier round and closed later ends clean",
			node,
			concludeRows(store.Review{
				Frozen:   testRev,
				Verdicts: []store.ReviewVerdict{fails},
				Ledger: []store.LedgerFinding{closureFinding(2, "confirmed",
					store.Closure{Finding: "security-2", Round: 1, Result: "open"},
					store.Closure{Finding: "security-2", Round: 2, Step: "close-2", Result: "closed"})},
				Rounds: []store.ReviewRound{
					{Round: 1, Revision: testRev, Outcome: outcomeFailed},
					{Round: 2, Revision: testRev, Outcome: outcomePassed},
				},
			}),
			concludeEnd(phaseClean, "epic epic-1 ended clean at base-1"),
		},
		{
			"a gap verdict ends with gaps naming the reviewer",
			node,
			concludeRows(store.Review{
				Frozen: testRev,
				Verdicts: []store.ReviewVerdict{{
					Position: 1, Reviewer: "security", Verdict: "gap",
					Reason: "security gave no verdict twice: done: boom",
				}},
			}),
			concludeEnd(phaseEndedWithGaps,
				"Ended with 1 gap:\n- reviewer security: security gave no verdict twice: done: boom"),
		},
		{
			"an undetermined verifier ends with gaps naming the finding",
			node,
			concludeRows(store.Review{
				Frozen:   testRev,
				Verdicts: []store.ReviewVerdict{fails},
				Ledger:   []store.LedgerFinding{undetermined},
			}),
			concludeEnd(phaseEndedWithGaps,
				"Ended with 1 gap:\n- security-1: verifier undetermined: cannot tell"),
		},
		{
			"an undetermined closure ends with gaps naming its round, and is not also open",
			node,
			concludeRows(store.Review{
				Frozen:   testRev,
				Verdicts: []store.ReviewVerdict{fails},
				Ledger:   []store.LedgerFinding{undeterminedClosure},
				Rounds:   []store.ReviewRound{{Round: 1, Revision: testRev, Outcome: outcomePassed}},
			}),
			concludeEnd(phaseEndedWithGaps,
				"Ended with 1 gap:\n- security-1: closure undetermined in round 1: cannot tell"),
		},
		{
			"a finding open after every round ends with gaps naming the rounds",
			node,
			concludeRows(store.Review{
				Frozen:   testRev,
				Verdicts: []store.ReviewVerdict{fails},
				Ledger:   []store.LedgerFinding{openTwice},
				Rounds: []store.ReviewRound{
					{Round: 1, Revision: testRev, Outcome: outcomeFailed},
					{Round: 2, Revision: testRev, Outcome: outcomeFailed},
				},
			}),
			concludeEnd(phaseEndedWithGaps, "Ended with 1 gap:\n- security-2: open after 2 rounds"),
		},
		{
			"gaps join in reviewer order, then ledger order",
			node,
			concludeRows(store.Review{
				Frozen: testRev,
				Verdicts: []store.ReviewVerdict{
					fails,
					{Position: 2, Reviewer: "perf", Verdict: "gap", Reason: "perf reason"},
				},
				Ledger: []store.LedgerFinding{undetermined, openOnce},
				Rounds: []store.ReviewRound{{Round: 1, Revision: testRev, Outcome: outcomeFailed}},
			}),
			concludeEnd(phaseEndedWithGaps,
				"Ended with 3 gaps:\n- reviewer perf: perf reason\n"+
					"- security-1: verifier undetermined: cannot tell\n- security-2: open after 1 rounds"),
		},
	})
}

func TestLedgerCause(t *testing.T) {
	t.Parallel()
	confirmed := closureFinding(1, "confirmed")
	confirmed.Reason = "the claim holds"
	unverified := reviewUnverifiedAt("perf", 1, 3)
	closedInRound1 := closureFinding(1, "confirmed", store.Closure{Finding: "security-1", Round: 1, Result: "closed"})
	openInRound1 := closureFinding(2, "confirmed", store.Closure{Finding: "security-2", Round: 1, Result: "open"})
	openInRound1.Reason = "the claim holds"
	undeterminedInRound1 := closureFinding(3, "confirmed",
		store.Closure{Finding: "security-3", Round: 1, Result: "undetermined"})

	tests := []struct {
		name   string
		review store.Review
		round  int64
		want   string
	}{
		{
			name:  "round 1 with no ledger names nothing to correct",
			round: 1,
			want:  "ledger round 1: no confirmed findings; nothing to correct",
		},
		{
			name:   "round 1 with only dropped findings names nothing to correct",
			review: store.Review{Ledger: []store.LedgerFinding{closureFinding(1, "dropped")}},
			round:  1,
			want:   "ledger round 1: no confirmed findings; nothing to correct",
		},
		{
			name: "round 1 lists each confirmed finding with its fields",
			review: store.Review{Ledger: []store.LedgerFinding{
				confirmed, closureFinding(2, "dropped"), unverified,
			}},
			round: 1,
			want: "ledger round 1: open findings to correct\n" +
				"security-1 (security): claim: claim 1; citation: file1.go:1; location: file1.go; " +
				"evidence: seen 1; reason: the claim holds\n" +
				"perf-1 (perf): claim: claim 1; citation: file1.go:1; location: file1.go; " +
				"evidence: seen 1; reason: unverified: reviewer perf has verify off",
		},
		{
			name: "round 2 lists only what round 1 left open",
			review: store.Review{Ledger: []store.LedgerFinding{
				closedInRound1, openInRound1, undeterminedInRound1,
			}},
			round: 2,
			want: "ledger round 2: open findings to correct\n" +
				"security-2 (security): claim: claim 2; citation: file2.go:2; location: file2.go; " +
				"evidence: seen 2; reason: the claim holds",
		},
		{
			name:   "round 2 with nothing left open names nothing to correct",
			review: store.Review{Ledger: []store.LedgerFinding{closedInRound1}},
			round:  2,
			want:   "ledger round 2: no open findings; nothing to correct",
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
