package kinds

import (
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

var _ Kind = Conclude{}

// The epic the conclude tests end, and the stored base it ends clean at.
const (
	concludeEpicID = "epic-1"
	concludeBase   = "base-1"
)

// concludeDeclared is the reason the latest turn gives for each gap declare declares unresolvable.
const concludeDeclared = "the planner cannot resolve it"

// concludeRows returns the rows of a conclude run over review, of the test epic.
func concludeRows(review store.Review) Rows {
	return Rows{Epic: store.EpicRow{ID: concludeEpicID, StoredBase: concludeBase}, Review: review}
}

// declare returns rows whose latest turn declares each gap of keys unresolvable, recommending correct.
func declare(rows Rows, keys ...string) Rows {
	for _, key := range keys {
		rows.Unresolvable = append(rows.Unresolvable, store.GapDeclaration{
			Gap: key, Reason: concludeDeclared, Recommended: store.ChoiceCorrect,
		})
	}
	return rows
}

// concludeClean returns the end of a conclude run that ends clean with output. A conclusion always ends passed.
func concludeClean(output string) Action {
	return Action{Kind: ActEnd, Outcome: outcomePassed, Phase: phaseClean, Output: output}
}

// gapsEnd returns the end of a conclude run that ends with gaps: its output, and the question it opens.
func gapsEnd(output string, question store.GapsQuestion) Action {
	return Action{Kind: ActEnd, Outcome: outcomePassed, Phase: phaseEndedWithGaps, Output: output, Question: question}
}

// correctFor returns the gaps question of entries, which recommends correct for the gaps of keys.
func correctFor(entries []string, keys ...string) store.GapsQuestion {
	return store.GapsQuestion{
		Gaps: entries, Recommended: store.ChoiceCorrect, Why: "correct for: " + strings.Join(keys, ", "),
	}
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
			declare(concludeRows(store.Review{}), "review"),
			gapsEnd(
				EndingBody(phaseEndedWithGaps, []string{"no review ran"}, nil),
				correctFor([]string{"no review ran (planner: the planner cannot resolve it)"}, "review"),
			),
		},
		{
			"all holds end clean at the stored base",
			node,
			concludeRows(store.Review{
				Frozen:   testRev,
				Verdicts: []store.ReviewVerdict{{Position: 1, Reviewer: "security", Verdict: "holds"}},
			}),
			concludeClean("epic epic-1 ended clean at base-1"),
		},
		{
			"dropped findings end clean",
			node,
			concludeRows(store.Review{
				Frozen:   testRev,
				Verdicts: []store.ReviewVerdict{fails},
				Ledger:   []store.LedgerFinding{closureFinding(1, "dropped")},
			}),
			concludeClean("epic epic-1 ended clean at base-1"),
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
			concludeClean("epic epic-1 ended clean at base-1"),
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
			concludeClean("epic epic-1 ended clean at base-1"),
		},
		{
			"a gap verdict ends with gaps naming the reviewer",
			node,
			declare(concludeRows(store.Review{
				Frozen: testRev,
				Verdicts: []store.ReviewVerdict{{
					Position: 1, Reviewer: "security", Verdict: "gap",
					Reason: "security gave no verdict twice: done: boom",
				}},
			}), "reviewer:security"),
			gapsEnd(
				EndingBody(
					phaseEndedWithGaps,
					[]string{"reviewer security: security gave no verdict twice: done: boom"},
					nil,
				),
				correctFor([]string{
					"reviewer security: security gave no verdict twice: done: boom " +
						"(planner: the planner cannot resolve it)",
				}, "reviewer:security"),
			),
		},
		{
			"an undetermined verifier ends with gaps naming the finding",
			node,
			declare(concludeRows(store.Review{
				Frozen:   testRev,
				Verdicts: []store.ReviewVerdict{fails},
				Ledger:   []store.LedgerFinding{undetermined},
			}), "verify:security-1"),
			gapsEnd(
				EndingBody(phaseEndedWithGaps, []string{"security-1: verifier undetermined: cannot tell"}, nil),
				correctFor([]string{
					"security-1: verifier undetermined: cannot tell (planner: the planner cannot resolve it)",
				}, "verify:security-1"),
			),
		},
		{
			"an undetermined closure ends with gaps naming its round, and is not also open",
			node,
			declare(concludeRows(store.Review{
				Frozen:   testRev,
				Verdicts: []store.ReviewVerdict{fails},
				Ledger:   []store.LedgerFinding{undeterminedClosure},
				Rounds:   []store.ReviewRound{{Round: 1, Revision: testRev, Outcome: outcomePassed}},
			}), "closure:security-1:1"),
			gapsEnd(
				EndingBody(
					phaseEndedWithGaps,
					[]string{"security-1: closure undetermined in round 1: cannot tell"},
					nil,
				),
				correctFor([]string{
					"security-1: closure undetermined in round 1: cannot tell (planner: the planner cannot resolve it)",
				}, "closure:security-1:1"),
			),
		},
		{
			"a finding open after every round ends with gaps naming the rounds",
			node,
			declare(concludeRows(store.Review{
				Frozen:   testRev,
				Verdicts: []store.ReviewVerdict{fails},
				Ledger:   []store.LedgerFinding{openTwice},
				Rounds: []store.ReviewRound{
					{Round: 1, Revision: testRev, Outcome: outcomeFailed},
					{Round: 2, Revision: testRev, Outcome: outcomeFailed},
				},
			}), "open:security-2"),
			gapsEnd(
				EndingBody(phaseEndedWithGaps, []string{"security-2: open after 2 rounds"}, nil),
				correctFor([]string{
					"security-2: open after 2 rounds (planner: the planner cannot resolve it)",
				}, "open:security-2"),
			),
		},
		{
			"gaps join in reviewer order, then ledger order",
			node,
			declare(concludeRows(store.Review{
				Frozen: testRev,
				Verdicts: []store.ReviewVerdict{
					fails,
					{Position: 2, Reviewer: "perf", Verdict: "gap", Reason: "perf reason"},
				},
				Ledger: []store.LedgerFinding{undetermined, openOnce},
				Rounds: []store.ReviewRound{{Round: 1, Revision: testRev, Outcome: outcomeFailed}},
			}), "reviewer:perf", "verify:security-1", "open:security-2"),
			gapsEnd(EndingBody(phaseEndedWithGaps, []string{
				"reviewer perf: perf reason",
				"security-1: verifier undetermined: cannot tell",
				"security-2: open after 1 rounds",
			}, nil), correctFor([]string{
				"reviewer perf: perf reason (planner: the planner cannot resolve it)",
				"security-1: verifier undetermined: cannot tell (planner: the planner cannot resolve it)",
				"security-2: open after 1 rounds (planner: the planner cannot resolve it)",
			}, "reviewer:perf", "verify:security-1", "open:security-2")),
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
		store.Closure{Finding: "security-3", Round: 1, Result: "undetermined", Reason: "cannot tell"})

	tests := []struct {
		name   string
		review store.Review
		round  int64
		want   string
	}{
		{
			name:   "round 1 with no ledger names nothing to correct",
			review: store.Review{Frozen: testRev},
			round:  1,
			want:   "ledger round 1: no confirmed findings; nothing to correct",
		},
		{
			name:   "round 1 with only dropped findings names nothing to correct",
			review: store.Review{Frozen: testRev, Ledger: []store.LedgerFinding{closureFinding(1, "dropped")}},
			round:  1,
			want:   "ledger round 1: no confirmed findings; nothing to correct",
		},
		{
			name: "round 1 lists each confirmed finding with its fields",
			review: store.Review{Frozen: testRev, Ledger: []store.LedgerFinding{
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
			review: store.Review{Frozen: testRev, Ledger: []store.LedgerFinding{
				closedInRound1, openInRound1, undeterminedInRound1,
			}},
			round: 2,
			want: "ledger round 2: open findings to correct\n" +
				"security-2 (security): claim: claim 2; citation: file2.go:2; location: file2.go; " +
				"evidence: seen 2; reason: the claim holds\n" +
				"gap closure:security-3:1: security-3: closure undetermined in round 1: cannot tell",
		},
		{
			name:   "round 2 with nothing left open names nothing to correct",
			review: store.Review{Frozen: testRev, Ledger: []store.LedgerFinding{closedInRound1}},
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
