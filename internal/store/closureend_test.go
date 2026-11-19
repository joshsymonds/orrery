package store

import (
	"context"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// closureEndFindings stores the review of integrateEpic frozen at rev-7 with three confirmed ledger findings in
// position order: conformance-1, conformance-2 and security-1.
func closureEndFindings(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	queries := s.fixtureQueries()
	if err := queries.InsertEpicReview(ctx, sqlcgen.InsertEpicReviewParams{
		Epic: integrateEpic, Frozen: "rev-7",
	}); err != nil {
		t.Fatalf("InsertEpicReview: %v", err)
	}
	for _, finding := range []sqlcgen.InsertEpicLedgerFindingParams{
		{Epic: integrateEpic, ID: "conformance-1", Position: 1, Reviewer: "conformance", Finding: "f-1",
			Step: "id-ver-1", Result: "confirmed", Reason: "reproduced"},
		{Epic: integrateEpic, ID: "conformance-2", Position: 2, Reviewer: "conformance", Finding: "f-2",
			Step: "id-ver-2", Result: "confirmed", Reason: "reproduced"},
		{Epic: integrateEpic, ID: "security-1", Position: 3, Reviewer: "security", Finding: "f-3",
			Step: "id-ver-3", Result: "confirmed", Reason: "reproduced"},
	} {
		if err := queries.InsertEpicLedgerFinding(ctx, finding); err != nil {
			t.Fatalf("InsertEpicLedgerFinding %s: %v", finding.ID, err)
		}
	}
}

// closureEndLedger returns the ledger closureEndFindings stores, each finding with the closures given for its id.
func closureEndLedger(closures map[string][]Closure) []LedgerFinding {
	return []LedgerFinding{
		{ID: "conformance-1", Position: 1, Reviewer: "conformance", Finding: "f-1", Step: "id-ver-1",
			Result: "confirmed", Reason: "reproduced", Closures: closures["conformance-1"]},
		{ID: "conformance-2", Position: 2, Reviewer: "conformance", Finding: "f-2", Step: "id-ver-2",
			Result: "confirmed", Reason: "reproduced", Closures: closures["conformance-2"]},
		{ID: "security-1", Position: 3, Reviewer: "security", Finding: "f-3", Step: "id-ver-3",
			Result: "confirmed", Reason: "reproduced", Closures: closures["security-1"]},
	}
}

// TestEndClosureEndsRunWithRoundAndClosures checks that EndClosure ends the closure run at its attempt failed with its
// output and stores round 1 with its revision, tasks and outcome and one closure of each of three findings. EpicReview
// reads the round and each finding's closure back.
func TestEndClosureEndsRunWithRoundAndClosures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	closureEndFindings(t, s)
	startIntegrateRuns(t, s, NodeRun{Path: "closure", ID: "id-closure", Node: "closure", Kind: "closure", Attempt: 1})

	round := ReviewRound{Round: 1, Revision: "rev-8", Tasks: []int64{3, 4}, Outcome: "failed"}
	closures := []Closure{
		{Finding: "conformance-1", Round: 1, Step: "id-close-1", Result: "closed", Reason: "fixed"},
		{Finding: "conformance-2", Round: 1, Step: "id-close-2", Result: "open", Reason: "still open"},
		{Finding: "security-1", Round: 1, Step: "id-close-3", Result: "undetermined", Reason: "no evidence"},
	}
	if err := s.EndClosure(ctx, integrateEpic, "closure", 1, "failed", "two open", round, closures,
		integrateEndedAt()); err != nil {
		t.Fatalf("EndClosure: %v", err)
	}

	requireIntegrateRun(t, s, "closure", NodeRun{Path: "closure", ID: "id-closure", Node: "closure", Kind: "closure",
		Attempt: 1, Outcome: "failed", Output: "two open", State: NodeEnded, Started: integrateStartedAt(),
		Ended: integrateEndedAt()})
	requireReview(t, s, Review{
		Frozen: "rev-7",
		Ledger: closureEndLedger(map[string][]Closure{
			"conformance-1": {closures[0]},
			"conformance-2": {closures[1]},
			"security-1":    {closures[2]},
		}),
		Rounds: []ReviewRound{round},
	})
}

// TestEndClosureSecondRoundAppendsAndKeepsFirst checks that a closure run ending at round 2 stores its round and
// closures beside round 1, and that round 1 keeps its revision, tasks, outcome and closures. Each finding reads its
// closures by round.
func TestEndClosureSecondRoundAppendsAndKeepsFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	closureEndFindings(t, s)
	startIntegrateRuns(t, s,
		NodeRun{Path: "closure/1", ID: "id-closure-1", Node: "closure", Kind: "closure", Attempt: 1},
		NodeRun{Path: "closure/2", ID: "id-closure-2", Node: "closure", Kind: "closure", Attempt: 1},
	)

	first := ReviewRound{Round: 1, Revision: "rev-8", Tasks: []int64{3, 4}, Outcome: "failed"}
	firstClosures := []Closure{
		{Finding: "conformance-1", Round: 1, Step: "id-close-1", Result: "open", Reason: "still open"},
		{Finding: "conformance-2", Round: 1, Step: "id-close-2", Result: "closed", Reason: "fixed"},
		{Finding: "security-1", Round: 1, Step: "id-close-3", Result: "closed", Reason: "fixed"},
	}
	if err := s.EndClosure(ctx, integrateEpic, "closure/1", 1, "failed", "one open", first, firstClosures,
		integrateEndedAt()); err != nil {
		t.Fatalf("EndClosure of round 1: %v", err)
	}

	second := ReviewRound{Round: 2, Revision: "rev-9", Tasks: []int64{5}, Outcome: "passed"}
	secondClosures := []Closure{
		{Finding: "conformance-1", Round: 2, Step: "id-close-4", Result: "closed", Reason: "fixed now"},
	}
	if err := s.EndClosure(ctx, integrateEpic, "closure/2", 1, "passed", "all closed", second, secondClosures,
		integrateParkedAt()); err != nil {
		t.Fatalf("EndClosure of round 2: %v", err)
	}

	requireIntegrateRun(t, s, "closure/1", NodeRun{Path: "closure/1", ID: "id-closure-1", Node: "closure",
		Kind: "closure", Attempt: 1, Outcome: "failed", Output: "one open", State: NodeEnded,
		Started: integrateStartedAt(), Ended: integrateEndedAt()})
	requireIntegrateRun(t, s, "closure/2", NodeRun{Path: "closure/2", ID: "id-closure-2", Node: "closure",
		Kind: "closure", Attempt: 1, Outcome: "passed", Output: "all closed", State: NodeEnded,
		Started: integrateStartedAt(), Ended: integrateParkedAt()})
	requireReview(t, s, Review{
		Frozen: "rev-7",
		Ledger: closureEndLedger(map[string][]Closure{
			"conformance-1": {firstClosures[0], secondClosures[0]},
			"conformance-2": {firstClosures[1]},
			"security-1":    {firstClosures[2]},
		}),
		Rounds: []ReviewRound{first, second},
	})
}

// TestEndClosureRepeatAndOtherAttemptChangeNothing checks that EndClosure changes nothing when the run it names has
// ended, or is running at another attempt. The ended run keeps its outcome, output and end time, the other run keeps
// running, and the review keeps only round 1 and its closures.
func TestEndClosureRepeatAndOtherAttemptChangeNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	closureEndFindings(t, s)
	startIntegrateRuns(t, s,
		NodeRun{Path: "closure", ID: "id-closure", Node: "closure", Kind: "closure", Attempt: 1},
		NodeRun{Path: "closure/other", ID: "id-other", Node: "closure", Kind: "closure", Attempt: 2},
	)

	round := ReviewRound{Round: 1, Revision: "rev-8", Tasks: []int64{3, 4}, Outcome: "failed"}
	closures := []Closure{
		{Finding: "conformance-1", Round: 1, Step: "id-close-1", Result: "open", Reason: "still open"},
	}
	if err := s.EndClosure(ctx, integrateEpic, "closure", 1, "failed", "first", round, closures,
		integrateEndedAt()); err != nil {
		t.Fatalf("EndClosure: %v", err)
	}

	repeatRound := ReviewRound{Round: 1, Revision: "rev-other", Tasks: []int64{9}, Outcome: "passed"}
	repeatClosures := []Closure{
		{Finding: "conformance-1", Round: 1, Step: "id-close-9", Result: "closed", Reason: "fixed"},
	}
	if err := s.EndClosure(ctx, integrateEpic, "closure", 1, "passed", "again", repeatRound, repeatClosures,
		integrateParkedAt()); err != nil {
		t.Fatalf("repeat EndClosure: %v", err)
	}
	requireIntegrateRun(t, s, "closure", NodeRun{Path: "closure", ID: "id-closure", Node: "closure", Kind: "closure",
		Attempt: 1, Outcome: "failed", Output: "first", State: NodeEnded, Started: integrateStartedAt(),
		Ended: integrateEndedAt()})

	otherRound := ReviewRound{Round: 2, Revision: "rev-9", Tasks: []int64{5}, Outcome: "passed"}
	otherClosures := []Closure{{Finding: "security-1", Round: 2, Step: "id-close-5", Result: "closed", Reason: "fixed"}}
	if err := s.EndClosure(ctx, integrateEpic, "closure/other", 1, "passed", "wrong attempt", otherRound,
		otherClosures, integrateParkedAt()); err != nil {
		t.Fatalf("EndClosure at another attempt: %v", err)
	}
	requireIntegrateRun(t, s, "closure/other", NodeRun{Path: "closure/other", ID: "id-other", Node: "closure",
		Kind: "closure", Attempt: 2, State: NodeRunning, Started: integrateStartedAt()})

	requireReview(t, s, Review{
		Frozen: "rev-7",
		Ledger: closureEndLedger(map[string][]Closure{"conformance-1": {closures[0]}}),
		Rounds: []ReviewRound{round},
	})
}

// TestEndClosureRepeatRoundKeepsFirstStored checks that a closure run ending at a round that is stored already ends
// its own run, and stores neither its round nor its closures: the stored round 1 and its closures stand as they were.
func TestEndClosureRepeatRoundKeepsFirstStored(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	closureEndFindings(t, s)
	startIntegrateRuns(t, s,
		NodeRun{Path: "closure/first", ID: "id-first", Node: "closure", Kind: "closure", Attempt: 1},
		NodeRun{Path: "closure/again", ID: "id-again", Node: "closure", Kind: "closure", Attempt: 1},
	)

	first := ReviewRound{Round: 1, Revision: "rev-8", Tasks: []int64{3, 4}, Outcome: "failed"}
	firstClosures := []Closure{
		{Finding: "conformance-1", Round: 1, Step: "id-close-1", Result: "open", Reason: "still open"},
	}
	if err := s.EndClosure(ctx, integrateEpic, "closure/first", 1, "failed", "first", first, firstClosures,
		integrateEndedAt()); err != nil {
		t.Fatalf("EndClosure: %v", err)
	}

	again := ReviewRound{Round: 1, Revision: "rev-other", Tasks: []int64{9}, Outcome: "passed"}
	againClosures := []Closure{
		{Finding: "conformance-1", Round: 1, Step: "id-close-9", Result: "closed", Reason: "fixed"},
		{Finding: "security-1", Round: 1, Step: "id-close-8", Result: "closed", Reason: "fixed"},
	}
	if err := s.EndClosure(ctx, integrateEpic, "closure/again", 1, "passed", "again", again, againClosures,
		integrateParkedAt()); err != nil {
		t.Fatalf("EndClosure of a repeated round: %v", err)
	}

	requireIntegrateRun(t, s, "closure/again", NodeRun{Path: "closure/again", ID: "id-again", Node: "closure",
		Kind: "closure", Attempt: 1, Outcome: "passed", Output: "again", State: NodeEnded,
		Started: integrateStartedAt(), Ended: integrateParkedAt()})
	requireReview(t, s, Review{
		Frozen: "rev-7",
		Ledger: closureEndLedger(map[string][]Closure{"conformance-1": {firstClosures[0]}}),
		Rounds: []ReviewRound{first},
	})
}

// TestEndClosureRejectsClosureOfAnotherRound checks that EndClosure returns an error for a closure that names a round
// other than round, and that the run stays running with nothing stored.
func TestEndClosureRejectsClosureOfAnotherRound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	closureEndFindings(t, s)
	startIntegrateRuns(t, s, NodeRun{Path: "closure", ID: "id-closure", Node: "closure", Kind: "closure", Attempt: 1})

	round := ReviewRound{Round: 1, Revision: "rev-8", Tasks: []int64{3, 4}, Outcome: "failed"}
	closures := []Closure{
		{Finding: "conformance-1", Round: 2, Step: "id-close-1", Result: "open", Reason: "still open"},
	}
	if err := s.EndClosure(ctx, integrateEpic, "closure", 1, "failed", "red", round, closures,
		integrateEndedAt()); err == nil {
		t.Fatalf("EndClosure of a round 2 closure under round 1: nil error, want an error")
	}

	requireIntegrateRun(t, s, "closure", NodeRun{Path: "closure", ID: "id-closure", Node: "closure", Kind: "closure",
		Attempt: 1, State: NodeRunning, Started: integrateStartedAt()})
	requireReview(t, s, Review{Frozen: "rev-7", Ledger: closureEndLedger(nil)})
}
