package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// gapsAt is the second the gap tests stamp on a rerun, a recorded gap turn or a claimed turn.
func gapsAt() time.Time {
	return time.Unix(1700002300, 0)
}

// storeReviewVerdict stores verdict by reviewer as the first verdict of the review of epic, which the review froze at
// rev-1.
func storeReviewVerdict(t *testing.T, s *Store, epic, reviewer, verdict string) {
	t.Helper()
	ctx := context.Background()
	queries := s.fixtureQueries()
	if err := queries.InsertEpicReview(ctx, sqlcgen.InsertEpicReviewParams{Epic: epic, Frozen: "rev-1"}); err != nil {
		t.Fatalf("InsertEpicReview of epic %s: %v", epic, err)
	}
	if err := queries.InsertEpicReviewVerdict(ctx, sqlcgen.InsertEpicReviewVerdictParams{
		Epic: epic, Position: 1, Reviewer: reviewer, Step: "id-rev-1", Verdict: verdict, Reason: "a reviewer's verdict",
	}); err != nil {
		t.Fatalf("InsertEpicReviewVerdict of reviewer %s: %v", reviewer, err)
	}
}

// rerunAction stores a rerun_review action naming reviewer as the pending action of turn 1 of integrateEpic, and returns
// it.
func rerunAction(t *testing.T, s *Store, reviewer string) PlannerAction {
	t.Helper()
	actions := storeActions(t, s, integrateEpic, EpicActionInput{
		Kind: ActionRerunReview, Reviewer: reviewer, Reason: "the gap verdict needs a second look",
	})
	if len(actions) != 1 {
		t.Fatalf("pending actions of epic %s = %d, want 1", integrateEpic, len(actions))
	}
	return actions[0]
}

// TestRerunReviewActionRecordsRerun checks that a rerun_review action naming a reviewer whose verdict the review holds is
// carried out: the action is taken with no steps to stop, and the epic's reruns hold that reviewer with the action's turn
// and position.
func TestRerunReviewActionRecordsRerun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	storeReviewVerdict(t, s, integrateEpic, "security", "gap")
	action := rerunAction(t, s, "security")

	stop, err := s.CarryOutAction(ctx, action, gapsAt())
	if err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	if len(stop) != 0 {
		t.Errorf("CarryOutAction stops = %v, want none", stop)
	}
	if state, refusal := actionState(t, s, action); state != "taken" || refusal != "" {
		t.Errorf("action state = %q refusal %q, want taken with no refusal", state, refusal)
	}
	reruns, err := s.ReviewReruns(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("ReviewReruns: %v", err)
	}
	want := []ReviewRerun{{Reviewer: "security", Turn: 1, Position: 1, Created: gapsAt()}}
	if !reflect.DeepEqual(reruns, want) {
		t.Errorf("ReviewReruns = %+v, want %+v", reruns, want)
	}
}

// TestRerunReviewRefusedWithoutReviewerVerdict checks that a rerun_review action naming a reviewer the review holds no
// verdict by is refused, and stores no rerun.
func TestRerunReviewRefusedWithoutReviewerVerdict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	storeReviewVerdict(t, s, integrateEpic, "conformance", "holds")
	action := rerunAction(t, s, "security")

	stop, err := s.CarryOutAction(ctx, action, gapsAt())
	if err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	if len(stop) != 0 {
		t.Errorf("CarryOutAction stops = %v, want none", stop)
	}
	requireRefusedAction(t, s, action, "rerun review", "epic "+integrateEpic+"'s review holds no verdict by security")
	reruns, err := s.ReviewReruns(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("ReviewReruns: %v", err)
	}
	if len(reruns) != 0 {
		t.Errorf("ReviewReruns = %+v, want none", reruns)
	}
}

// TestCompleteTurnStoresUnresolvableGaps checks that UnresolvableGaps returns the gaps the highest-seq succeeded turn
// declared it cannot resolve, in the order declared, and not the gap an earlier turn declared.
func TestCompleteTurnStoresUnresolvableGaps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, 1, EpicPlan{
		Outcome:      "work",
		Unresolvable: []GapDeclaration{{Gap: "old-gap", Reason: "needs the owner", Recommended: "release"}},
	}, completedAt()); err != nil {
		t.Fatalf("CompleteTurn of turn 1: %v", err)
	}
	if err := s.fixtureQueries().InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: id, ID: "t3", Cause: "test trigger", State: "pending", Turn: 0, Created: epicCreated().Unix(),
	}); err != nil {
		t.Fatalf("insert trigger t3 of %s: %v", id, err)
	}
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn of turn 2: %v", err)
	}
	want := []GapDeclaration{
		{Gap: "gap-a", Reason: "the spec and the code disagree", Recommended: "correct"},
		{Gap: "gap-b", Reason: "the release needs a credential", Recommended: "release"},
	}
	if err := s.CompleteTurn(ctx, id, 2, EpicPlan{Outcome: "work", Unresolvable: want}, completedAt()); err != nil {
		t.Fatalf("CompleteTurn of turn 2: %v", err)
	}

	got, err := s.UnresolvableGaps(ctx, id)
	if err != nil {
		t.Fatalf("UnresolvableGaps: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UnresolvableGaps = %+v, want %+v", got, want)
	}
}

// TestUnresolvableGapsNilWhenLatestTurnDeclaredNone checks that UnresolvableGaps returns nil when the highest-seq
// succeeded turn declared no gap.
func TestUnresolvableGapsNilWhenLatestTurnDeclaredNone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	turn, err := s.ClaimTurn(ctx, id, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, turn.Seq, EpicPlan{Outcome: "work"}, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	got, err := s.UnresolvableGaps(ctx, id)
	if err != nil {
		t.Fatalf("UnresolvableGaps: %v", err)
	}
	if got != nil {
		t.Errorf("UnresolvableGaps = %+v, want nil", got)
	}
}

// TestCompleteTurnRefusesUnknownRecommendation checks that a declaration recommending anything but release or correct
// makes CompleteTurn fail, and that the write stores no declaration and leaves the turn running.
func TestCompleteTurnRefusesUnknownRecommendation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	turn, err := s.ClaimTurn(ctx, id, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}

	err = s.CompleteTurn(ctx, id, turn.Seq, EpicPlan{
		Outcome:      "work",
		Unresolvable: []GapDeclaration{{Gap: "gap-a", Reason: "needs the owner", Recommended: "wait"}},
	}, completedAt())
	if err == nil {
		t.Fatalf("CompleteTurn recommending wait: no error, want one")
	}
	if rows := rowsOf(t, s.db.DB, "planner_unresolvable"); len(rows) != 0 {
		t.Errorf("planner_unresolvable = %v, want no rows", rows)
	}
	running, err := s.RunningTurns(ctx)
	if err != nil {
		t.Fatalf("RunningTurns: %v", err)
	}
	if len(running) != 1 {
		t.Errorf("running turns = %d, want the turn still running", len(running))
	}
}

// TestGapTurnsStopAtTwo checks that a gap takes two planner turns, that a third is refused with ErrGapSpent, and that
// GapTurns holds exactly the two.
func TestGapTurnsStopAtTwo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const gap = "gap-a"
	if err := s.RecordGapTurn(ctx, integrateEpic, gap, 1, "first turn", gapsAt()); err != nil {
		t.Fatalf("RecordGapTurn of turn 1: %v", err)
	}
	if err := s.RecordGapTurn(ctx, integrateEpic, gap, 2, "second turn", gapsAt()); err != nil {
		t.Fatalf("RecordGapTurn of turn 2: %v", err)
	}
	if err := s.RecordGapTurn(ctx, integrateEpic, gap, 3, "third turn", gapsAt()); !errors.Is(err, ErrGapSpent) {
		t.Fatalf("RecordGapTurn of turn 3 = %v, want an error matching %v", err, ErrGapSpent)
	}

	got, err := s.GapTurns(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("GapTurns: %v", err)
	}
	want := []GapTurn{
		{Gap: gap, Turn: 1, Reason: "first turn", Created: gapsAt()},
		{Gap: gap, Turn: 2, Reason: "second turn", Created: gapsAt()},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GapTurns = %+v, want %+v", got, want)
	}
}

// TestRecordGapTurnRepeatChangesNothing checks that recording a gap and turn already recorded returns nil, keeps the
// first record's reason, and spends none of the gap's turns, so the gap still takes its second turn after the repeat.
func TestRecordGapTurnRepeatChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	const gap = "gap-a"
	if err := s.RecordGapTurn(ctx, integrateEpic, gap, 1, "first", gapsAt()); err != nil {
		t.Fatalf("RecordGapTurn of turn 1: %v", err)
	}
	if err := s.RecordGapTurn(ctx, integrateEpic, gap, 1, "repeat", gapsAt()); err != nil {
		t.Fatalf("repeated RecordGapTurn of turn 1: %v", err)
	}
	if err := s.RecordGapTurn(ctx, integrateEpic, gap, 2, "second", gapsAt()); err != nil {
		t.Fatalf("RecordGapTurn of turn 2 after the repeat: %v", err)
	}

	got, err := s.GapTurns(ctx, integrateEpic)
	if err != nil {
		t.Fatalf("GapTurns: %v", err)
	}
	want := []GapTurn{
		{Gap: gap, Turn: 1, Reason: "first", Created: gapsAt()},
		{Gap: gap, Turn: 2, Reason: "second", Created: gapsAt()},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("GapTurns = %+v, want %+v", got, want)
	}
}
