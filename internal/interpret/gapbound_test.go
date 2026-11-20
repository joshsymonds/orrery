package interpret

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// gapBoundRerunPath returns the path of the rerun that the rerun_review action of planner turn turn starts.
func gapBoundRerunPath(turn int64) string {
	return fmt.Sprintf("%s/R%d.1/rerun", concludeNodeKind, turn)
}

// gapBoundRerunCostly returns the answers of the rerun at path of test_parsimony's review, whose two tries both end
// failed, so the rerun gives no verdict.
func gapBoundRerunCostly(path string) []reviewLoopEnd {
	return []reviewLoopEnd{
		{path: path, part: "reviewer 1", end: "failed", status: "failed", reason: gapTurnFailure},
		{path: path, part: "reviewer 1 try 2", end: "failed", status: "failed", reason: gapTurnFailure},
	}
}

// gapBoundNoTurn completes no planner turn, so a pass leaves each planner turn to the test.
func gapBoundNoTurn(*testing.T, *store.Store) []string {
	return nil
}

// gapBoundCountGapCauses returns how many of causes are the cause of a gap trigger of test_parsimony's reviewer.
func gapBoundCountGapCauses(causes []string) int {
	count := 0
	for _, cause := range causes {
		if strings.HasPrefix(cause, "gap reviewer:"+reviewers.TestParsimony+": ") {
			count++
		}
	}
	return count
}

// gapBoundWantTurns fails the test unless the gap turns of the test epic are turn 1 and turn 2 of test_parsimony's
// reviewer, and nothing more.
func gapBoundWantTurns(t *testing.T, s *store.Store) []store.GapTurn {
	t.Helper()
	turns, err := s.GapTurns(context.Background(), runEpic)
	if err != nil {
		t.Fatalf("GapTurns: %v", err)
	}
	gap := "reviewer:" + reviewers.TestParsimony
	if len(turns) != 2 || turns[0].Gap != gap || turns[0].Turn != 1 || turns[1].Gap != gap || turns[1].Turn != 2 {
		t.Fatalf("gap turns = %+v, want turns 1 and 2 of %s and no other", turns, gap)
	}
	return turns
}

// TestReviewerWithoutVerdictGetsTwoTurnsGapBound: a test_parsimony reviewer that gives no verdict twice reaches the
// conclusion, which starts G1 with its gap trigger. Turn 1 reruns the review, and the rerun gives no verdict either;
// the conclusion then starts G2 with the gap's trigger again. Turn 2 reruns the review once more, which gives no
// verdict, and then no third gap round starts: the epic ends ended_with_gaps, and its one gaps question holds both
// turns' reasons.
func TestReviewerWithoutVerdictGetsTwoTurnsGapBound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openNodeStore(t)
	createReviewLoopEpic(t, s)
	runner := gapTurnRunner(t, s, &reviewLoopSteps{store: s}, kinds.Reviewer{Name: reviewers.TestParsimony})
	claimed := reviewLoopDrive(t, s, runner, gapTurnCostlyScript(), gapTurnPlanBefore)
	gapTurnWantPending(t, s, "gap reviewer:"+reviewers.TestParsimony+": ")
	if plan := runAt(t, s, "conclude/G1/fix"); plan.State != store.NodeRunning {
		t.Fatalf("first gap round plan = %+v, want it running", plan)
	}

	turn1, causes1 := gapTurnClaim(t, s, gapTurnRerunPlan())
	gapTurnCarryOut(t, s)
	path1 := gapBoundRerunPath(turn1)
	claimed = append(claimed, causes1...)
	reviewLoopDrive(t, s, runner, gapBoundRerunCostly(path1), gapBoundNoTurn)
	if run := runAt(t, s, path1); run.State != store.NodeEnded {
		t.Fatalf("rerun run after turn 1 = %+v, want it ended", run)
	}
	review, err := s.EpicReview(ctx, runEpic)
	if err != nil {
		t.Fatalf("EpicReview: %v", err)
	}
	if len(review.Verdicts) != 1 || review.Verdicts[0].Reviewer != reviewers.TestParsimony ||
		review.Verdicts[0].Verdict != "gap" {
		t.Fatalf("verdicts after turn 1 = %+v, want the one gap verdict of test_parsimony", review.Verdicts)
	}
	if plan := runAt(t, s, "conclude/G2/fix"); plan.State != store.NodeRunning {
		t.Fatalf("second gap round plan = %+v, want it running", plan)
	}
	gapTurnWantPending(t, s, "gap reviewer:"+reviewers.TestParsimony+": ")

	turn2, causes2 := gapTurnClaim(t, s, gapTurnRerunPlan())
	gapTurnCarryOut(t, s)
	path2 := gapBoundRerunPath(turn2)
	claimed = append(claimed, causes2...)
	reviewLoopDrive(t, s, runner, gapBoundRerunCostly(path2), gapBoundNoTurn)
	if run := runAt(t, s, path2); run.State != store.NodeEnded {
		t.Fatalf("rerun run after turn 2 = %+v, want it ended", run)
	}
	for _, run := range runsOf(t, s) {
		if strings.HasPrefix(run.Path, "conclude/G3") {
			t.Errorf("node run %s started after the gap's second turn", run.Path)
		}
	}
	if phase := epicOf(t, s).Phase; phase != phaseEndedWithGaps {
		t.Errorf("epic phase = %q, want %q", phase, phaseEndedWithGaps)
	}

	turns := gapBoundWantTurns(t, s)
	if got := gapBoundCountGapCauses(claimed); got != 2 {
		t.Errorf("planner turns claimed with a gap trigger = %d, want 2", got)
	}
	questions, err := s.OpenQuestions(ctx, runEpic)
	if err != nil {
		t.Fatalf("OpenQuestions: %v", err)
	}
	if len(questions) != 1 || questions[0].Label != store.GapsLabel {
		t.Fatalf("open questions = %+v, want one gaps question", questions)
	}
	for _, turn := range turns {
		want := fmt.Sprintf("turn %d: %s", turn.Turn, turn.Reason)
		if turn.Reason == "" || !strings.Contains(questions[0].Text, want) {
			t.Errorf("gaps question %q does not hold %q", questions[0].Text, want)
		}
	}
}
