package planner

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

func TestRunnerRejectedLimitWithNoResetTimeSendsOneNotice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	var reads atomic.Int64
	runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitForStarts(t, steps, 1)
	waitFor(t, "step-1 recorded on turn 1", func() bool { return stepOneOnTurnOne(t, s) })
	if err := s.ApplyEngine(ctx, []store.EngineOp{store.EngineRateLimited{
		ID: "step-1", Status: "rejected", Kind: "five_hour",
	}}, claimedAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	steps.end("step-1", store.StepRow{End: "checks_failed", Reason: "tasks[0] wrong"})
	waitFor(t, "the epic to be paused", func() bool { return epicOf(t, s).Paused })
	router := &fakeAttentionRouter{}
	attention := attentionOver(s, router)

	attention.pass(ctx)
	attention.pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after two passes = %d, want exactly one", len(router.notices))
	}
	want := "usage limit, reset time unknown"
	if got := shownOf(t, router.notices[0]).push.Summary; got != want {
		t.Errorf("limit notice summary = %q, want %q", got, want)
	}
}

func TestAttentionSendsOneNoticeForALimitWithNoResetTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.FreeTurn(ctx, runnerEpicID, turn, store.TurnFree{
		Reason: "usage limit", Pause: true, Limit: true,
	}, claimedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	router := &fakeAttentionRouter{}
	attention := attentionOver(s, router)

	attention.pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}
	want := "usage limit, reset time unknown"
	if got := shownOf(t, router.notices[0]).push.Summary; got != want {
		t.Errorf("limit notice summary = %q, want %q", got, want)
	}

	attention.pass(ctx)
	attentionOver(s, router).pass(ctx)
	if n := len(router.notices); n != 1 {
		t.Errorf("notices after a second pass and a restarted Attention = %d, want still one", n)
	}
}

func TestAttentionSendsTheResetTimeOfALimitThatNamesOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.FreeTurn(ctx, runnerEpicID, turn, store.TurnFree{
		Reason: "usage limit", Pause: true, Limit: true, Resets: time.Unix(limitResetsUnix, 0),
	}, claimedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}
	want := "usage limit, resets 2023-11-14T23:13:20Z"
	if got := shownOf(t, router.notices[0]).push.Summary; got != want {
		t.Errorf("limit notice summary = %q, want %q", got, want)
	}
}
