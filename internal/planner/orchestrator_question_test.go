package planner

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestOrchestratorQuestionNeedsItsCatastrophe asserts that a question naming no catastrophe is refused by a line that
// names the catastrophe field and the four kinds it may be.
func TestOrchestratorQuestionNeedsItsCatastrophe(t *testing.T) {
	t.Parallel()
	bad := validQuestion("second")
	bad.Catastrophe = ""
	want := `questions[1] "second" catastrophe: ` +
		`not one of irreversible-step, contract-cannot-hold, premise-false, cannot-proceed`
	checkLines(t, checkPlan(secondQuestion(bad), nil), want)
}

// TestOrchestratorQuestionIsStoredWithItsCatastropheAndRoutedToTheOwner asserts that a turn returning a question that
// names its catastrophe stores the question with the kind leading its why, and that one attention pass routes the owner
// one notice whose body names the catastrophe.
func TestOrchestratorQuestionIsStoredWithItsCatastropheAndRoutedToTheOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runner := New(runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))

	turn, err := s.ClaimTurn(ctx, runnerEpicID, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = runner.complete(ctx, runnerEpicID, turn.Seq, validPlan); err != nil {
		t.Fatalf("complete turn 1: %v", err)
	}

	epic := epicOf(t, s)
	wantQuestions := []store.EpicQuestion{
		{
			Seq:   1,
			Turn:  1,
			Text:  "which store?",
			State: "open",
			Label: "which-store",
			Choices: []store.EpicChoice{
				{ID: "sqlite", Does: "Keep the store in SQLite."},
				{ID: "files", Does: "Write the store to files."},
			},
			Recommended: "sqlite",
			Why:         "Catastrophe irreversible-step: The tables already hold the epic.",
			Confidence:  "high",
		},
	}
	if !reflect.DeepEqual(epic.Questions, wantQuestions) {
		t.Errorf("questions = %+v, want %+v", epic.Questions, wantQuestions)
	}

	router := &fakeAttentionRouter{}
	attentionOver(s, router).pass(ctx)
	if n := len(router.notices); n != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", n)
	}
	if body := shownOf(t, router.notices[0]).desk.Body; !strings.Contains(body, "Catastrophe irreversible-step:") {
		t.Errorf("notice body = %q, want it to name the catastrophe", body)
	}
}

// TestOrchestratorQuestionRoleDecidesAndAsksOnlyOnCatastrophe asserts that the role text tells the planner to decide and
// record, and to ask the owner only on catastrophe.
func TestOrchestratorQuestionRoleDecidesAndAsksOnlyOnCatastrophe(t *testing.T) {
	t.Parallel()
	for _, want := range []string{"decide and record", "ask the owner only on catastrophe"} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}
