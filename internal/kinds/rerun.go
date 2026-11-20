package kinds

import (
	"fmt"
	"slices"

	"github.com/joshsymonds/orrery/internal/store"
)

// RerunReview decides a rerun of the end-of-epic reviewer named reviewer, over run's steps. A reviewer that is not
// enabled for the epic ends the run failed. A run with no revision is prepared first, as a review run is. Otherwise
// the walk settles only that reviewer, at its position p (its index in rows.Reviewers plus 1): its try chain, and the
// verifier chains of the findings it raises, exactly as the review kind walks them. Its parts read "reviewer <p>" and
// "verify <p> <n>", and its step ids come from reviewTryID. The walk returns what the review kind's shared decision
// returns. Its end holds the reviewer's one verdict, at position p, and the findings it raised as the ledger. Their
// positions run on from the highest position among the ledger's findings by other reviewers, or from 1 when there are
// none, in the order the walk raised them.
func RerunReview(run store.NodeRun, reviewer string, rows Rows) Action {
	index := slices.IndexFunc(rows.Reviewers, func(enabled Reviewer) bool { return enabled.Name == reviewer })
	if index < 0 {
		return Action{
			Kind: ActEnd, Outcome: outcomeFailed,
			Output: fmt.Sprintf("reviewer %s is not enabled for epic %s", reviewer, rows.Epic.ID),
		}
	}
	if run.Revision == "" {
		return prepare(rows)
	}
	pass := reviewPass{run: run, rows: rows, after: reviewLedgerHighest(rows.Review.Ledger, reviewer)}
	pass.settleReviewer(index+1, rows.Reviewers[index])
	return pass.decide()
}

// reviewLedgerHighest returns the highest position among the findings of ledger that reviewer did not raise, or 0 when
// there are none.
func reviewLedgerHighest(ledger []store.LedgerFinding, reviewer string) int64 {
	var highest int64
	for _, finding := range ledger {
		if finding.Reviewer != reviewer && finding.Position > highest {
			highest = finding.Position
		}
	}
	return highest
}
