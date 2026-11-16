package enginehost

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// ledgerFrozenRevision is the revision the review of the ledger epic freezes at.
const ledgerFrozenRevision = "synthetic-frozen-revision"

// ledgerReviewPath is the path of the review run of the ledger epic.
const ledgerReviewPath = "review"

// TestEpicGetReturnsLedgerOfTheReview checks that GetEpic shows the frozen revision, the verdicts, the ledger, the
// correction rounds and the clean revision of a review that froze, ran two closure rounds and ended clean, with every
// field of each verdict, finding, closure and round. The verdicts and findings are stored out of position order, so the
// reply must list them by position, and each finding's closures by round.
func TestEpicGetReturnsLedgerOfTheReview(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	created := createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	at := time.Unix(turnFreed, 0)
	review := store.NodeRun{Path: ledgerReviewPath, ID: "run-review", Node: "review", Kind: "agent", Attempt: 1}
	if err := st.StartNodes(ctx, epicServiceID, []store.NodeRun{review}, nil, at); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := st.FreezeReview(ctx, epicServiceID, ledgerReviewPath, 1, ledgerFrozenRevision); err != nil {
		t.Fatalf("FreezeReview: %v", err)
	}
	verdicts := []store.ReviewVerdict{
		{Position: 2, Reviewer: "synthetic-security", Step: "synthetic-step-2", Verdict: "fails",
			Reason: "synthetic reason"},
		{Position: 1, Reviewer: "synthetic-conformance", Step: "synthetic-step-1", Verdict: "holds"},
	}
	ledger := []store.LedgerFinding{
		{ID: "synthetic-security-1", Position: 1, Reviewer: "synthetic-security", Finding: "f-1",
			Claim: "synthetic claim", Citation: "a.go:1", Location: "a.go:2", Evidence: "synthetic evidence",
			VerifyBy: "synthetic check", Step: "synthetic-verify-1", Result: "confirmed", Reason: "reproduced"},
		{ID: "synthetic-conformance-1", Position: 2, Reviewer: "synthetic-conformance", Finding: "f-2",
			Claim: "synthetic claim two", Citation: "b.go:1", Location: "b.go:2", Evidence: "synthetic evidence two",
			VerifyBy: "synthetic check two", Result: "confirmed", Unverified: true,
			Reason: "unverified: reviewer synthetic-conformance has verify off"},
	}
	if err := st.EndReview(
		ctx,
		epicServiceID,
		ledgerReviewPath,
		1,
		"synthetic output",
		verdicts,
		ledger,
		at,
	); err != nil {
		t.Fatalf("EndReview: %v", err)
	}

	before := getEpicOK(t, client)
	if before.GetFrozen() != ledgerFrozenRevision {
		t.Errorf("GetEpic frozen = %q, want %q", before.GetFrozen(), ledgerFrozenRevision)
	}
	if before.GetClean() != "" {
		t.Errorf("GetEpic clean = %q, want empty before the epic ends clean", before.GetClean())
	}

	firstRun := store.NodeRun{Path: "closure/1", ID: "run-closure-1", Node: "closure", Kind: "closure", Attempt: 1}
	if err := st.StartNodes(ctx, epicServiceID, []store.NodeRun{firstRun}, nil, at); err != nil {
		t.Fatalf("StartNodes of round 1: %v", err)
	}
	firstRound := store.ReviewRound{
		Round:    1,
		Revision: "synthetic-round-1-revision",
		Tasks:    []int64{3, 4},
		Outcome:  "failed",
	}
	firstClosures := []store.Closure{
		{Finding: "synthetic-security-1", Round: 1, Step: "synthetic-close-1", Result: "open",
			Reason: "synthetic still open"},
		{Finding: "synthetic-conformance-1", Round: 1, Step: "synthetic-close-2", Result: "closed",
			Reason: "synthetic fixed"},
	}
	if err := st.EndClosure(ctx, epicServiceID, "closure/1", 1, "failed", "synthetic output one", firstRound,
		firstClosures, at); err != nil {
		t.Fatalf("EndClosure of round 1: %v", err)
	}

	secondRun := store.NodeRun{Path: "closure/2", ID: "run-closure-2", Node: "closure", Kind: "closure", Attempt: 1}
	if err := st.StartNodes(ctx, epicServiceID, []store.NodeRun{secondRun}, nil, at); err != nil {
		t.Fatalf("StartNodes of round 2: %v", err)
	}
	secondRound := store.ReviewRound{
		Round:    2,
		Revision: "synthetic-round-2-revision",
		Tasks:    []int64{5},
		Outcome:  "passed",
	}
	secondClosures := []store.Closure{
		{Finding: "synthetic-security-1", Round: 2, Step: "synthetic-close-3", Result: "closed",
			Reason: "synthetic fixed now"},
	}
	if err := st.EndClosure(ctx, epicServiceID, "closure/2", 1, "passed", "synthetic output two", secondRound,
		secondClosures, at); err != nil {
		t.Fatalf("EndClosure of round 2: %v", err)
	}
	if err := st.EndEpic(ctx, epicServiceID, "clean", "Epic ended clean.", nil, at); err != nil {
		t.Fatalf("EndEpic: %v", err)
	}

	got := getEpicOK(t, client)
	if got.GetFrozen() != ledgerFrozenRevision {
		t.Errorf("GetEpic frozen = %q, want %q", got.GetFrozen(), ledgerFrozenRevision)
	}
	if want := created.GetContract().GetBase(); got.GetClean() != want {
		t.Errorf("GetEpic clean = %q, want the stored base %q", got.GetClean(), want)
	}
	if got.GetLedger() == nil {
		t.Fatalf("GetEpic ledger is unset, want the review's ledger")
	}
	wantProtos(t, "GetEpic verdicts", got.GetLedger().GetVerdicts(), []*v1.EpicReviewVerdict{
		{Position: 1, Reviewer: "synthetic-conformance", Step: "synthetic-step-1", Verdict: "holds"},
		{
			Position: 2,
			Reviewer: "synthetic-security",
			Step:     "synthetic-step-2",
			Verdict:  "fails",
			Reason:   "synthetic reason",
		},
	})
	wantProtos(t, "GetEpic findings", got.GetLedger().GetFindings(), []*v1.EpicLedgerFinding{
		{Id: "synthetic-security-1", Position: 1, Reviewer: "synthetic-security", Finding: "f-1",
			Claim: "synthetic claim", Citation: "a.go:1", Location: "a.go:2", Evidence: "synthetic evidence",
			VerifyBy: "synthetic check", Step: "synthetic-verify-1", Result: "confirmed", Reason: "reproduced",
			Closures: []*v1.EpicClosure{
				{Round: 1, Step: "synthetic-close-1", Result: "open", Reason: "synthetic still open"},
				{Round: 2, Step: "synthetic-close-3", Result: "closed", Reason: "synthetic fixed now"},
			}},
		{Id: "synthetic-conformance-1", Position: 2, Reviewer: "synthetic-conformance", Finding: "f-2",
			Claim: "synthetic claim two", Citation: "b.go:1", Location: "b.go:2", Evidence: "synthetic evidence two",
			VerifyBy: "synthetic check two", Result: "confirmed", Unverified: true,
			Reason: "unverified: reviewer synthetic-conformance has verify off",
			Closures: []*v1.EpicClosure{
				{Round: 1, Step: "synthetic-close-2", Result: "closed", Reason: "synthetic fixed"},
			}},
	})
	wantProtos(t, "GetEpic rounds", got.GetLedger().GetRounds(), []*v1.EpicReviewRound{
		{Round: 1, Revision: "synthetic-round-1-revision", Tasks: []int64{3, 4}, Outcome: "failed"},
		{Round: 2, Revision: "synthetic-round-2-revision", Tasks: []int64{5}, Outcome: "passed"},
	})
}

// TestEpicGetWithNoReviewHasEmptyLedger checks that an epic whose review has not started returns an empty ledger, with
// no frozen or clean revision.
func TestEpicGetWithNoReviewHasEmptyLedger(t *testing.T) {
	t.Parallel()
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	got := getEpicOK(t, client)
	if got.GetFrozen() != "" {
		t.Errorf("GetEpic frozen = %q, want empty before the review freezes", got.GetFrozen())
	}
	if got.GetClean() != "" {
		t.Errorf("GetEpic clean = %q, want empty before the epic ends clean", got.GetClean())
	}
	if got.GetLedger() == nil {
		t.Fatalf("GetEpic ledger is unset, want an empty ledger")
	}
	if !proto.Equal(got.GetLedger(), &v1.EpicLedger{}) {
		t.Errorf("GetEpic ledger = %v, want an empty ledger", got.GetLedger())
	}
}
