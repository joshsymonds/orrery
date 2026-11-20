package interpret

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// gapTurnFailure is the reason each costly end of the test reviewer's tries gives, so its gap verdict reads
// "reviewer 1 gave no verdict twice: failed: lint gate crashed".
const gapTurnFailure = "lint gate crashed"

// gapTurnReviewers stands in for the reviewers service with the reviewers it is given, enabled in order. Its starts are
// recorded as reviewWorkReviewers records them.
type gapTurnReviewers struct {
	*reviewWorkReviewers
	enabled []kinds.Reviewer
}

// Epic returns the reviewers the fake is given.
func (r *gapTurnReviewers) Epic(store.EpicRow) []kinds.Reviewer {
	return r.enabled
}

// gapTurnRunner returns a runner over s that starts the steps of the enabled reviewers through steps, and ends the epic
// through s.
func gapTurnRunner(t *testing.T, s *store.Store, steps *reviewLoopSteps, enabled ...kinds.Reviewer) *Runner {
	t.Helper()
	return New(Config{
		Store: s, Planner: &fakePlanner{}, Builder: &fakeBuilder{}, Steps: steps,
		Reviewers: &gapTurnReviewers{reviewWorkReviewers: &reviewWorkReviewers{}, enabled: enabled},
		Ending:    s, Getenv: reviewWorkGetenv(t), Now: runClock,
	})
}

// gapTurnCostlyScript returns the answers of a loop whose one reviewer, test_parsimony, ends both its tries failed, so it
// gives no verdict and the review keeps a gap.
func gapTurnCostlyScript() []reviewLoopEnd {
	return []reviewLoopEnd{
		{path: reviewLoopReview, part: "reviewer 1", end: "failed", status: "failed", reason: gapTurnFailure},
		{path: reviewLoopReview, part: "reviewer 1 try 2", end: "failed", status: "failed", reason: gapTurnFailure},
	}
}

// gapTurnPending returns the gap triggers of the test epic that no planner turn has claimed yet.
func gapTurnPending(t *testing.T, s *store.Store) []store.EpicTrigger {
	t.Helper()
	triggers, err := s.TurnTriggers(context.Background(), runEpic, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	var gaps []store.EpicTrigger
	for _, trigger := range triggers {
		if strings.HasPrefix(trigger.ID, gapTriggerPrefix) {
			gaps = append(gaps, trigger)
		}
	}
	return gaps
}

// gapTurnPlanBefore completes the planner turn the test epic waits on with a plan of no task, as reviewLoopPlanTurn does.
// It claims no turn while a gap trigger is pending, so the gap's own turn is left for the test to answer.
func gapTurnPlanBefore(t *testing.T, s *store.Store) []string {
	t.Helper()
	if len(gapTurnPending(t, s)) > 0 {
		return nil
	}
	return reviewLoopPlanTurn(t, s)
}

// gapTurnClaim claims the planner turn the test epic waits on, and completes it with plan. It returns the seq of the
// claimed turn and the cause of each trigger it claimed.
func gapTurnClaim(t *testing.T, s *store.Store, plan store.EpicPlan) (int64, []string) {
	t.Helper()
	ctx := context.Background()
	turn, err := s.ClaimTurn(ctx, runEpic, runClock())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	triggers, err := s.TurnTriggers(ctx, runEpic, turn.Seq)
	if err != nil {
		t.Fatalf("TurnTriggers of turn %d: %v", turn.Seq, err)
	}
	causes := make([]string, 0, len(triggers))
	for _, trigger := range triggers {
		causes = append(causes, trigger.Cause)
	}
	if err = s.CompleteTurn(ctx, runEpic, turn.Seq, plan, runClock()); err != nil {
		t.Fatalf("CompleteTurn of turn %d: %v", turn.Seq, err)
	}
	return turn.Seq, causes
}

// gapTurnRerunPlan is the planner return of a gap's turn that asks for a rerun of test_parsimony's review, once its cause
// is fixed.
func gapTurnRerunPlan() store.EpicPlan {
	return store.EpicPlan{
		Decisions: []string{}, Outline: "the gap's cause is fixed", Outcome: "complete",
		Actions: []store.EpicActionInput{{
			Kind: store.ActionRerunReview, Reviewer: reviewers.TestParsimony, Reason: "the gap's cause is fixed",
		}},
	}
}

// gapTurnCarryOut carries out each pending planner action of the test epic, as the planner's pass does.
func gapTurnCarryOut(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	actions, err := s.PendingActions(ctx)
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	for _, action := range actions {
		if _, err = s.CarryOutAction(ctx, action, runClock()); err != nil {
			t.Fatalf("CarryOutAction: %v", err)
		}
	}
}

// gapTurnWantPending fails the test unless the test epic has one pending gap trigger, and its cause starts with prefix.
func gapTurnWantPending(t *testing.T, s *store.Store, prefix string) {
	t.Helper()
	pending := gapTurnPending(t, s)
	if len(pending) != 1 || !strings.HasPrefix(pending[0].Cause, prefix) {
		t.Fatalf("pending gap triggers = %+v, want the one gap trigger, its cause starting %q", pending, prefix)
	}
}

// gapTurnWantTurns fails the test unless the gap turns of the test epic are the first turn of gap, and nothing more.
func gapTurnWantTurns(t *testing.T, s *store.Store, gap string) {
	t.Helper()
	turns, err := s.GapTurns(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("GapTurns: %v", err)
	}
	if len(turns) != 1 || turns[0].Gap != gap || turns[0].Turn != 1 {
		t.Errorf("gap turns = %+v, want turn 1 of %s and no other", turns, gap)
	}
}

// TestGapTriggerHoldsReasonAndLedgerGapTurn: an epic whose test_parsimony reviewer gave no verdict twice reaches the
// conclusion, which starts a gap round. The round's pending trigger for the gap holds the verdict's full reason, after
// the gap's name, and the review's ledger.
func TestGapTriggerHoldsReasonAndLedgerGapTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openNodeStore(t)
	createReviewLoopEpic(t, s)
	runner := gapTurnRunner(t, s, &reviewLoopSteps{store: s}, kinds.Reviewer{Name: reviewers.TestParsimony})
	reviewLoopDrive(t, s, runner, gapTurnCostlyScript(), gapTurnPlanBefore)

	pending := gapTurnPending(t, s)
	if len(pending) != 1 {
		t.Fatalf("pending gap triggers = %+v, want the one gap trigger", pending)
	}
	review, err := s.EpicReview(ctx, runEpic)
	if err != nil {
		t.Fatalf("EpicReview: %v", err)
	}
	if len(review.Verdicts) != 1 || review.Verdicts[0].Verdict != "gap" {
		t.Fatalf("verdicts = %+v, want the one gap verdict of test_parsimony", review.Verdicts)
	}
	prefix := "gap reviewer:" + reviewers.TestParsimony + ": reviewer " + reviewers.TestParsimony + ": " +
		review.Verdicts[0].Reason
	if !strings.HasPrefix(pending[0].Cause, prefix) {
		t.Errorf("gap trigger cause = %q, want it to start %q", pending[0].Cause, prefix)
	}
	if !strings.Contains(pending[0].Cause, "\nledger:") {
		t.Errorf("gap trigger cause = %q, want the review's ledger after it", pending[0].Cause)
	}
}

// TestRerunReviewRunsReviewerAgainGapTurn: once the gap's planner turn returned rerun_review for test_parsimony, and the
// action is carried out and the gap round's plan ended, a pass starts a rerun run at <conclude>/R<turn>.<position>/rerun.
// The run prepares at the epic's stored base and starts the reviewer step of test_parsimony. Once that step holds, a pass
// ends the run passed with the holding verdict, and the next pass ends the epic clean.
func TestRerunReviewRunsReviewerAgainGapTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openNodeStore(t)
	createReviewLoopEpic(t, s)
	steps := &reviewLoopSteps{store: s}
	runner := gapTurnRunner(t, s, steps, kinds.Reviewer{Name: reviewers.TestParsimony})
	reviewLoopDrive(t, s, runner, gapTurnCostlyScript(), gapTurnPlanBefore)
	gapTurnWantPending(t, s, "gap reviewer:"+reviewers.TestParsimony+": ")

	turn, _ := gapTurnClaim(t, s, gapTurnRerunPlan())
	gapTurnCarryOut(t, s)
	path := fmt.Sprintf("%s/R%d.1/rerun", concludeNodeKind, turn)
	base := epicOf(t, s).StoredBase

	runner.Pass(ctx)
	if plan := runAt(t, s, "conclude/G1/fix"); plan.State != store.NodeEnded {
		t.Fatalf("gap round plan = %+v, want it ended", plan)
	}
	run := runAt(t, s, path)
	if run.State != store.NodeRunning || run.Node != kindRerun || run.Kind != kindRerun || base == "" ||
		run.Revision != base {
		t.Fatalf("rerun run = %+v, want it running at the stored base %q", run, base)
	}
	stepID := workID(t, s, path, "reviewer 1")
	if !slices.ContainsFunc(steps.calls, func(call enginehost.StepStart) bool {
		return call.ID == stepID && call.Task == path && call.Agent == "reviewer-"+reviewers.TestParsimony
	}) {
		t.Fatalf("steps started = %+v, want the reviewer step of test_parsimony under %s", steps.calls, path)
	}

	reviewLoopAnswer(t, s, []reviewLoopEnd{{path: path, part: "reviewer 1", end: "done", status: "done"}})
	runner.Pass(ctx)
	if run = runAt(t, s, path); run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Fatalf("rerun run = %+v, want it ended passed", run)
	}
	review, err := s.EpicReview(ctx, runEpic)
	if err != nil {
		t.Fatalf("EpicReview: %v", err)
	}
	holds := []store.ReviewVerdict{{Position: 1, Reviewer: reviewers.TestParsimony, Step: stepID, Verdict: "holds"}}
	if !reflect.DeepEqual(review.Verdicts, holds) {
		t.Errorf("verdicts = %+v, want %+v", review.Verdicts, holds)
	}

	runner.Pass(ctx)
	if phase := epicOf(t, s).Phase; phase != "clean" {
		t.Errorf("epic phase = %q, want clean", phase)
	}
}

// TestConcludeWaitsOnPendingActionGapTurn: while the rerun_review action the gap's turn returned is still pending, a pass
// at the conclusion starts no rerun and no second gap round.
func TestConcludeWaitsOnPendingActionGapTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openNodeStore(t)
	createReviewLoopEpic(t, s)
	runner := gapTurnRunner(t, s, &reviewLoopSteps{store: s}, kinds.Reviewer{Name: reviewers.TestParsimony})
	reviewLoopDrive(t, s, runner, gapTurnCostlyScript(), gapTurnPlanBefore)
	gapTurnWantPending(t, s, "gap reviewer:"+reviewers.TestParsimony+": ")
	gapTurnClaim(t, s, gapTurnRerunPlan())

	runner.Pass(ctx)
	runner.Pass(ctx)
	for _, run := range runsOf(t, s) {
		if strings.HasPrefix(run.Path, "conclude/R") || run.Path == "conclude/G2/fix" {
			t.Errorf("node run %s started while the rerun_review action is pending", run.Path)
		}
	}
	if run := runAt(t, s, concludeNodeKind); run.State != store.NodeRunning {
		t.Errorf("conclude run = %+v, want it still running", run)
	}
	actions, err := s.PendingActions(ctx)
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	if len(actions) != 1 {
		t.Errorf("pending actions = %+v, want the rerun_review action still pending", actions)
	}
}

// TestTaskAndClosureGapsGetTurnsGapTurn: a task that ended as a gap, and an undetermined closure of round 1, each is a gap
// of the conclusion. Each gets a gap round of its own, whose pending trigger names the gap in its first line, and records
// the gap's first planner turn.
func TestTaskAndClosureGapsGetTurnsGapTurn(t *testing.T) {
	t.Parallel()
	loopReviewers := []kinds.Reviewer{{Name: "security", Verify: true}, {Name: "tests"}}
	t.Run("task", func(t *testing.T) {
		t.Parallel()
		s, _ := openNodeStore(t)
		createReviewLoopEpic(t, s)
		runner := gapTurnRunner(t, s, &reviewLoopSteps{store: s}, loopReviewers...)
		planned := false
		turn := func(t *testing.T, s *store.Store) []string {
			t.Helper()
			if planned {
				return gapTurnPlanBefore(t, s)
			}
			planned = true
			_, causes := gapTurnClaim(t, s, store.EpicPlan{
				Decisions: []string{}, Tasks: []store.EpicTaskInput{runTask("gapped", "internal/interpret/runner.go")},
				Outline: "one task", Outcome: "complete",
			})
			gapTurnClimbToGap(t, s)
			return causes
		}
		reviewLoopDrive(t, s, runner, reviewLoopHoldsScript(), turn)

		seq := epicOf(t, s).Tasks[0].Seq
		gapTurnWantPending(t, s, fmt.Sprintf("gap task:T%d: ", seq))
		gapTurnWantTurns(t, s, fmt.Sprintf("task:T%d", seq))
	})
	t.Run("closure", func(t *testing.T) {
		t.Parallel()
		s, _ := openNodeStore(t)
		createReviewLoopEpic(t, s)
		runner := gapTurnRunner(t, s, &reviewLoopSteps{store: s}, loopReviewers...)
		reviewLoopDrive(t, s, runner, reviewLoopFindingScript("undetermined"), gapTurnPlanBefore)

		gapTurnWantPending(t, s, "gap closure:security-1:1: ")
		gapTurnWantTurns(t, s, "closure:security-1:1")
	})
}

// gapTurnClimbToGap climbs the test epic's one task to the gap, as its ladder does when it ends as a gap, on the failure
// gapTurnFailure.
func gapTurnClimbToGap(t *testing.T, s *store.Store) {
	t.Helper()
	task := epicOf(t, s).Tasks[0]
	move := store.RungMove{MovedBy: store.MovedByUnit, Cause: gapTurnFailure, To: store.GapRung}
	to := store.ClimbedBuild(task.Build, store.GapRung, gapTurnFailure)
	err := s.ClimbTask(context.Background(), runEpic, task.Seq, task.Build, to, move, nil, nil, runClock())
	if err != nil {
		t.Fatalf("ClimbTask of T%d: %v", task.Seq, err)
	}
	if status := epicOf(t, s).Tasks[0].Status; status != store.TaskGap {
		t.Fatalf("task status = %q, want %q", status, store.TaskGap)
	}
}
