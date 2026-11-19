package planner

import (
	"context"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/store"
)

// runnerContractTitle is the contract title of the runner epic. A park notice's desk notice is titled by it.
const runnerContractTitle = "Plan the runner"

// implementerMoveText is the text of the tell an implementer's ref move writes, as build writes it.
const implementerMoveText = "T1 implementer moved internal/planner/runner.go (a1b2c3 to d4e5f6)"

// nodeMoveText is the text of the tell a workflow node's ref move writes, as interpret writes it.
const nodeMoveText = "workflow node build/T1/check moved internal/planner/runner.go (a1b2c3 to d4e5f6)"

// retryAdvice is the advice of the park question of the runner epic's park that is not final: its head and its advice
// lines, which a park's notice body leads with.
const retryAdvice = `Q1 park:
  retry: end the park and start a turn on the waiting triggers, as orrery epic retry does
  other: answer in your own words
  recommended: retry (low): no model judges a park; retry runs the epic again on its waiting triggers`

// settledRunner ends the planned work of the runner epic and passes once, so its turn is told and no notice is out.
func settledRunner(t *testing.T, s *store.Store) {
	t.Helper()
	endWorkTurn(t, s, claimedAt())
	attentionOver(s, &fakeAttentionRouter{}).pass(context.Background())
}

// tellsOfRunner returns every tell of the runner epic in seq order, told or not.
func tellsOfRunner(t *testing.T, s *store.Store) []store.EpicTell {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), runnerEpicID)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	return history.Tells
}

// parkNoticeWant is what the park notice of the runner epic shows with body: the epic's contract title on the desk, and
// the block of its park question, Q1.
func parkNoticeWant(body string) shown {
	return shown{
		key:   notice.Key{Epic: runnerEpicID},
		state: "waiting",
		desk: deskstream.Notice{
			State: "waiting", Title: runnerContractTitle, Body: body, Block: "question", BlockRef: "Q1",
		},
		push: push.Notification{
			Project:  runnerContractTitle,
			State:    protocol.StateWaiting,
			Summary:  body,
			Block:    "question",
			BlockRef: "Q1",
		},
	}
}

func TestQuietImplementerTellGetsNoNoticeAndStaysInTheEpic(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text string
	}{
		{name: "an implementer move", text: implementerMoveText},
		{name: "a workflow node move", text: nodeMoveText},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openRunnerStore(t)
			createRunnerEpic(t, s)
			endWorkTurn(t, s, claimedAt())
			tellRunner(t, s, tc.text, claimedAt().Add(time.Second))
			router := &fakeAttentionRouter{}

			attentionOver(s, router).pass(ctx)

			if n := len(router.notices); n != 0 {
				t.Errorf("notices for the move = %d, want none", n)
			}
			if tells := untoldTellsOfRunner(t, s); len(tells) != 0 {
				t.Errorf("untold tells after the pass = %+v, want none", tells)
			}
			tells := tellsOfRunner(t, s)
			if len(tells) != 1 || tells[0].Text != tc.text || !tells[0].Told {
				t.Errorf("tells = %+v, want the move kept and marked told", tells)
			}
		})
	}
}

func TestQuietImplementerTellAfterAnAnsweredQuestionLeavesTheEpicNeedingNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, store.EpicPlan{
		Questions: []store.EpicQuestionInput{shapedQuestion("which-store", "which store?")},
		Tasks:     plannedRunnerTasks(),
		Outcome:   "work",
	}, claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	router := &fakeAttentionRouter{}
	attention := attentionOver(s, router)
	attention.pass(ctx)
	if len(router.notices) != 1 {
		t.Fatalf("notices before the answer = %d, want the question's one", len(router.notices))
	}
	if _, err := s.AnswerQuestion(
		ctx, runnerEpicID, "answer-1", "use the store", 1, store.ChoiceOther, store.Maker{},
		claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	tellRunner(t, s, implementerMoveText, claimedAt().Add(time.Second))

	attention.pass(ctx)

	if n := len(router.notices); n != 1 {
		t.Errorf("notices after the answer and the move = %d, want no new notice", n)
	}
	want := attentionSeen{key: notice.Key{Epic: runnerEpicID}, seen: claimedAt().Unix()}
	if len(router.seens) != 1 || router.seens[0] != want {
		t.Errorf("seen reports after the answer and the move = %+v, want exactly [%+v]", router.seens, want)
	}
	if tells := untoldTellsOfRunner(t, s); len(tells) != 0 {
		t.Errorf("untold tells after the pass = %+v, want none", tells)
	}
}

func TestQuietImplementerTellBesideTheEndingTellNotifiesOnlyTheEnding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	endWorkTurn(t, s, claimedAt())
	tellRunner(t, s, implementerMoveText, claimedAt().Add(time.Second))
	if err := s.EndEpic(
		ctx, runnerEpicID, "released", "Released. No gaps.", nil, claimedAt().Add(2*time.Second),
	); err != nil {
		t.Fatalf("EndEpic released: %v", err)
	}
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices for the move and the ending = %d, want exactly the ending's", len(router.notices))
	}
	want := shown{
		key:   notice.Key{Epic: runnerEpicID},
		state: "waiting",
		desk:  deskstream.Notice{State: "waiting", Title: runnerEpicID, Body: "Released. No gaps.", Block: "report"},
		push: push.Notification{
			Project: runnerContractTitle, State: protocol.StateWaiting, Summary: "Released. No gaps.", Block: "report",
		},
	}
	if got := shownOf(t, router.notices[0]); got != want {
		t.Errorf("notice = %+v, want %+v", got, want)
	}
	if tells := untoldTellsOfRunner(t, s); len(tells) != 0 {
		t.Errorf("untold tells after the pass = %+v, want none", tells)
	}
}

func TestQuietImplementerTellWorkflowNodeParkMoveStillRoutes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	settledRunner(t, s)
	build := store.TaskBuild{}
	// A workflow node's move that parks writes its reason as a tell that carries the park question, as an implementer's
	// does, so it routes like one.
	if err := s.ParkTask(
		ctx, runnerEpicID, 1, build, build, store.ParkClassRefMove, nodeMoveText, claimedAt().Add(time.Second),
	); err != nil {
		t.Fatalf("ParkTask: %v", err)
	}
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices for the parking node move = %d, want exactly one", len(router.notices))
	}
	want := parkNoticeWant(retryAdvice + "\nparked: " + nodeMoveText)
	if got := shownOf(t, router.notices[0]); got != want {
		t.Errorf("notice = %+v, want %+v", got, want)
	}
}

func TestQuietParkNeedRoutesTheParkUnderItsContractTitle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.FreeTurn(ctx, runnerEpicID, turn, store.TurnFree{
		Reason: "credit ran out", Park: "credit ran out",
	}, claimedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}
	if got, want := shownOf(t, router.notices[0]), parkNoticeWant(creditParkBody); got != want {
		t.Errorf("notice = %+v, want %+v", got, want)
	}
}

func TestQuietParkNeedTellWithItsTurnToldRoutesOneNotice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	settledRunner(t, s)
	build := store.TaskBuild{}
	if err := s.ParkTask(
		ctx, runnerEpicID, 1, build, build, store.ParkClassRefMove, implementerMoveText, claimedAt().Add(time.Second),
	); err != nil {
		t.Fatalf("ParkTask: %v", err)
	}
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}
	want := parkNoticeWant(retryAdvice + "\nparked: " + implementerMoveText)
	if got := shownOf(t, router.notices[0]); got != want {
		t.Errorf("notice = %+v, want %+v", got, want)
	}
}

func TestQuietParkNeedFinalParkTellLeadsWithItsReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	settledRunner(t, s)
	// Ending at the base sets the phase a final park reads, and its ending tell is routed and told before the park.
	if err := s.EndEpic(
		ctx, runnerEpicID, "met_at_base", "Met at the base.", nil, claimedAt().Add(time.Second),
	); err != nil {
		t.Fatalf("EndEpic met_at_base: %v", err)
	}
	attentionOver(s, &fakeAttentionRouter{}).pass(ctx)
	if err := s.ParkEpic(
		ctx, runnerEpicID, store.ParkClassBranch, "budget spent", claimedAt().Add(2*time.Second),
	); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after the final park = %d, want exactly one", len(router.notices))
	}
	want := parkNoticeWant("parked: budget spent\nQ1 park:\n  other: answer in your own words")
	if got := shownOf(t, router.notices[0]); got != want {
		t.Errorf("notice = %+v, want %+v", got, want)
	}
}

func TestQuietParkNeedBodyOfAParkWithNoQuestionIsItsReason(t *testing.T) {
	t.Parallel()
	if got, want := parkBody("budget spent", nil), "parked: budget spent"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}
