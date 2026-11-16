package store

import (
	"context"
	"testing"
)

// reviewendStartFrozen starts the review run of integrateEpic at path review and attempt 1, and freezes the review
// at rev-7, so the epic's phase is reviewing and the run is running.
func reviewendStartFrozen(t *testing.T, s *Store) {
	t.Helper()
	startIntegrateRuns(t, s, NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review", Attempt: 1})
	if err := s.FreezeReview(
		context.Background(),
		integrateEpic,
		"review",
		1,
		"rev-7",
		integrateEndedAt(),
	); err != nil {
		t.Fatalf("FreezeReview: %v", err)
	}
}

// TestEndReviewEndsRunAndStoresLedger checks that EndReview ends the frozen review run at its attempt as passed with
// its output, stores the verdicts and the ledger in position order with every field, and sets the epic's phase to
// correcting. The ledger holds a confirmed finding, a dropped one and a confirmed finding that was not verified.
// Renaming a stored field, dropping the output, or leaving the phase at reviewing breaks it.
func TestEndReviewEndsRunAndStoresLedger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	reviewendStartFrozen(t, s)

	if err := s.EndReview(ctx, integrateEpic, "review", 1, "review output",
		[]ReviewVerdict{
			{Position: 1, Reviewer: "conformance", Step: "id-rev-1", Verdict: "holds"},
			{Position: 2, Reviewer: "security", Step: "id-rev-2", Verdict: "fails", Reason: "two findings"},
		},
		[]LedgerFinding{
			{ID: "conformance-1", Position: 1, Reviewer: "conformance", Finding: "f-1", Claim: "claim one",
				Citation: "cite one", Location: "a.go:1", Evidence: "evidence one", VerifyBy: "run one",
				Step: "id-ver-1", Result: "confirmed", Reason: "reproduced"},
			{ID: "security-1", Position: 2, Reviewer: "security", Finding: "f-2", Claim: "claim two",
				Citation: "cite two", Location: "b.go:2", Evidence: "evidence two", VerifyBy: "run two",
				Step: "id-ver-2", Result: "dropped", Reason: "not reproduced"},
			{ID: "security-2", Position: 3, Reviewer: "security", Finding: "f-3", Claim: "claim three",
				Citation: "cite three", Location: "c.go:3", Evidence: "evidence three", VerifyBy: "run three",
				Result: "confirmed", Unverified: true, Reason: "unverified: reviewer security has verify off"},
		},
		integrateEndedAt()); err != nil {
		t.Fatalf("EndReview: %v", err)
	}

	requireIntegrateRun(t, s, "review", NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review",
		State: NodeEnded, Outcome: "passed", Started: integrateStartedAt(), Ended: integrateEndedAt(), Attempt: 1,
		Revision: "rev-7", Output: "review output"})
	requireReview(t, s, Review{
		Frozen: "rev-7",
		Verdicts: []ReviewVerdict{
			{Position: 1, Reviewer: "conformance", Step: "id-rev-1", Verdict: "holds"},
			{Position: 2, Reviewer: "security", Step: "id-rev-2", Verdict: "fails", Reason: "two findings"},
		},
		Ledger: []LedgerFinding{
			{ID: "conformance-1", Position: 1, Reviewer: "conformance", Finding: "f-1", Claim: "claim one",
				Citation: "cite one", Location: "a.go:1", Evidence: "evidence one", VerifyBy: "run one",
				Step: "id-ver-1", Result: "confirmed", Reason: "reproduced"},
			{ID: "security-1", Position: 2, Reviewer: "security", Finding: "f-2", Claim: "claim two",
				Citation: "cite two", Location: "b.go:2", Evidence: "evidence two", VerifyBy: "run two",
				Step: "id-ver-2", Result: "dropped", Reason: "not reproduced"},
			{ID: "security-2", Position: 3, Reviewer: "security", Finding: "f-3", Claim: "claim three",
				Citation: "cite three", Location: "c.go:3", Evidence: "evidence three", VerifyBy: "run three",
				Result: "confirmed", Unverified: true, Reason: "unverified: reviewer security has verify off"},
		},
	})
	if row := integrateEpicRow(t, s); row.Phase != "correcting" {
		t.Errorf("phase of epic %s after EndReview = %q, want correcting", integrateEpic, row.Phase)
	}
}

// TestEndReviewRepeatStoresNothing checks that a repeat of EndReview after the run ended stores none of its other
// verdicts or ledger findings and keeps the first output, so the ledger is written once. Storing the repeat's verdict
// and finding, or ending the run again, breaks it.
func TestEndReviewRepeatStoresNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	reviewendStartFrozen(t, s)
	if err := s.EndReview(ctx, integrateEpic, "review", 1, "review output",
		[]ReviewVerdict{{Position: 1, Reviewer: "conformance", Step: "id-rev-1", Verdict: "holds"}},
		[]LedgerFinding{{ID: "conformance-1", Position: 1, Reviewer: "conformance", Finding: "f-1", Claim: "claim one",
			Step: "id-ver-1", Result: "confirmed", Reason: "reproduced"}},
		integrateEndedAt()); err != nil {
		t.Fatalf("EndReview: %v", err)
	}

	if err := s.EndReview(ctx, integrateEpic, "review", 1, "other output",
		[]ReviewVerdict{{Position: 2, Reviewer: "security", Step: "id-rev-2", Verdict: "fails", Reason: "other"}},
		[]LedgerFinding{{ID: "security-1", Position: 2, Reviewer: "security", Finding: "f-2", Claim: "claim two",
			Result: "confirmed", Unverified: true, Reason: "unverified: reviewer security has verify off"}},
		integrateEndedAt()); err != nil {
		t.Fatalf("repeat EndReview: %v", err)
	}

	requireIntegrateRun(t, s, "review", NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review",
		State: NodeEnded, Outcome: "passed", Started: integrateStartedAt(), Ended: integrateEndedAt(), Attempt: 1,
		Revision: "rev-7", Output: "review output"})
	requireReview(t, s, Review{
		Frozen:   "rev-7",
		Verdicts: []ReviewVerdict{{Position: 1, Reviewer: "conformance", Step: "id-rev-1", Verdict: "holds"}},
		Ledger: []LedgerFinding{{ID: "conformance-1", Position: 1, Reviewer: "conformance", Finding: "f-1",
			Claim: "claim one", Step: "id-ver-1", Result: "confirmed", Reason: "reproduced"}},
	})
	if row := integrateEpicRow(t, s); row.Phase != "correcting" {
		t.Errorf("phase of epic %s after repeat EndReview = %q, want correcting", integrateEpic, row.Phase)
	}
}

// TestEndReviewIgnoresOtherAttempt checks that EndReview at an attempt other than the running run's changes nothing:
// the run stays running with no output, no verdict or ledger finding is stored and the phase stays reviewing. Ending
// the run at any attempt breaks it.
func TestEndReviewIgnoresOtherAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	reviewendStartFrozen(t, s)

	if err := s.EndReview(ctx, integrateEpic, "review", 2, "other attempt output",
		[]ReviewVerdict{{Position: 1, Reviewer: "conformance", Step: "id-rev-1", Verdict: "holds"}},
		[]LedgerFinding{{ID: "conformance-1", Position: 1, Reviewer: "conformance", Finding: "f-1", Claim: "claim one",
			Step: "id-ver-1", Result: "confirmed", Reason: "reproduced"}},
		integrateEndedAt()); err != nil {
		t.Fatalf("EndReview at another attempt: %v", err)
	}

	requireIntegrateRun(t, s, "review", NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review",
		State: NodeRunning, Started: integrateStartedAt(), Attempt: 1, Revision: "rev-7"})
	requireReview(t, s, Review{Frozen: "rev-7"})
	if row := integrateEpicRow(t, s); row.Phase != "reviewing" {
		t.Errorf("phase of epic %s after EndReview at another attempt = %q, want reviewing", integrateEpic, row.Phase)
	}
}

// TestEndReviewIgnoresEndedRun checks that EndReview of a run another write ended changes nothing: the run keeps its
// outcome and output, no verdict or ledger finding is stored and the phase stays reviewing. Ending the run again with
// passed, or setting the phase, breaks it.
func TestEndReviewIgnoresEndedRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	reviewendStartFrozen(t, s)
	if err := s.EndNodes(ctx, integrateEpic, []NodeEnd{{Path: "review", Outcome: "failed", Output: "node output"}},
		integrateEndedAt()); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}

	if err := s.EndReview(ctx, integrateEpic, "review", 1, "review output",
		[]ReviewVerdict{{Position: 1, Reviewer: "conformance", Step: "id-rev-1", Verdict: "holds"}},
		[]LedgerFinding{{ID: "conformance-1", Position: 1, Reviewer: "conformance", Finding: "f-1", Claim: "claim one",
			Step: "id-ver-1", Result: "confirmed", Reason: "reproduced"}},
		integrateEndedAt()); err != nil {
		t.Fatalf("EndReview of an ended run: %v", err)
	}

	requireIntegrateRun(t, s, "review", NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review",
		State: NodeEnded, Outcome: "failed", Started: integrateStartedAt(), Ended: integrateEndedAt(), Attempt: 1,
		Revision: "rev-7", Output: "node output"})
	requireReview(t, s, Review{Frozen: "rev-7"})
	if row := integrateEpicRow(t, s); row.Phase != "reviewing" {
		t.Errorf("phase of epic %s after EndReview of an ended run = %q, want reviewing", integrateEpic, row.Phase)
	}
}
