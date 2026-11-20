package interpret

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// gapRoundReason is the reason the test_parsimony reviewer gives for its gap verdict. gapRoundFullReason is the gap's
// reason, which the conclusion names with the reviewer in front of it, and gapRoundGap the gap's key.
const (
	gapRoundReason     = "the test pins the output"
	gapRoundFullReason = "reviewer test_parsimony: " + gapRoundReason
	gapRoundGap        = "reviewer:test_parsimony"
)

// gapRoundFlow is the workflow the gap round tests run: the plan a gap round replans, which holds build and integrate,
// then the review, then conclude. The plan comes first, so the test's first planner turn ends it, and the review starts
// after it. It is the only plan in the flow with an integrate body, so the gap rounds run it.
func gapRoundFlow() workflow.Workflow {
	fix := kindNode("fix", "plan", kindNode("build", "build"), kindNode("integrate", "integrate"))
	return workflow.Workflow{
		Name:  "gap-round",
		Nodes: []workflow.Node{fix, kindNode("review", "review"), kindNode("conclude", "conclude")},
	}
}

// gapRoundPlanned is the planner return that completes a turn with no task, which the test epic's plan and each of its
// gap rounds take.
func gapRoundPlanned() store.EpicPlan {
	return store.EpicPlan{Decisions: []string{}, Outline: "no task", Outcome: "complete"}
}

// gapRoundRunner returns the runner the gap round tests pass over s. Its review kind waits for the review's freeze,
// then ends with the test_parsimony reviewer's gap verdict.
func gapRoundRunner(s *store.Store) *Runner {
	verdict := store.ReviewVerdict{Position: 1, Reviewer: "test_parsimony", Verdict: "gap", Reason: gapRoundReason}
	review := kinds.Action{Kind: kinds.ActEnd, Output: "test_parsimony: gap", Verdicts: []store.ReviewVerdict{verdict}}
	runner := runnerReviewRunner(s, runnerReviewers{}, s)
	runner.kinds[reviewNodeKind] = runnerWaitsForFreeze(review)
	return runner
}

// gapRoundReachConclude passes the test epic until its conclusion decides. The plan takes one planner turn, completed
// with planned, then the review runs, is frozen and ends with the gap verdict, and the conclusion decides on that gap.
// It returns the runner, which the test keeps passing with.
func gapRoundReachConclude(t *testing.T, s *store.Store, planned store.EpicPlan) *Runner {
	t.Helper()
	ctx := context.Background()
	runner := gapRoundRunner(s)
	row := store.EpicRow{ID: runEpic}
	runner.passFlow(ctx, row, gapRoundFlow())
	completeTurn(t, s, planned)
	runner.passFlow(ctx, row, gapRoundFlow())
	runnerFreezeReview(t, s)
	runner.passFlow(ctx, row, gapRoundFlow())
	return runner
}

// gapRoundPaths returns the paths of the test epic's node runs that its conclusion's gap rounds started, sorted.
func gapRoundPaths(t *testing.T, s *store.Store) []string {
	t.Helper()
	var paths []string
	for _, run := range runsOf(t, s) {
		if strings.HasPrefix(run.Path, "conclude/G") {
			paths = append(paths, run.Path)
		}
	}
	slices.Sort(paths)
	return paths
}

// TestGapRoundStartsPlanWithTriggersAndTurns: a review whose test_parsimony reviewer gives a gap reaches the
// conclusion, which starts a gap round. The round stores the plan run at conclude/G1/fix running, a pending trigger
// for the gap whose cause names it, and the gap's first planner turn.
func TestGapRoundStartsPlanWithTriggersAndTurns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	gapRoundReachConclude(t, s, gapRoundPlanned())

	plan := runAt(t, s, "conclude/G1/fix")
	if plan.State != store.NodeRunning || plan.Node != "fix" || plan.Kind != kindPlan {
		t.Errorf("gap round plan = %+v, want the running plan fix", plan)
	}
	pending, err := s.TurnTriggers(ctx, runEpic, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending triggers = %+v, want the one gap trigger", pending)
	}
	wantCause := "gap " + gapRoundGap + ": reviewer test_parsimony: " + gapRoundReason
	if !strings.HasPrefix(pending[0].Cause, wantCause) || !strings.Contains(pending[0].Cause, "ledger") {
		t.Errorf("gap trigger cause = %q, want it to start %q and name the ledger", pending[0].Cause, wantCause)
	}
	turns, err := s.GapTurns(ctx, runEpic)
	if err != nil {
		t.Fatalf("GapTurns: %v", err)
	}
	if len(turns) != 1 || turns[0].Gap != gapRoundGap || turns[0].Turn != 1 {
		t.Errorf("gap turns = %+v, want turn 1 of %s", turns, gapRoundGap)
	}
}

// TestGapRoundSecondRoundThenEndsAskingOwner: once the first round's plan ends with the gap still there, the
// conclusion starts a second round, which gives the gap its second turn. Once that plan ends, the epic ends
// ended_with_gaps with no third round, and asks the owner one question that holds both turns' reasons.
func TestGapRoundSecondRoundThenEndsAskingOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	runner := gapRoundReachConclude(t, s, gapRoundPlanned())

	completeTurn(t, s, gapRoundPlanned())
	runner.passFlow(ctx, store.EpicRow{ID: runEpic}, gapRoundFlow())
	if run := runAt(t, s, "conclude/G2/fix"); run.State != store.NodeRunning {
		t.Fatalf("second gap round plan = %+v, want it running", run)
	}
	turns, err := s.GapTurns(ctx, runEpic)
	if err != nil {
		t.Fatalf("GapTurns: %v", err)
	}
	if len(turns) != 2 || turns[1].Turn != 2 {
		t.Fatalf("gap turns = %+v, want turns 1 and 2 of the gap", turns)
	}

	completeTurn(t, s, gapRoundPlanned())
	runner.passFlow(ctx, store.EpicRow{ID: runEpic}, gapRoundFlow())
	if phase := epicOf(t, s).Phase; phase != "ended_with_gaps" {
		t.Errorf("epic phase = %q, want ended_with_gaps", phase)
	}
	if paths, want := gapRoundPaths(t, s), []string{"conclude/G1/fix", "conclude/G2/fix"}; !slices.Equal(paths, want) {
		t.Errorf("gap round plans = %q, want %q", paths, want)
	}
	questions, err := s.OpenQuestions(ctx, runEpic)
	if err != nil {
		t.Fatalf("OpenQuestions: %v", err)
	}
	if len(questions) != 1 || questions[0].Label != store.GapsLabel {
		t.Fatalf("open questions = %+v, want one gaps question", questions)
	}
	for _, turn := range []string{"turn 1: " + gapRoundFullReason, "turn 2: " + gapRoundFullReason} {
		if !strings.Contains(questions[0].Text, turn) {
			t.Errorf("gaps question %q does not hold %q", questions[0].Text, turn)
		}
	}
}

// TestGapRoundDeclaredGapEndsAskingOwner: a latest succeeded turn that declares the gap with release starts no round,
// and the epic ends ended_with_gaps with a gaps question that recommends release.
func TestGapRoundDeclaredGapEndsAskingOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	planned := gapRoundPlanned()
	planned.Unresolvable = []store.GapDeclaration{{
		Gap: gapRoundGap, Reason: "the owner decides", Recommended: store.ChoiceRelease,
	}}
	gapRoundReachConclude(t, s, planned)

	if paths := gapRoundPaths(t, s); len(paths) != 0 {
		t.Errorf("gap round plans = %q, want none", paths)
	}
	if phase := epicOf(t, s).Phase; phase != "ended_with_gaps" {
		t.Errorf("epic phase = %q, want ended_with_gaps", phase)
	}
	questions, err := s.OpenQuestions(ctx, runEpic)
	if err != nil {
		t.Fatalf("OpenQuestions: %v", err)
	}
	if len(questions) != 1 || questions[0].Label != store.GapsLabel {
		t.Fatalf("open questions = %+v, want one gaps question", questions)
	}
	if got := questions[0].Recommended; got != store.ChoiceRelease {
		t.Errorf("recommended = %q, want %q", got, store.ChoiceRelease)
	}
	if !strings.Contains(questions[0].Text, "(planner: the owner decides)") {
		t.Errorf("gaps question %q does not hold the planner's declared reason", questions[0].Text)
	}
}
