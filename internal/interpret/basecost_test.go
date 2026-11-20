package interpret

import (
	"context"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestEpicReviewPromptShowsDeltaBaseCost: the test_parsimony reviewer's prompt, started with the epic's cost checks at
// its base, gives the package's change, its base and the delta in place of the Done gate's baseline line.
func TestEpicReviewPromptShowsDeltaBaseCost(t *testing.T) {
	t.Parallel()
	parsimony := rvReviewer(reviewers.TestParsimony, true, true, reviewers.StageEpic)
	doneGate := store.NodeCheck{
		Stdout: "test-budget: cost internal/store 195.0\n" +
			"test-budget: internal/store 195 cpu-s > 124 (+57%)\n",
	}
	base := store.NodeCheck{Path: "cost", State: store.NodeEnded, Stdout: "test-budget: cost internal/store 190.0\n"}
	got, err := NewEpicReviewers().Start(
		context.Background(), rvEpic(parsimony), reviewers.TestParsimony, "base111", "frozen222",
		[]store.NodeCheck{doneGate}, []store.NodeCheck{base})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := strings.Join([]string{
		rvWantPrompt(), reviewers.TestCostHeading, "- internal/store: 195 cpu-s, base 190 cpu-s, +5",
	}, "\n")
	if got.Prompt != want {
		t.Errorf("Prompt\n got:\n%s\nwant:\n%s", got.Prompt, want)
	}
}

// TestReviewWorkReadsBaseCostBaseCost: the runner's start of an epic-stage test_parsimony unit passes the epic's cost
// check at its base to the reviewers' Start as its base cost.
func TestReviewWorkReadsBaseCostBaseCost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, store.EpicWorkflow{})
	run := reviewWorkFrozenRun(t, s)
	const (
		costID = "cost-check-base"
		stdout = "test-budget: cost internal/store 190.0\n"
	)
	if err := s.AddNodeCheck(ctx, store.NodeCheck{
		ID: costID, Epic: runEpic, Path: "cost", Revision: runBase, Argv: []string{"just", "check"},
	}, runClock()); err != nil {
		t.Fatalf("AddNodeCheck: %v", err)
	}
	if err := s.EndNodeCheck(ctx, costID, store.NodeCheckEnd{Stdout: stdout}, runClock()); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}
	fake := &reviewWorkReviewers{}
	steps := &reviewWorkSteps{fakeSteps: fakeSteps{store: s}}
	runner := reviewWorkRunner(s, steps, fake, reviewWorkGetenv(t))
	parsimonyID := workID(t, s, "review", "reviewer 1")
	act := Action{
		Kind: ActWork, Work: kinds.ActStart, Path: "review", NodeKind: reviewNodeKind, Run: run,
		IDs:   []string{parsimonyID},
		Units: []kinds.Unit{{ID: parsimonyID, Part: "reviewer 1", Reviewer: reviewers.TestParsimony}},
	}
	if err := runner.work(ctx, epicOf(t, s), act, runClock()); err != nil {
		t.Fatalf("work: %v", err)
	}

	if len(fake.calls) != 1 {
		t.Fatalf("reviewer starts = %+v, want 1", fake.calls)
	}
	baseCost := fake.calls[0].baseCost
	if len(baseCost) != 1 || baseCost[0].ID != costID || baseCost[0].Stdout != stdout {
		t.Errorf("test_parsimony base cost checks = %+v, want the check %s with stdout %q", baseCost, costID, stdout)
	}
}
