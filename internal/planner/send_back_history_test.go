package planner

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestSendBackHistoryPromptShowsOnlyTheCurrentLadder checks that the planner's prompt reads only the current generation
// of a task sent back. Task 1 fails once, climbing from rung 1 to rung 2, is sent back, and fails again on its reopened
// ladder. The earlier rungs stay in the store under the earlier generation; the prompt's task lines give the reopened
// ladder alone: rung 1 moved by send_back and failed by the second failure, then the rung 2 it climbed to. The first
// failure's rungs are not among its rung lines.
func TestSendBackHistoryPromptShowsOnlyTheCurrentLadder(t *testing.T) {
	t.Parallel()
	const first, reopen, second = "the first build failed", "the commit breaks the order", "the reopened build failed"
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	if _, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, runnerEpicID, 1, store.EpicPlan{
		Tasks: []store.EpicTaskInput{{
			Title: "Build the thing", Deliverable: "The thing holds", Files: []string{"thing.go"},
			Requirements: []string{"R1", "R2"}, Gate: "check",
			Evidence: evidence.Item{Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "it holds", Judge: "j"},
		}},
		Actions: []store.EpicActionInput{
			{Kind: store.ActionFailTask, Task: 1, Reason: first},
			{Kind: store.ActionSendBack, Task: 1, Reason: reopen},
			{Kind: store.ActionFailTask, Task: 1, Reason: second},
		},
		Outline: "One task",
		Outcome: "work",
	}, claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if err := s.SetTaskBuild(ctx, runnerEpicID, 1, store.TaskBuild{},
		store.TaskBuild{Stage: "implement", Attempt: 1, Step: "step-implement"}); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	actions, err := s.PendingActions(ctx)
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	if len(actions) != 3 {
		t.Fatalf("pending actions = %+v, want the three of turn 1", actions)
	}
	for _, action := range actions {
		if _, err = s.CarryOutAction(ctx, action, claimedAt()); err != nil {
			t.Fatalf("CarryOutAction %s: %v", action.Kind, err)
		}
	}

	prompt, err := Prompt(epicOf(t, s), 2, nil, nil, nil, store.BoundChain{})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	var ladder []string
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "  rung ") {
			ladder = append(ladder, line)
		}
	}
	want := []string{
		"  rung 1 attempt moved by send_back: " + reopen + " -> failed: " + second,
		"  rung 2 attempt moved by fail_task: " + second,
	}
	if !slices.Equal(ladder, want) {
		t.Errorf("rung lines of the prompt:\n got %q\nwant %q", ladder, want)
	}
}
