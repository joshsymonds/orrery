package server

import (
	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/redact"
)

// epicLedgerFindingMessages converts each finding of an orreryd epic's ledger to its EpicLedgerFinding, in order. Its
// claim, citation, location, evidence and reason are redacted, as epicBuildTaskMessages redacts a task finding's text,
// and each closure is converted by epicClosureMessages. The finding's text and its verify_by are not carried.
func epicLedgerFindingMessages(findings []*enginev1.EpicLedgerFinding) []*orreryv1.EpicLedgerFinding {
	out := make([]*orreryv1.EpicLedgerFinding, 0, len(findings))
	for _, finding := range findings {
		out = append(out, &orreryv1.EpicLedgerFinding{
			Id:         finding.GetId(),
			Position:   finding.GetPosition(),
			Reviewer:   finding.GetReviewer(),
			Claim:      redact.Text(finding.GetClaim()),
			Citation:   redact.Text(finding.GetCitation()),
			Location:   redact.Text(finding.GetLocation()),
			Evidence:   redact.Text(finding.GetEvidence()),
			Step:       finding.GetStep(),
			Result:     finding.GetResult(),
			Unverified: finding.GetUnverified(),
			Reason:     redact.Text(finding.GetReason()),
			Closures:   epicClosureMessages(finding.GetClosures()),
		})
	}
	return out
}

// epicClosureMessages converts each closure of a ledger finding to its EpicClosure, in order, with its reason redacted.
func epicClosureMessages(closures []*enginev1.EpicClosure) []*orreryv1.EpicClosure {
	out := make([]*orreryv1.EpicClosure, 0, len(closures))
	for _, closure := range closures {
		out = append(out, &orreryv1.EpicClosure{
			Round:  closure.GetRound(),
			Step:   closure.GetStep(),
			Result: closure.GetResult(),
			Reason: redact.Text(closure.GetReason()),
		})
	}
	return out
}

// epicReviewVerdictMessages converts each verdict of an orreryd epic's review to its EpicReviewVerdict, in order, with
// its reason redacted.
func epicReviewVerdictMessages(verdicts []*enginev1.EpicReviewVerdict) []*orreryv1.EpicReviewVerdict {
	out := make([]*orreryv1.EpicReviewVerdict, 0, len(verdicts))
	for _, verdict := range verdicts {
		out = append(out, &orreryv1.EpicReviewVerdict{
			Position: verdict.GetPosition(),
			Reviewer: verdict.GetReviewer(),
			Step:     verdict.GetStep(),
			Verdict:  verdict.GetVerdict(),
			Reason:   redact.Text(verdict.GetReason()),
		})
	}
	return out
}
