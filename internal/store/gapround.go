package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// StartGapRound stores run as StartNodes stores a running node run, adds each of triggers as a pending trigger created
// at at unless a trigger with its id is stored in any state, and records each of turns as a gap turn of epic at at, in
// one write. A turn's Turn is the conclude gap round the gap was given, 1 or 2, and not a planner turn seq. A turn whose
// gap and turn are recorded already is left as it is. A turn whose gap already has GapTurnBound other turns makes the
// write fail with an error wrapping ErrGapSpent, and nothing is written. A repeat of the same call changes nothing.
func (s *Store) StartGapRound(
	ctx context.Context, epic string, run NodeRun, triggers []NodeTrigger, turns []GapTurn, at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := insertNodeRuns(ctx, queries, epic, []NodeRun{run}, at); err != nil {
			return err
		}
		for _, trigger := range triggers {
			if err := insertNodeTrigger(ctx, queries, epic, trigger, at); err != nil {
				return err
			}
		}
		for _, turn := range turns {
			if err := insertGapTurn(ctx, queries, epic, turn.Gap, turn.Turn, turn.Reason, at); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReplaceReviewerVerdict replaces, in one write, the stored verdict of epic's review by verdict.Reviewer with verdict,
// and that reviewer's ledger findings, with their closures, with findings, each at its Position. Every other reviewer's
// verdict and findings are kept. A review with no verdict by that reviewer is an error, and writes nothing. Neither a
// verdict nor a finding holds a time, so the write does not store the second it is made at.
func (s *Store) ReplaceReviewerVerdict(
	ctx context.Context, epic string, verdict ReviewVerdict, findings []LedgerFinding, _ time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return replaceReviewerVerdict(ctx, queries, epic, verdict, findings)
	})
}

// EndReviewRerun ends, in one write, the running run end names of epic as EndNodes ends it, and replaces the stored
// verdict and findings of the review's reviewer as ReplaceReviewerVerdict does. The rerun of a reviewer whose gap a
// planner turn fixed ends with the verdict and findings its try chain settled. A review with no verdict by that reviewer
// is an error, and the write leaves the run running, as it writes nothing.
func (s *Store) EndReviewRerun(
	ctx context.Context, epic string, end NodeEnd, verdict ReviewVerdict, findings []LedgerFinding, at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := endNodeRuns(ctx, queries, epic, []NodeEnd{end}, at); err != nil {
			return err
		}
		return replaceReviewerVerdict(ctx, queries, epic, verdict, findings)
	})
}

// replaceReviewerVerdict replaces, in the caller's write, the stored verdict of epic's review by verdict.Reviewer with
// verdict, and that reviewer's ledger findings, with their closures, with findings, as ReplaceReviewerVerdict describes.
func replaceReviewerVerdict(
	ctx context.Context, queries *sqlcgen.Queries, epic string, verdict ReviewVerdict, findings []LedgerFinding,
) error {
	if err := deleteReviewerVerdict(ctx, queries, epic, verdict.Reviewer); err != nil {
		return err
	}
	if err := queries.DeleteEpicClosuresOfReviewer(ctx, sqlcgen.DeleteEpicClosuresOfReviewerParams{
		Epic: epic, Reviewer: verdict.Reviewer,
	}); err != nil {
		return fmt.Errorf("delete closures of findings by %s of epic %s: %w", verdict.Reviewer, epic, err)
	}
	if err := queries.DeleteEpicLedgerOfReviewer(ctx, sqlcgen.DeleteEpicLedgerOfReviewerParams{
		Epic: epic, Reviewer: verdict.Reviewer,
	}); err != nil {
		return fmt.Errorf("delete findings by %s of epic %s: %w", verdict.Reviewer, epic, err)
	}
	if err := insertVerdicts(ctx, queries, epic, []ReviewVerdict{verdict}); err != nil {
		return err
	}
	return insertLedger(ctx, queries, epic, findings)
}

// deleteReviewerVerdict deletes the stored verdict of epic's review by reviewer in the caller's write. A review with no
// verdict by reviewer is an error.
func deleteReviewerVerdict(ctx context.Context, queries *sqlcgen.Queries, epic, reviewer string) error {
	removed, err := queries.DeleteEpicReviewVerdictOf(ctx, sqlcgen.DeleteEpicReviewVerdictOfParams{
		Epic: epic, Reviewer: reviewer,
	})
	if err != nil {
		return fmt.Errorf("delete verdict by %s of epic %s: %w", reviewer, epic, err)
	}
	if removed == 0 {
		return fmt.Errorf("epic %s's review holds no verdict by %s", epic, reviewer)
	}
	return nil
}
