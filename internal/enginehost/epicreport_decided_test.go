package enginehost

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestEpicReportTextNamesTheFindingsClosedByDecision asserts that the report's text lists, after its gaps, each finding
// an owner's answer closed, and that a report with none has no such section.
func TestEpicReportTextNamesTheFindingsClosedByDecision(t *testing.T) {
	t.Parallel()
	decided := store.LedgerFinding{
		ID: "conformance-1", Result: "confirmed", Closures: []store.Closure{{
			Finding: "conformance-1", Round: 1, Result: "closed",
			Reason: "closed by decision owner-Q2-keep-fix (answers Q2 with keep-fix)",
		}},
	}
	verified := store.LedgerFinding{
		ID: "test_parsimony-1", Result: "confirmed", Closures: []store.Closure{{
			Finding: "test_parsimony-1", Round: 1, Step: "close-step", Result: "closed", Reason: "the fix holds",
		}},
	}
	flow := store.EpicFlow{
		Epic: store.EpicRow{Phase: "released"},
		Review: store.Review{
			Frozen: "frozen-1", Ledger: []store.LedgerFinding{decided, verified},
			Rounds: []store.ReviewRound{{Round: 1, Outcome: "passed"}},
		},
	}
	const want = "Outcome: released\nShipped:\nNot shipped:\nGaps:\nClosed by decision:\n- conformance-1 in round 1\n"
	if got := epicReportText(flow, nil, nil); got != want {
		t.Errorf("epicReportText() = %q, want %q", got, want)
	}
	flow.Review.Ledger = []store.LedgerFinding{verified}
	const none = "Outcome: released\nShipped:\nNot shipped:\nGaps:\n"
	if got := epicReportText(flow, nil, nil); got != none {
		t.Errorf("epicReportText() with no decided finding = %q, want %q", got, none)
	}
}
