package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// requireReview fails unless the review of integrateEpic is want, in every field.
func requireReview(t *testing.T, s *Store, want Review) {
	t.Helper()
	got, err := s.EpicReview(context.Background(), integrateEpic)
	if err != nil {
		t.Fatalf("EpicReview of epic %s: %v", integrateEpic, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("review of epic %s = %+v, want %+v", integrateEpic, got, want)
	}
}

// TestFreezeReviewFreezesRunAtItsAttempt checks that FreezeReview gives the running review run at path and attempt the
// revision, stores that revision as the review's frozen revision with nothing else in the review, and sets the epic's
// phase to reviewing.
func TestFreezeReviewFreezesRunAtItsAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s, NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review", Attempt: 1})

	if err := s.FreezeReview(ctx, integrateEpic, "review", 1, "rev-7", integrateEndedAt()); err != nil {
		t.Fatalf("FreezeReview: %v", err)
	}
	requireIntegrateRun(t, s, "review", NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review",
		State: NodeRunning, Started: integrateStartedAt(), Attempt: 1, Revision: "rev-7"})
	requireReview(t, s, Review{Frozen: "rev-7"})
	if row := integrateEpicRow(t, s); row.Phase != "reviewing" {
		t.Errorf("phase of epic %s after FreezeReview = %q, want reviewing", integrateEpic, row.Phase)
	}
}

// TestFreezeReviewKeepsFirstFrozenRevision checks that a repeat of FreezeReview for the run it froze changes nothing,
// and that a second review run takes the revision the review froze first rather than the one it is given.
func TestFreezeReviewKeepsFirstFrozenRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s,
		NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review", Attempt: 1},
		NodeRun{Path: "review/again", ID: "id-again", Node: "review", Kind: "review", Attempt: 1},
	)
	if err := s.FreezeReview(ctx, integrateEpic, "review", 1, "rev-7", integrateEndedAt()); err != nil {
		t.Fatalf("FreezeReview: %v", err)
	}
	if err := s.FreezeReview(ctx, integrateEpic, "review", 1, "rev-8", integrateEndedAt()); err != nil {
		t.Fatalf("repeat FreezeReview: %v", err)
	}
	requireIntegrateRun(t, s, "review", NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review",
		State: NodeRunning, Started: integrateStartedAt(), Attempt: 1, Revision: "rev-7"})

	if err := s.FreezeReview(ctx, integrateEpic, "review/again", 1, "rev-8", integrateEndedAt()); err != nil {
		t.Fatalf("FreezeReview of a second run: %v", err)
	}
	requireIntegrateRun(t, s, "review/again", NodeRun{Path: "review/again", ID: "id-again", Node: "review",
		Kind: "review", State: NodeRunning, Started: integrateStartedAt(), Attempt: 1, Revision: "rev-7"})
	requireReview(t, s, Review{Frozen: "rev-7"})
}

// TestFreezeReviewIgnoresOtherAttemptsAndEndedRuns checks that FreezeReview changes nothing when the run at path is at
// another attempt or has ended: the run keeps no revision, no review is stored and the epic's phase stays as it was.
func TestFreezeReviewIgnoresOtherAttemptsAndEndedRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s,
		NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review", Attempt: 2},
		NodeRun{Path: "review/ended", ID: "id-ended", Node: "review", Kind: "review", Attempt: 1},
	)
	if err := s.EndNodes(ctx, integrateEpic, []NodeEnd{{Path: "review/ended", Outcome: "passed"}},
		integrateEndedAt()); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}

	if err := s.FreezeReview(ctx, integrateEpic, "review", 1, "rev-7", integrateEndedAt()); err != nil {
		t.Fatalf("FreezeReview at another attempt: %v", err)
	}
	if err := s.FreezeReview(ctx, integrateEpic, "review/ended", 1, "rev-7", integrateEndedAt()); err != nil {
		t.Fatalf("FreezeReview of an ended run: %v", err)
	}
	requireIntegrateRun(t, s, "review", NodeRun{Path: "review", ID: "id-review", Node: "review", Kind: "review",
		State: NodeRunning, Started: integrateStartedAt(), Attempt: 2})
	requireIntegrateRun(t, s, "review/ended", NodeRun{Path: "review/ended", ID: "id-ended", Node: "review",
		Kind: "review", State: NodeEnded, Outcome: "passed", Started: integrateStartedAt(), Ended: integrateEndedAt(),
		Attempt: 1})
	requireReview(t, s, Review{})
	if row := integrateEpicRow(t, s); row.Phase != "accepted" {
		t.Errorf("phase of epic %s after ignored freezes = %q, want accepted", integrateEpic, row.Phase)
	}
}

// TestEpicReviewZeroWithoutReview checks that an epic whose review has not started reads back the zero Review.
func TestEpicReviewZeroWithoutReview(t *testing.T) {
	t.Parallel()
	s := openIntegrateStore(t)
	requireReview(t, s, Review{})
}

// TestEpicReviewReadsStoredRows checks that EpicReview reads every table of a review back: the verdicts, the ledger
// with its closures by round, the rounds with their tasks, and the holds in id order. The rows are stored through the
// generated queries in an order other than the one they read back in.
func TestEpicReviewReadsStoredRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	queries := sqlcgen.New(s.db)
	if err := queries.InsertEpicReview(ctx, sqlcgen.InsertEpicReviewParams{
		Epic: integrateEpic, Frozen: "rev-7",
	}); err != nil {
		t.Fatalf("InsertEpicReview: %v", err)
	}
	if err := queries.SetEpicReviewClean(
		ctx,
		sqlcgen.SetEpicReviewCleanParams{Clean: "rev-9", Epic: integrateEpic},
	); err != nil {
		t.Fatalf("SetEpicReviewClean: %v", err)
	}
	for _, verdict := range []sqlcgen.InsertEpicReviewVerdictParams{
		{Epic: integrateEpic, Position: 2, Reviewer: "security", Step: "id-rev-2", Verdict: "fails", Reason: "two findings"},
		{Epic: integrateEpic, Position: 1, Reviewer: "conformance", Step: "id-rev-1", Verdict: "holds"},
	} {
		if err := queries.InsertEpicReviewVerdict(ctx, verdict); err != nil {
			t.Fatalf("InsertEpicReviewVerdict %d: %v", verdict.Position, err)
		}
	}
	for _, finding := range []sqlcgen.InsertEpicLedgerFindingParams{
		{Epic: integrateEpic, ID: "security-1", Position: 2, Reviewer: "security", Finding: "f-2", Claim: "claim two",
			Citation: "cite two", Location: "b.go:2", Evidence: "evidence two", VerifyBy: "run two", Step: "",
			Result: "confirmed", Unverified: 1, Reason: "unverified: reviewer security has verify off"},
		{Epic: integrateEpic, ID: "conformance-1", Position: 1, Reviewer: "conformance", Finding: "f-1",
			Claim: "claim one", Citation: "cite one", Location: "a.go:1", Evidence: "evidence one", VerifyBy: "run one",
			Step: "id-ver-1", Result: "confirmed", Unverified: 0, Reason: "reproduced"},
	} {
		if err := queries.InsertEpicLedgerFinding(ctx, finding); err != nil {
			t.Fatalf("InsertEpicLedgerFinding %s: %v", finding.ID, err)
		}
	}
	for _, closure := range []sqlcgen.InsertEpicClosureParams{
		{Epic: integrateEpic, Round: 2, Finding: "security-1", Step: "id-close-3", Result: "closed", Reason: "fixed"},
		{Epic: integrateEpic, Round: 2, Finding: "conformance-1", Step: "id-close-2", Result: "open", Reason: "still open"},
		{Epic: integrateEpic, Round: 1, Finding: "conformance-1", Step: "id-close-1", Result: "closed", Reason: "fixed"},
	} {
		if err := queries.InsertEpicClosure(ctx, closure); err != nil {
			t.Fatalf("InsertEpicClosure %s of round %d: %v", closure.Finding, closure.Round, err)
		}
	}
	for _, round := range []sqlcgen.InsertEpicReviewRoundParams{
		{Epic: integrateEpic, Round: 2, Revision: "rev-9", Tasks: "[]", Outcome: "passed"},
		{Epic: integrateEpic, Round: 1, Revision: "rev-8", Tasks: "[3,4]", Outcome: "failed"},
	} {
		if err := queries.InsertEpicReviewRound(ctx, round); err != nil {
			t.Fatalf("InsertEpicReviewRound %d: %v", round.Round, err)
		}
	}
	for _, step := range []string{"id-hold-2", "id-hold-1"} {
		if _, err := queries.InsertEpicReviewHold(ctx, sqlcgen.InsertEpicReviewHoldParams{
			Epic: integrateEpic, Step: step,
		}); err != nil {
			t.Fatalf("InsertEpicReviewHold %s: %v", step, err)
		}
	}

	requireReview(t, s, Review{
		Frozen: "rev-7",
		Clean:  "rev-9",
		Verdicts: []ReviewVerdict{
			{Position: 1, Reviewer: "conformance", Step: "id-rev-1", Verdict: "holds"},
			{Position: 2, Reviewer: "security", Step: "id-rev-2", Verdict: "fails", Reason: "two findings"},
		},
		Ledger: []LedgerFinding{
			{
				ID:       "conformance-1",
				Position: 1,
				Reviewer: "conformance",
				Finding:  "f-1",
				Claim:    "claim one",
				Citation: "cite one",
				Location: "a.go:1",
				Evidence: "evidence one",
				VerifyBy: "run one",
				Step:     "id-ver-1",
				Result:   "confirmed",
				Reason:   "reproduced",
				Closures: []Closure{
					{Finding: "conformance-1", Round: 1, Step: "id-close-1", Result: "closed", Reason: "fixed"},
					{Finding: "conformance-1", Round: 2, Step: "id-close-2", Result: "open", Reason: "still open"},
				},
			},
			{ID: "security-1", Position: 2, Reviewer: "security", Finding: "f-2", Claim: "claim two",
				Citation: "cite two", Location: "b.go:2", Evidence: "evidence two", VerifyBy: "run two",
				Result: "confirmed", Unverified: true, Reason: "unverified: reviewer security has verify off",
				Closures: []Closure{
					{Finding: "security-1", Round: 2, Step: "id-close-3", Result: "closed", Reason: "fixed"},
				}},
		},
		Rounds: []ReviewRound{
			{Round: 1, Revision: "rev-8", Tasks: []int64{3, 4}, Outcome: "failed"},
			{Round: 2, Revision: "rev-9", Outcome: "passed"},
		},
		Holds: []string{"id-hold-1", "id-hold-2"},
	})
}
