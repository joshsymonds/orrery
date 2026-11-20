package build

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// reviewCostStore is a Store that holds the steps labeled with a task, or fails to read them with err, and the epic's
// cost checks, or fails to read them with baseErr. Only LabelledSteps and BaseCost are called; any other method panics
// on the nil Store it embeds.
type reviewCostStore struct {
	Store
	steps   []store.StepRow
	err     error
	base    []store.NodeCheck
	baseErr error
}

// LabelledSteps returns the steps the fake holds, or its error.
func (f reviewCostStore) LabelledSteps(context.Context, string, string) ([]store.StepRow, error) {
	return f.steps, f.err
}

// BaseCost returns the cost checks the fake holds, or its error.
func (f reviewCostStore) BaseCost(context.Context, string) ([]store.NodeCheck, error) {
	return f.base, f.baseErr
}

// reviewCostEpic is an epic whose only review gate is the test_parsimony reviewer, at the task stage.
func reviewCostEpic() store.EpicRow {
	epic := briefEpic()
	epic.ReviewGates = []string{reviewers.TestParsimony}
	epic.Reviewers = []reviewers.Reviewer{{
		Name: reviewers.TestParsimony, Enabled: true, Stage: []string{reviewers.StageTask},
		MustNot: []string{"The change adds a slow test."}, Model: "sonnet", Limit: 1800,
	}}
	return epic
}

// gateStep returns an implementer step that ran its gate once per stdout, in order.
func gateStep(stdouts ...string) store.StepRow {
	row := store.StepRow{Schema: ImplementerSchema, End: endDone}
	for round, stdout := range stdouts {
		row.Checks = append(row.Checks,
			store.StepCheck{Round: round, Name: "owned", OK: true},
			store.StepCheck{Round: round, Name: gateCheck, OK: round == len(stdouts)-1, Stdout: stdout},
		)
	}
	return row
}

// TestReviewStartGivesTestParsimonyTheLastGateRunOfTheNewestImplementer: the test_parsimony review's prompt carries
// the budget lines of the last gate run of the task's newest implementer step, the run its commit passed, and not those
// of an earlier round, an earlier implementer step or another kind of step.
func TestReviewStartGivesTestParsimonyTheLastGateRunOfTheNewestImplementer(t *testing.T) {
	t.Parallel()
	steps := []store.StepRow{
		gateStep("test-budget: internal/old 90 cpu-s > 40 (+125%)\n"),
		gateStep(
			"test-budget: internal/store 330 cpu-s > 160 (+106%), over twice its baseline\n",
			"test-budget: internal/store 212 cpu-s > 160 (+33%)\ncheck-changed: OK after 97s of its 240s budget\n",
		),
		{Schema: OrchestratorSchema, End: endDone, Checks: []store.StepCheck{
			{Name: gateCheck, Stdout: "test-budget: internal/orchestrated 1 cpu-s, no baseline\n"},
		}},
	}
	task := briefTask()
	task.Build = store.TaskBuild{Stage: StageReview, Base: "base1", Revision: "rev1"}
	runner := New(Config{Store: reviewCostStore{steps: steps}})
	start, err := runner.reviewStart(context.Background(), reviewCostEpic(), task, "review-1")
	if err != nil {
		t.Fatalf("reviewStart: %v", err)
	}
	want := strings.Join([]string{
		reviewers.TestCostHeading,
		"- test-budget: internal/store 212 cpu-s > 160 (+33%)",
		"- check-changed: OK after 97s of its 240s budget",
	}, "\n")
	if !strings.HasSuffix(start.Prompt, "\n"+want) {
		t.Fatalf("review prompt does not end with\n%s\nprompt:\n%s", want, start.Prompt)
	}
}

// TestReviewStartGivesTestParsimonyNoBudgetLinesWithoutAGateRun: a task with no implementer gate run, or whose steps
// cannot be read, gives the test_parsimony review "no budget lines".
func TestReviewStartGivesTestParsimonyNoBudgetLinesWithoutAGateRun(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		store reviewCostStore
	}{
		{name: "no steps", store: reviewCostStore{}},
		{name: "implementer without a gate run", store: reviewCostStore{steps: []store.StepRow{gateStep()}}},
		{name: "steps unreadable", store: reviewCostStore{err: errors.New("disk gone")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			task := briefTask()
			task.Build = store.TaskBuild{Stage: StageReview, Base: "base1", Revision: "rev1"}
			start, err := New(Config{Store: tc.store}).reviewStart(context.Background(), reviewCostEpic(), task, "r")
			if err != nil {
				t.Fatalf("reviewStart: %v", err)
			}
			want := "\n" + reviewers.TestCostHeading + "\n- no budget lines"
			if !strings.HasSuffix(start.Prompt, want) {
				t.Fatalf("review prompt does not end with %q:\n%s", want, start.Prompt)
			}
		})
	}
}
