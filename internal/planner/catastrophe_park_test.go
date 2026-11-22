package planner

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/store"
)

// requireQuestionPark fails unless the runner epic is parked as class with reason, its one question is the open park question
// with reason, and no pending trigger is a planner park trigger.
func requireQuestionPark(ctx context.Context, t *testing.T, s *store.Store, class store.ParkClass, reason string) {
	t.Helper()
	epic, err := s.Epic(ctx, runnerEpicID)
	if err != nil {
		t.Fatalf("Epic %s: %v", runnerEpicID, err)
	}
	if epic.Parked != reason || epic.ParkClass != class {
		t.Errorf("park of epic = %q %q, want %q %q", epic.ParkClass, epic.Parked, class, reason)
	}
	if len(epic.Questions) != 1 || epic.Questions[0].Label != store.ParkLabel ||
		epic.Questions[0].Text != reason || epic.Questions[0].State != store.QuestionOpen {
		t.Errorf("questions = %+v, want one open park question with text %q", epic.Questions, reason)
	}
	triggers, err := s.TurnTriggers(ctx, runnerEpicID, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	for _, trigger := range triggers {
		if strings.HasPrefix(trigger.ID, "park:") {
			t.Errorf("trigger %s = %+v, want no planner park trigger", trigger.ID, trigger)
		}
	}
}

// requireOneNotice fails unless one attention pass over s routes exactly one notice, and its body names reason and opens
// on the park question.
func requireOneNotice(ctx context.Context, t *testing.T, s *store.Store, reason string) {
	t.Helper()
	router := &fakeAttentionRouter{}
	attentionOver(s, router).pass(ctx)
	if len(router.notices) != 1 {
		t.Fatalf("notices after one attention pass = %d, want exactly one", len(router.notices))
	}
	desk, err := router.notices[0].Desk(ctx)
	if err != nil {
		t.Fatalf("Desk: %v", err)
	}
	if !strings.Contains(desk.Body, reason) || !strings.Contains(desk.Body, "Q1 park:") {
		t.Errorf("notice body = %q, want the park reason %q and its park question", desk.Body, reason)
	}
}

// TestCatastropheParkPlannerRoutesItsOwnerNotice checks that the planner's question-class catastrophe parks, on an epic
// with no steerer, open the park question and route its notice to the owner in one attention pass. An orchestrator whose role or agent does not resolve is parked
// by a Pass, and a met-at-base park on an epic that has had no planner turn is routed although the epic has no turn.
func TestCatastropheParkPlannerRoutesItsOwnerNotice(t *testing.T) {
	t.Parallel()
	t.Run("role does not resolve", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openRunnerStore(t)
		createRunnerEpic(t, s)
		roleErr := errors.New("planner role: no agent")
		var reads atomic.Int64
		runner := New(runnerConfig(s, newFakeSteps(), agentReturning("", roleErr, &reads)))
		runner.Pass(ctx)
		runner.WaitTurns()
		requireQuestionPark(ctx, t, s, store.ParkClassOrchestrator, roleErr.Error())
		requireOneNotice(ctx, t, s, roleErr.Error())
	})
	t.Run("agent does not resolve", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openRunnerStore(t)
		createRunnerEpic(t, s)
		steps := newFakeSteps()
		steps.startErr = agents.ErrUnresolved
		var reads atomic.Int64
		runner := New(runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
		runner.Pass(ctx)
		runner.WaitTurns()
		reason := "start planner step: " + agents.ErrUnresolved.Error()
		requireQuestionPark(ctx, t, s, store.ParkClassOrchestrator, reason)
		requireOneNotice(ctx, t, s, reason)
	})
	t.Run("met at base with no planner turn", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openRunnerStore(t)
		createRunnerEpic(t, s)
		const reason = "workflow node red: R1 met at the epic's base main"
		runs := []store.NodeRun{{Path: "red", ID: "id-red", Node: "red", Kind: "evidence", Attempt: 1}}
		if err := s.StartNodes(ctx, runnerEpicID, runs, nil, claimedAt()); err != nil {
			t.Fatalf("StartNodes: %v", err)
		}
		if err := s.ParkNodeMetAtBase(ctx, runnerEpicID, "red", 1, "failed", "R1 red: failed", reason,
			[]string{"R1"}, claimedAt()); err != nil {
			t.Fatalf("ParkNodeMetAtBase: %v", err)
		}
		requireQuestionPark(ctx, t, s, store.ParkClassMetAtBase, reason)
		requireOneNotice(ctx, t, s, reason)
	})
}
