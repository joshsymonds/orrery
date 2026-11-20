package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// gapRoundRun is the plan run a gap round starts on integrateEpic.
func gapRoundRun() NodeRun {
	return NodeRun{Path: "gap-plan", ID: "id-gap-plan", Node: "plan", Kind: "plan", Attempt: 1}
}

// gapTriggersOf returns the stored triggers of integrateEpic whose ids name a gap, as ListEpicTriggers returns them.
func gapTriggersOf(ctx context.Context, t *testing.T, s *Store) []sqlcgen.EpicTrigger {
	t.Helper()
	rows, err := s.fixtureQueries().ListEpicTriggers(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("ListEpicTriggers of epic %s: %v", integrateEpic, err)
	}
	var gapTriggers []sqlcgen.EpicTrigger
	for _, row := range rows {
		if strings.HasPrefix(row.ID, "gap:") {
			gapTriggers = append(gapTriggers, row)
		}
	}
	return gapTriggers
}

// TestStartGapRoundStartsRunTriggersAndTurns checks that StartGapRound stores the plan run as running, two pending
// triggers with their causes and two gap turns with their reasons, and that a repeat of the call changes none of them.
func TestStartGapRoundStartsRunTriggersAndTurns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	triggers := []NodeTrigger{
		{ID: "gap:gap-a", Cause: "gap-a needs the planner"},
		{ID: "gap:gap-b", Cause: "gap-b needs the planner"},
	}
	turns := []GapTurn{
		{Gap: "gap-a", Turn: 1, Reason: "the spec and the code disagree"},
		{Gap: "gap-b", Turn: 1, Reason: "the release needs a credential"},
	}
	start := func() {
		t.Helper()
		if err := s.StartGapRound(ctx, integrateEpic, gapRoundRun(), triggers, turns, gapsAt()); err != nil {
			t.Fatalf("StartGapRound: %v", err)
		}
	}
	check := func() {
		t.Helper()
		nodes, err := s.NodeRuns(ctx, integrateEpic)
		if err != nil {
			t.Fatalf("NodeRuns: %v", err)
		}
		wantRun := NodeRun{
			Path: "gap-plan", ID: "id-gap-plan", Node: "plan", Kind: "plan", State: NodeRunning, Attempt: 1,
			Started: gapsAt(),
		}
		if !reflect.DeepEqual(nodes, []NodeRun{wantRun}) {
			t.Errorf("NodeRuns = %+v, want the running plan run %+v", nodes, wantRun)
		}
		wantTriggers := []sqlcgen.EpicTrigger{
			{Epic: integrateEpic, ID: "gap:gap-a", Cause: "gap-a needs the planner", State: triggerPending,
				Created: gapsAt().Unix()},
			{Epic: integrateEpic, ID: "gap:gap-b", Cause: "gap-b needs the planner", State: triggerPending,
				Created: gapsAt().Unix()},
		}
		if got := gapTriggersOf(ctx, t, s); !reflect.DeepEqual(got, wantTriggers) {
			t.Errorf("gap triggers = %+v, want %+v", got, wantTriggers)
		}
		gotTurns, err := s.GapTurns(ctx, integrateEpic)
		if err != nil {
			t.Fatalf("GapTurns: %v", err)
		}
		wantTurns := []GapTurn{
			{Gap: "gap-a", Turn: 1, Reason: "the spec and the code disagree", Created: gapsAt()},
			{Gap: "gap-b", Turn: 1, Reason: "the release needs a credential", Created: gapsAt()},
		}
		if !reflect.DeepEqual(gotTurns, wantTurns) {
			t.Errorf("GapTurns = %+v, want %+v", gotTurns, wantTurns)
		}
	}

	start()
	check()
	start()
	check()
}

// TestStartGapRoundRefusesSpentGap checks that a gap with turns 1 and 2 recorded is refused a turn 3 with an error
// matching ErrGapSpent, and that the call stores neither its run, nor its triggers, nor its other turns.
func TestStartGapRoundRefusesSpentGap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const gap = "gap-a"
	for _, turn := range []int64{1, 2} {
		if err := s.RecordGapTurn(ctx, integrateEpic, gap, turn, "earlier turn", gapsAt()); err != nil {
			t.Fatalf("RecordGapTurn of turn %d: %v", turn, err)
		}
	}

	err := s.StartGapRound(ctx, integrateEpic, gapRoundRun(),
		[]NodeTrigger{{ID: "gap:gap-a", Cause: "gap-a needs the planner"}},
		[]GapTurn{
			{Gap: "gap-b", Turn: 1, Reason: "a gap with no turns yet"},
			{Gap: gap, Turn: 3, Reason: "a third round"},
		},
		gapsAt())
	if !errors.Is(err, ErrGapSpent) {
		t.Fatalf("StartGapRound of a spent gap = %v, want an error matching %v", err, ErrGapSpent)
	}
	nodes, err := s.NodeRuns(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("NodeRuns: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("NodeRuns = %+v, want none", nodes)
	}
	if got := gapTriggersOf(ctx, t, s); len(got) != 0 {
		t.Errorf("gap triggers = %+v, want none", got)
	}
	got, err := s.GapTurns(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("GapTurns: %v", err)
	}
	want := []GapTurn{
		{Gap: gap, Turn: 1, Reason: "earlier turn", Created: gapsAt()},
		{Gap: gap, Turn: 2, Reason: "earlier turn", Created: gapsAt()},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GapTurns = %+v, want %+v", got, want)
	}
}

// TestReplaceReviewerVerdictReplacesGap checks that replacing test_parsimony's verdict of an ended review, a gap, with
// holds replaces its findings and their closures with the findings given, and keeps floor's verdict, findings and
// closures. It also checks that naming a reviewer with no verdict is an error that changes nothing.
func TestReplaceReviewerVerdictReplacesGap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	reviewendStartFrozen(t, s)
	if err := s.EndReview(ctx, integrateEpic, "review", 1, "review output",
		[]ReviewVerdict{
			{Position: 1, Reviewer: "floor", Step: "id-rev-1", Verdict: "holds", Reason: "the floor holds"},
			{
				Position: 2, Reviewer: "test_parsimony", Step: "id-rev-2", Verdict: "gap",
				Reason: "two tests repeat a check",
			},
		},
		[]LedgerFinding{
			{ID: "floor-1", Position: 1, Reviewer: "floor", Finding: "f-1", Claim: "claim one", Citation: "cite one",
				Location: "a.go:1", Evidence: "evidence one", VerifyBy: "run one", Step: "id-ver-1", Result: "dropped",
				Reason: "not reproduced"},
			{ID: "test_parsimony-1", Position: 2, Reviewer: "test_parsimony", Finding: "f-2", Claim: "claim two",
				Citation: "cite two", Location: "b_test.go:2", Evidence: "evidence two", VerifyBy: "run two",
				Step: "id-ver-2", Result: "confirmed", Reason: "reproduced"},
			{
				ID:         "floor-2",
				Position:   3,
				Reviewer:   "floor",
				Finding:    "f-3",
				Claim:      "claim three",
				Citation:   "cite three",
				Location:   "c.go:3",
				Evidence:   "evidence three",
				VerifyBy:   "run three",
				Result:     "confirmed",
				Unverified: true,
				Reason:     "unverified: reviewer floor has verify off",
			},
		},
		integrateEndedAt()); err != nil {
		t.Fatalf("EndReview: %v", err)
	}
	for _, closure := range []sqlcgen.InsertEpicClosureParams{
		{Epic: integrateEpic, Round: 1, Finding: "floor-1", Step: "id-close-1", Result: "closed", Reason: "fixed"},
		{Epic: integrateEpic, Round: 1, Finding: "test_parsimony-1", Step: "id-close-2", Result: "open",
			Reason: "still repeats"},
		{Epic: integrateEpic, Round: 2, Finding: "floor-2", Step: "id-close-3", Result: "closed", Reason: "fixed"},
	} {
		if err := s.fixtureQueries().InsertEpicClosure(ctx, closure); err != nil {
			t.Fatalf("InsertEpicClosure %s of round %d: %v", closure.Finding, closure.Round, err)
		}
	}
	floorOne := LedgerFinding{ID: "floor-1", Position: 1, Reviewer: "floor", Finding: "f-1", Claim: "claim one",
		Citation: "cite one", Location: "a.go:1", Evidence: "evidence one", VerifyBy: "run one", Step: "id-ver-1",
		Result: "dropped", Reason: "not reproduced",
		Closures: []Closure{{Finding: "floor-1", Round: 1, Step: "id-close-1", Result: "closed", Reason: "fixed"}}}
	floorTwo := LedgerFinding{ID: "floor-2", Position: 3, Reviewer: "floor", Finding: "f-3", Claim: "claim three",
		Citation: "cite three", Location: "c.go:3", Evidence: "evidence three", VerifyBy: "run three",
		Result: "confirmed", Unverified: true, Reason: "unverified: reviewer floor has verify off",
		Closures: []Closure{{Finding: "floor-2", Round: 2, Step: "id-close-3", Result: "closed", Reason: "fixed"}}}
	requireReview(t, s, Review{
		Frozen: "rev-7",
		Verdicts: []ReviewVerdict{
			{Position: 1, Reviewer: "floor", Step: "id-rev-1", Verdict: "holds", Reason: "the floor holds"},
			{
				Position: 2, Reviewer: "test_parsimony", Step: "id-rev-2", Verdict: "gap",
				Reason: "two tests repeat a check",
			},
		},
		Ledger: []LedgerFinding{
			floorOne,
			{ID: "test_parsimony-1", Position: 2, Reviewer: "test_parsimony", Finding: "f-2", Claim: "claim two",
				Citation: "cite two", Location: "b_test.go:2", Evidence: "evidence two", VerifyBy: "run two",
				Step: "id-ver-2", Result: "confirmed", Reason: "reproduced",
				Closures: []Closure{{Finding: "test_parsimony-1", Round: 1, Step: "id-close-2", Result: "open",
					Reason: "still repeats"}}},
			floorTwo,
		},
	})

	replaced := ReviewVerdict{Position: 2, Reviewer: "test_parsimony", Step: "id-rev-3", Verdict: "holds",
		Reason: "the repeated check is gone"}
	replacement := LedgerFinding{ID: "test_parsimony-1", Position: 2, Reviewer: "test_parsimony", Finding: "f-4",
		Claim: "claim four", Citation: "cite four", Location: "b_test.go:4", Evidence: "evidence four",
		VerifyBy: "run four", Step: "id-ver-4", Result: "dropped", Reason: "the check is not repeated"}
	if err := s.ReplaceReviewerVerdict(ctx, integrateEpic, replaced,
		[]LedgerFinding{replacement}, integrateEndedAt()); err != nil {
		t.Fatalf("ReplaceReviewerVerdict: %v", err)
	}
	want := Review{
		Frozen: "rev-7",
		Verdicts: []ReviewVerdict{
			{Position: 1, Reviewer: "floor", Step: "id-rev-1", Verdict: "holds", Reason: "the floor holds"},
			replaced,
		},
		Ledger: []LedgerFinding{floorOne, replacement, floorTwo},
	}
	requireReview(t, s, want)

	err := s.ReplaceReviewerVerdict(ctx, integrateEpic,
		ReviewVerdict{Position: 3, Reviewer: "security", Step: "id-rev-4", Verdict: "holds"},
		[]LedgerFinding{{ID: "security-1", Position: 3, Reviewer: "security", Finding: "f-5", Claim: "claim five",
			Citation: "cite five", Location: "d.go:5", Evidence: "evidence five", VerifyBy: "run five",
			Result: "confirmed", Reason: "reproduced"}},
		integrateEndedAt())
	if err == nil {
		t.Fatalf("ReplaceReviewerVerdict naming security, which holds no verdict: no error, want one")
	}
	requireReview(t, s, want)
}

// TestEpicFlowReadsGapRecords checks that EpicFlow's GapTurns, Unresolvable and Reruns equal what GapTurns,
// UnresolvableGaps and ReviewReruns return after a gap turn is recorded, a turn declares an unresolvable gap, and a
// rerun_review action is carried out.
func TestEpicFlowReadsGapRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	storeReviewVerdict(t, s, integrateEpic, "security", "gap")
	if err := s.RecordGapTurn(ctx, integrateEpic, "gap-a", 1, "first turn", gapsAt()); err != nil {
		t.Fatalf("RecordGapTurn: %v", err)
	}
	if _, err := s.ClaimTurn(ctx, integrateEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, integrateEpic, 1, EpicPlan{
		Outcome:      "work",
		Unresolvable: []GapDeclaration{{Gap: "gap-b", Reason: "needs the owner", Recommended: "release"}},
		Actions: []EpicActionInput{
			{Kind: ActionRerunReview, Reviewer: "security", Reason: "the gap verdict needs a second look"},
		},
	}, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if _, err := s.CarryOutAction(ctx, pendingActionsOf(t, s, integrateEpic)[0], gapsAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}

	gapTurns, err := s.GapTurns(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("GapTurns: %v", err)
	}
	unresolvable, err := s.UnresolvableGaps(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("UnresolvableGaps: %v", err)
	}
	reruns, err := s.ReviewReruns(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("ReviewReruns: %v", err)
	}
	wantTurns := []GapTurn{{Gap: "gap-a", Turn: 1, Reason: "first turn", Created: gapsAt()}}
	wantUnresolvable := []GapDeclaration{{Gap: "gap-b", Reason: "needs the owner", Recommended: "release"}}
	wantReruns := []ReviewRerun{{Reviewer: "security", Turn: 1, Position: 1, Created: gapsAt()}}
	if !reflect.DeepEqual(gapTurns, wantTurns) || !reflect.DeepEqual(unresolvable, wantUnresolvable) ||
		!reflect.DeepEqual(reruns, wantReruns) {
		t.Fatalf("gap turns = %+v, unresolvable = %+v, reruns = %+v; want %+v, %+v, %+v",
			gapTurns, unresolvable, reruns, wantTurns, wantUnresolvable, wantReruns)
	}

	flow, err := s.EpicFlow(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if !reflect.DeepEqual(flow.GapTurns, gapTurns) {
		t.Errorf("EpicFlow gap turns = %+v, want the turns GapTurns returns %+v", flow.GapTurns, gapTurns)
	}
	if !reflect.DeepEqual(flow.Unresolvable, unresolvable) {
		t.Errorf("EpicFlow unresolvable = %+v, want the gaps UnresolvableGaps returns %+v",
			flow.Unresolvable, unresolvable)
	}
	if !reflect.DeepEqual(flow.Reruns, reruns) {
		t.Errorf("EpicFlow reruns = %+v, want the reruns ReviewReruns returns %+v", flow.Reruns, reruns)
	}
}
