package store

import (
	"context"
	"testing"
)

// TestEndReviewRerunEndsRunAndReplacesVerdictGapTurn: on a review that ended with test_parsimony's gap verdict and floor's
// holding verdict and finding, a rerun run of test_parsimony ends passed with the verdict and findings it settled. The
// review then holds test_parsimony's new verdict, and floor's verdict and finding are unchanged. Naming a reviewer the
// review holds no verdict by is an error, and that rerun stays running.
func TestEndReviewRerunEndsRunAndReplacesVerdictGapTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	reviewendStartFrozen(t, s)
	floorVerdict := ReviewVerdict{Position: 1, Reviewer: "floor", Step: "id-rev-1", Verdict: "holds", Reason: "fine"}
	floorOne := LedgerFinding{ID: "floor-1", Position: 1, Reviewer: "floor", Finding: "f-1", Claim: "claim one",
		Citation: "cite one", Location: "a.go:1", Evidence: "evidence one", VerifyBy: "run one", Step: "id-ver-1",
		Result: "confirmed", Reason: "reproduced"}
	gapVerdict := ReviewVerdict{Position: 2, Reviewer: "test_parsimony", Step: "id-rev-2", Verdict: "gap",
		Reason: "reviewer 1 gave no verdict twice"}
	if err := s.EndReview(ctx, integrateEpic, "review", 1, "review output",
		[]ReviewVerdict{floorVerdict, gapVerdict}, []LedgerFinding{floorOne}, integrateEndedAt()); err != nil {
		t.Fatalf("EndReview: %v", err)
	}
	review := Review{
		Frozen:   "rev-7",
		Verdicts: []ReviewVerdict{floorVerdict, gapVerdict},
		Ledger:   []LedgerFinding{floorOne},
	}

	const rerun = "conclude/R2.1/rerun"
	startIntegrateRuns(t, s, NodeRun{Path: rerun, ID: "id-rerun", Node: "rerun", Kind: "rerun", Attempt: 1})
	holds := ReviewVerdict{Position: 2, Reviewer: "test_parsimony", Step: "id-rerun-1", Verdict: "holds"}
	if err := s.EndReviewRerun(
		ctx,
		integrateEpic,
		NodeEnd{Path: rerun, Outcome: "passed", Output: "test_parsimony: holds"},
		holds,
		nil,
		integrateEndedAt(),
	); err != nil {
		t.Fatalf("EndReviewRerun: %v", err)
	}
	requireIntegrateRun(t, s, rerun, NodeRun{Path: rerun, ID: "id-rerun", Node: "rerun", Kind: "rerun",
		State: NodeEnded, Outcome: "passed", Started: integrateStartedAt(), Ended: integrateEndedAt(), Attempt: 1,
		Output: "test_parsimony: holds"})
	review.Verdicts[1] = holds
	requireReview(t, s, review)

	const refused = "conclude/R2.2/rerun"
	startIntegrateRuns(t, s, NodeRun{Path: refused, ID: "id-refused", Node: "rerun", Kind: "rerun", Attempt: 1})
	err := s.EndReviewRerun(ctx, integrateEpic, NodeEnd{Path: refused, Outcome: "passed", Output: "security: holds"},
		ReviewVerdict{Position: 3, Reviewer: "security", Step: "id-refused-1", Verdict: "holds"}, nil,
		integrateEndedAt())
	if err == nil {
		t.Fatalf("EndReviewRerun naming security, which holds no verdict: no error, want one")
	}
	requireIntegrateRun(t, s, refused, NodeRun{Path: refused, ID: "id-refused", Node: "rerun", Kind: "rerun",
		State: NodeRunning, Started: integrateStartedAt(), Attempt: 1})
	requireReview(t, s, review)
}
