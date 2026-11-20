package build

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/reviewers"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestTaskReviewPromptShowsDeltaBaseCost: with a base measurement, the test_parsimony review prompt gives the package's
// change, its base and the delta in place of the gate's baseline line, and marks the package over only when the change
// exceeds the base by more than the larger of 15% of the base and 5 seconds.
func TestTaskReviewPromptShowsDeltaBaseCost(t *testing.T) {
	t.Parallel()
	base := map[string]float64{"internal/store": 190}
	cases := []struct {
		cost, want string
		over       bool
	}{
		{cost: "195.0", want: "- internal/store: 195 cpu-s, base 190 cpu-s, +5", over: false},
		{cost: "260.0", want: "- internal/store: 260 cpu-s, base 190 cpu-s, +70, over", over: true},
	}
	for _, tc := range cases {
		gate := store.StepCheck{
			Name: gateCheck, OK: true,
			Stdout: "test-budget: cost internal/store " + tc.cost + "\n" +
				"test-budget: internal/store 195 cpu-s > 124 (+57%)\n",
		}
		got := ReviewPrompt(briefEpic(), briefTask(), reviewers.TestParsimony, "base1", "rev1", gate, base)
		want := "\n" + reviewers.TestCostHeading + "\n" + tc.want
		if !strings.HasSuffix(got, want) {
			t.Errorf("review prompt does not end with\n%s\nprompt:\n%s", want, got)
		}
		if strings.Contains(got, "> 124") {
			t.Errorf("review prompt carries the gate's baseline line:\n%s", got)
		}
		if strings.Contains(got, ", over") != tc.over {
			t.Errorf("review prompt over marking = %t, want %t:\n%s", !tc.over, tc.over, got)
		}
	}
}

// TestReviewStartReadsBaseCostBaseCost: the test_parsimony review's start reads the epic's cost checks and compares the
// change's cost in its passed gate with their base, so its prompt carries the delta and not the gate's baseline line.
func TestReviewStartReadsBaseCostBaseCost(t *testing.T) {
	t.Parallel()
	steps := []store.StepRow{gateStep("test-budget: cost internal/store 195.0\n" +
		"test-budget: internal/store 195 cpu-s > 124 (+57%)\n")}
	base := []store.NodeCheck{{
		Path: "cost", State: store.NodeEnded, Stdout: "test-budget: cost internal/store 190.0\n",
	}}
	task := briefTask()
	task.Build = store.TaskBuild{Stage: StageReview, Base: "base1", Revision: "rev1"}
	runner := New(Config{Store: reviewCostStore{steps: steps, base: base}})
	start, err := runner.reviewStart(context.Background(), reviewCostEpic(), task, "review-1")
	if err != nil {
		t.Fatalf("reviewStart: %v", err)
	}
	want := "\n" + reviewers.TestCostHeading + "\n- internal/store: 195 cpu-s, base 190 cpu-s, +5"
	if !strings.HasSuffix(start.Prompt, want) {
		t.Fatalf("review prompt does not end with %q:\n%s", want, start.Prompt)
	}
}

// TestReviewStartBaseCostReadFailsBaseCost: when the epic's cost checks cannot be read, the test_parsimony review's
// start is logged without a base, so its prompt carries the gate's budget lines as they are.
func TestReviewStartBaseCostReadFailsBaseCost(t *testing.T) {
	t.Parallel()
	steps := []store.StepRow{gateStep("test-budget: internal/store 195 cpu-s > 124 (+57%)\n")}
	task := briefTask()
	task.Build = store.TaskBuild{Stage: StageReview, Base: "base1", Revision: "rev1"}
	runner := New(Config{Store: reviewCostStore{steps: steps, baseErr: errors.New("disk gone")}})
	start, err := runner.reviewStart(context.Background(), reviewCostEpic(), task, "review-1")
	if err != nil {
		t.Fatalf("reviewStart: %v", err)
	}
	want := "\n" + reviewers.TestCostHeading + "\n- test-budget: internal/store 195 cpu-s > 124 (+57%)"
	if !strings.HasSuffix(start.Prompt, want) {
		t.Fatalf("review prompt does not end with %q:\n%s", want, start.Prompt)
	}
}
