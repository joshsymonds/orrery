package planner

import (
	"strings"
	"testing"
)

// TestEpicBaseRoleNoTaskEpicTakesOriginMainAsItsBase asserts that the prompt tells the planner an epic with no task and
// no branch takes origin's main as its base before each planner turn.
func TestEpicBaseRoleNoTaskEpicTakesOriginMainAsItsBase(t *testing.T) {
	t.Parallel()
	prompt := promptOf(t, syntheticEpic(), 2, syntheticTriggers())
	const want = "An epic with no task and no branch takes origin's main as its base before each planner turn"
	if !strings.Contains(prompt, want) {
		t.Errorf("prompt does not say an epic with no task takes origin's main as its base before each turn: want %q",
			want)
	}
}

// TestEpicBaseRoleQuestionNeverAsksToMoveBase asserts that the prompt tells the planner a question never asks the owner
// to move a base, since the base of an epic with no task follows origin's main.
func TestEpicBaseRoleQuestionNeverAsksToMoveBase(t *testing.T) {
	t.Parallel()
	prompt := promptOf(t, syntheticEpic(), 2, syntheticTriggers())
	const want = "so a question never asks the owner to move a base"
	if !strings.Contains(prompt, want) {
		t.Errorf("prompt does not say a question never asks the owner to move a base: want %q", want)
	}
}
