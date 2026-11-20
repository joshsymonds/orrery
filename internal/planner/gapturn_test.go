package planner

import (
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestPromptShowsGapTriggerGapTurn: a pending gap trigger's cause, which runs over several lines of the gap's reason, the
// review's verdicts and its ledger, reaches the planner's prompt whole, inside the Triggers section.
func TestPromptShowsGapTriggerGapTurn(t *testing.T) {
	t.Parallel()
	cause := "gap reviewer:test_parsimony: reviewer test_parsimony: reviewer 1 gave no verdict twice: failed: " +
		"lint gate crashed\n" +
		"verdicts:\n- test_parsimony: gap: reviewer 1 gave no verdict twice: failed: lint gate crashed\n" +
		"ledger:\n- security-1 (security): confirmed: claim: the claim; reason: a reason\n" +
		"  closure round 1: open: checked"
	trigger := store.EpicTrigger{ID: "gap:3f9c", Cause: cause, State: "claimed", Turn: 2}
	prompt := promptOf(t, syntheticEpic(), 2, []store.EpicTrigger{trigger})
	inOrder(t, prompt, "## Triggers\n", cause, "## Spent lines\n")
}
