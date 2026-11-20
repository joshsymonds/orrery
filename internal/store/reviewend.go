package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// phaseCorrecting is the phase an epic is in while the ledger of its review is corrected, from the end of its run.
const phaseCorrecting = "correcting"

// EndReview ends the running run at path of epic at attempt as passed with output, stores verdicts and ledger as the
// review's verdicts and ledger, and sets the epic's phase to correcting, in one write. Output is cut to NodeOutputMax
// bytes, and the ledger is stored without closures. A run at another attempt and a run that is not running change
// nothing, so a repeat stores nothing twice.
func (s *Store) EndReview(
	ctx context.Context,
	epic, path string,
	attempt int64,
	output string,
	verdicts []ReviewVerdict,
	ledger []LedgerFinding,
	at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		ended, err := queries.EndEpicNodeRunAttempt(ctx, sqlcgen.EndEpicNodeRunAttemptParams{
			Outcome: "passed", Ended: at.Unix(), Output: capNodeOutput(output),
			Epic: epic, Path: path, Attempt: attempt,
		})
		if err != nil {
			return fmt.Errorf("end node run %s of epic %s: %w", path, epic, err)
		}
		if ended == 0 {
			return nil
		}
		return storeEndedReview(ctx, queries, epic, verdicts, ledger, at)
	})
}

// storeEndedReview stores verdicts and ledger as the verdicts and ledger of the review of epic, and sets the phase of
// epic to correcting at at, in the caller's write.
func storeEndedReview(
	ctx context.Context, queries *sqlcgen.Queries, epic string, verdicts []ReviewVerdict, ledger []LedgerFinding,
	at time.Time,
) error {
	if err := insertVerdicts(ctx, queries, epic, verdicts); err != nil {
		return err
	}
	if err := insertLedger(ctx, queries, epic, ledger); err != nil {
		return err
	}
	return setEpicPhase(ctx, queries, epic, phaseCorrecting, at)
}

// insertVerdicts stores verdicts as verdicts of the review of epic, each at its Position, in the caller's write.
func insertVerdicts(ctx context.Context, queries *sqlcgen.Queries, epic string, verdicts []ReviewVerdict) error {
	for _, v := range verdicts {
		if err := queries.InsertEpicReviewVerdict(ctx, sqlcgen.InsertEpicReviewVerdictParams{
			Epic: epic, Position: v.Position, Reviewer: v.Reviewer, Step: v.Step, Verdict: v.Verdict, Reason: v.Reason,
		}); err != nil {
			return fmt.Errorf("insert verdict %d of epic %s: %w", v.Position, epic, err)
		}
	}
	return nil
}

// insertLedger stores ledger as findings of the ledger of epic, each at its Position and without closures, in the
// caller's write.
func insertLedger(ctx context.Context, queries *sqlcgen.Queries, epic string, ledger []LedgerFinding) error {
	for _, finding := range ledger {
		if err := queries.InsertEpicLedgerFinding(ctx, sqlcgen.InsertEpicLedgerFindingParams{
			Epic: epic, ID: finding.ID, Position: finding.Position, Reviewer: finding.Reviewer,
			Finding: finding.Finding, Claim: finding.Claim, Citation: finding.Citation, Location: finding.Location,
			Evidence: finding.Evidence, VerifyBy: finding.VerifyBy, Step: finding.Step, Result: finding.Result,
			Unverified: boolInt(finding.Unverified), Reason: finding.Reason,
		}); err != nil {
			return fmt.Errorf("insert finding %s of epic %s: %w", finding.ID, epic, err)
		}
	}
	return nil
}
