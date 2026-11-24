package server

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// ledgerEpic returns the stored epic of the ledger tests: two reviewer verdicts, and two ledger findings, the first
// unverified and the second verified and closed in two correction rounds.
func ledgerEpic() *enginev1.StoredEpic {
	epic := storedEpicOf("repoa/reviewed", "accepted", epicsAgo(time.Hour))
	epic.Ledger = &enginev1.EpicLedger{
		Verdicts: []*enginev1.EpicReviewVerdict{
			{
				Position: 1, Reviewer: "synthetic-reviewer-a", Step: "step-review-a", Verdict: "fails",
				Reason: "synthetic verdict reason a",
			},
			{
				Position: 2, Reviewer: "synthetic-reviewer-b", Step: "step-review-b", Verdict: "holds",
				Reason: "synthetic verdict reason b",
			},
		},
		Findings: []*enginev1.EpicLedgerFinding{
			{
				Id: "synthetic-reviewer-a-1", Position: 1, Reviewer: "synthetic-reviewer-a", Finding: "F1",
				Claim: "synthetic claim one", Citation: "synthetic citation one", Location: "synthetic/one.go:10",
				Evidence: "synthetic evidence one", Result: "confirmed", Unverified: true,
				Reason: "synthetic unverified reason",
			},
			{
				Id: "synthetic-reviewer-b-1", Position: 2, Reviewer: "synthetic-reviewer-b", Finding: "F2",
				Claim: "synthetic claim two", Citation: "synthetic citation two", Location: "synthetic/two.go:20",
				Evidence: "synthetic evidence two", VerifyBy: "synthetic verify by", Step: "step-verify-2",
				Result: "confirmed", Reason: "synthetic verified reason",
				Closures: []*enginev1.EpicClosure{
					{Round: 1, Step: "step-closure-2a", Result: "open", Reason: "synthetic open reason"},
					{Round: 2, Step: "step-closure-2b", Result: "closed", Reason: "synthetic closed reason"},
				},
			},
		},
	}
	return epic
}

func TestEpicsGetAnswersLedgerAndReviewsInOrder(t *testing.T) {
	t.Parallel()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{ledgerEpic()}}
	client := epicStoreServe(t, &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/reviewed")

	wantLedger := []*orreryv1.EpicLedgerFinding{
		{
			Id: "synthetic-reviewer-a-1", Position: 1, Reviewer: "synthetic-reviewer-a",
			Claim: "synthetic claim one", Citation: "synthetic citation one", Location: "synthetic/one.go:10",
			Evidence: "synthetic evidence one", Result: "confirmed", Unverified: true,
			Reason: "synthetic unverified reason",
		},
		{
			Id: "synthetic-reviewer-b-1", Position: 2, Reviewer: "synthetic-reviewer-b",
			Claim: "synthetic claim two", Citation: "synthetic citation two", Location: "synthetic/two.go:20",
			Evidence: "synthetic evidence two", Step: "step-verify-2", Result: "confirmed",
			Reason: "synthetic verified reason",
			Closures: []*orreryv1.EpicClosure{
				{Round: 1, Step: "step-closure-2a", Result: "open", Reason: "synthetic open reason"},
				{Round: 2, Step: "step-closure-2b", Result: "closed", Reason: "synthetic closed reason"},
			},
		},
	}
	wantReviews := []*orreryv1.EpicReviewVerdict{
		{
			Position: 1, Reviewer: "synthetic-reviewer-a", Step: "step-review-a", Verdict: "fails",
			Reason: "synthetic verdict reason a",
		},
		{
			Position: 2, Reviewer: "synthetic-reviewer-b", Step: "step-review-b", Verdict: "holds",
			Reason: "synthetic verdict reason b",
		},
	}

	if len(got.GetLedger()) != len(wantLedger) {
		t.Fatalf("ledger = %d findings, want %d", len(got.GetLedger()), len(wantLedger))
	}
	for i := range wantLedger {
		if !proto.Equal(got.GetLedger()[i], wantLedger[i]) {
			t.Errorf("ledger[%d] = %v, want %v", i, got.GetLedger()[i], wantLedger[i])
		}
	}
	if len(got.GetReviews()) != len(wantReviews) {
		t.Fatalf("reviews = %d verdicts, want %d", len(got.GetReviews()), len(wantReviews))
	}
	for i := range wantReviews {
		if !proto.Equal(got.GetReviews()[i], wantReviews[i]) {
			t.Errorf("reviews[%d] = %v, want %v", i, got.GetReviews()[i], wantReviews[i])
		}
	}
}

func TestEpicsGetLedgerEmptyWithoutReview(t *testing.T) {
	t.Parallel()
	unreviewed := storedEpicOf("repoa/unreviewed", "accepted", epicsAgo(time.Hour))
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{unreviewed}}
	client := epicStoreServe(t, &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/unreviewed")
	if len(got.GetLedger()) != 0 || len(got.GetReviews()) != 0 {
		t.Errorf("ledger = %v reviews = %v, want both empty", got.GetLedger(), got.GetReviews())
	}
}

func TestEpicsGetRedactsLedgerText(t *testing.T) {
	t.Parallel()
	epic := storedEpicOf("repoa/leaky", "accepted", epicsAgo(time.Hour))
	epic.Ledger = &enginev1.EpicLedger{
		Verdicts: []*enginev1.EpicReviewVerdict{{
			Position: 1, Reviewer: "synthetic-reviewer", Verdict: "fails",
			Reason: "token ghp_EXAMPLE0123456789abcdefABCDEF0123 ok",
		}},
		Findings: []*enginev1.EpicLedgerFinding{{
			Id: "synthetic-reviewer-1", Position: 1, Reviewer: "synthetic-reviewer", Result: "confirmed",
			Claim:    "Authorization: Bearer EXAMPLEBEARERTOKEN",
			Citation: "SLACK_TOKEN=xoxb-EXAMPLE-0123456789-abcdef",
			Location: "OPENAI_KEY=sk-EXAMPLEKEY0123456789abcd",
			Evidence: "sk-ant-api03-EXAMPLEKEY0123456789ab",
			Reason:   "token ghp_EXAMPLE0123456789abcdefABCDEF0123 ok",
			Closures: []*enginev1.EpicClosure{{
				Round: 1, Result: "open", Reason: "token ghp_EXAMPLE0123456789abcdefABCDEF0123 ok",
			}},
		}},
	}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{epic}}
	client := epicStoreServe(t, &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/leaky")
	if len(got.GetLedger()) != 1 || len(got.GetReviews()) != 1 {
		t.Fatalf("ledger = %d findings, reviews = %d verdicts, want 1 each",
			len(got.GetLedger()), len(got.GetReviews()))
	}
	finding := got.GetLedger()[0]
	if len(finding.GetClosures()) != 1 {
		t.Fatalf("closures = %d, want 1", len(finding.GetClosures()))
	}
	check := func(name, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	check("verdict reason", got.GetReviews()[0].GetReason(), "token [redacted] ok")
	check("finding claim", finding.GetClaim(), "Authorization: [redacted]")
	check("finding citation", finding.GetCitation(), "SLACK_TOKEN=[redacted]")
	check("finding location", finding.GetLocation(), "OPENAI_KEY=[redacted]")
	check("finding evidence", finding.GetEvidence(), "[redacted]")
	check("finding reason", finding.GetReason(), "token [redacted] ok")
	check("closure reason", finding.GetClosures()[0].GetReason(), "token [redacted] ok")
	check("finding id", finding.GetId(), "synthetic-reviewer-1")
	check("finding result", finding.GetResult(), "confirmed")
}
