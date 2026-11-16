package planner

import (
	"context"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// The Attention runs against the real store, and its Router is a route.Router or the fake below.
var (
	_ AttentionStore = (*store.Store)(nil)
	_ Router         = (*route.Router)(nil)
)

// limitResetsUnix is the reset time, in unix seconds, of the usage limit the limit test hits.
const limitResetsUnix = 1700003600

// attentionSeen is one Seen call a fakeAttentionRouter received.
type attentionSeen struct {
	key  notice.Key
	seen int64
}

// fakeAttentionRouter records the notices and seen reports its Attention hands it. Each test calls its pass directly,
// so it needs no lock.
type fakeAttentionRouter struct {
	notices []route.Notice
	seens   []attentionSeen
}

func (f *fakeAttentionRouter) Notify(_ context.Context, n route.Notice) {
	f.notices = append(f.notices, n)
}

func (f *fakeAttentionRouter) Seen(_ context.Context, key notice.Key, seen int64) {
	f.seens = append(f.seens, attentionSeen{key: key, seen: seen})
}

// shown is what a notice shows: its key and state, and the desk notice and push it builds.
type shown struct {
	key   notice.Key
	state string
	desk  deskstream.Notice
	push  push.Notification
}

// shownOf returns what n shows.
func shownOf(t *testing.T, n route.Notice) shown {
	t.Helper()
	ctx := context.Background()
	desk, err := n.Desk(ctx)
	if err != nil {
		t.Fatalf("Desk: %v", err)
	}
	note, err := n.Push(ctx)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	return shown{key: n.Key, state: n.State, desk: desk, push: note}
}

// attentionOver returns an Attention over s that hands its notices to router, on the runner clock.
func attentionOver(s *store.Store, router Router) *Attention {
	return NewAttention(AttentionConfig{Store: s, Router: router, Now: claimedAt})
}

// claimAttentionTurn claims the pending turn of the runner epic and returns its seq.
func claimAttentionTurn(t *testing.T, s *store.Store) int64 {
	t.Helper()
	turn, err := s.ClaimTurn(context.Background(), runnerEpicID, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	return turn.Seq
}

// shapedQuestion returns a question that passes the store's refusals: the label and text given, two synthetic choices
// with the first recommended, a medium confidence and a synthetic reason.
func shapedQuestion(label, text string) store.EpicQuestionInput {
	return store.EpicQuestionInput{
		Label: label, Text: text,
		Choices: []store.EpicChoice{
			{ID: "first", Does: "Synthetic first choice."},
			{ID: "second", Does: "Synthetic second choice."},
		},
		Recommended: "first", Why: "Synthetic reason.", Confidence: "medium",
	}
}

// askingTurn returns a turn of the runner epic that asks two questions and succeeds.
func askingTurn() store.EpicPlan {
	return store.EpicPlan{
		Questions: []store.EpicQuestionInput{
			shapedQuestion("which-store", "which store?"),
			shapedQuestion("which-tick", "which tick?"),
		},
		Outcome: "wait",
	}
}

// askingBody is the body of the notice askingTurn sends: each question's head, then its advice lines.
const askingBody = `Q1 which-store: which store?
  first: Synthetic first choice.
  second: Synthetic second choice.
  other: answer in your own words
  recommended: first (medium): Synthetic reason.
Q2 which-tick: which tick?
  first: Synthetic first choice.
  second: Synthetic second choice.
  other: answer in your own words
  recommended: first (medium): Synthetic reason.`

func TestAttentionSendsOneNoticeForTheQuestionsOfATurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, askingTurn(), claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	router := &fakeAttentionRouter{}
	attention := attentionOver(s, router)

	attention.pass(ctx)

	want := shown{
		key:   notice.Key{Epic: runnerEpicID},
		state: "waiting",
		desk: deskstream.Notice{
			State: "waiting", Title: runnerEpicID, Body: askingBody, Block: "question", BlockRef: "Q1",
		},
		push: push.Notification{
			Project: "Plan the runner", State: protocol.StateWaiting, Summary: askingBody,
			Block: "question", BlockRef: "Q1",
		},
	}
	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}
	if got := shownOf(t, router.notices[0]); got != want {
		t.Errorf("notice = %+v, want %+v", got, want)
	}

	attention.pass(ctx)
	attentionOver(s, router).pass(ctx)
	if n := len(router.notices); n != 1 {
		t.Errorf("notices after a second pass and a restarted Attention = %d, want still one", n)
	}
}

// creditParkBody is the body of the notice of a park for credit ran out whose question is not final: the reason, then
// the park question's head and its retry advice.
const creditParkBody = `parked: credit ran out
Q1 park:
  retry: end the park and start a turn on the waiting triggers, as orrery epic retry does
  other: answer in your own words
  recommended: retry (low): no model judges a park; retry runs the epic again on its waiting triggers`

func TestAttentionSendsTheReasonOfAPark(t *testing.T) {
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
	attention := attentionOver(s, router)

	attention.pass(ctx)

	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}
	if got := shownOf(t, router.notices[0]).desk.Body; got != creditParkBody {
		t.Errorf("park notice body = %q, want %q", got, creditParkBody)
	}

	attention.pass(ctx)

	if n := len(router.notices); n != 1 {
		t.Errorf("notices after a second pass = %d, want still one", n)
	}
}

func TestAttentionSendsTheResetTimeOfAUsageLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.FreeTurn(ctx, runnerEpicID, turn, store.TurnFree{
		Reason: "usage limit", Pause: true, Resets: time.Unix(limitResetsUnix, 0),
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

func TestAttentionClearsTheNoticeOnceTheLastQuestionIsAnswered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, store.EpicPlan{
		Questions: []store.EpicQuestionInput{shapedQuestion("which-store", "which store?")}, Outcome: "wait",
	}, claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	router := &fakeAttentionRouter{}
	attention := attentionOver(s, router)
	attention.pass(ctx)
	if len(router.notices) != 1 {
		t.Fatalf("notices before the answer = %d, want exactly one", len(router.notices))
	}
	if len(router.seens) != 0 {
		t.Fatalf("seen reports before the answer = %d, want none", len(router.seens))
	}
	if _, err := s.AnswerQuestion(
		ctx, runnerEpicID, "answer-1", "use the store", 1, store.ChoiceOther, claimedAt(),
	); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}

	attention.pass(ctx)

	if n := len(router.notices); n != 1 {
		t.Errorf("notices after the answer = %d, want no new notice", n)
	}
	want := attentionSeen{key: notice.Key{Epic: runnerEpicID}, seen: claimedAt().Unix()}
	if len(router.seens) != 1 || router.seens[0] != want {
		t.Errorf("seen reports after the answer = %+v, want exactly [%+v]", router.seens, want)
	}
}

func TestAttentionMarksATurnWithNoQuestionsToldWithoutTellingTheOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, store.EpicPlan{Outcome: "work"}, claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	router := &fakeAttentionRouter{}

	attentionOver(s, router).pass(ctx)

	if n := len(router.notices); n != 0 {
		t.Errorf("notices for a turn with no questions = %d, want none", n)
	}
	turns := epicOf(t, s).Turns
	if len(turns) != 1 || !turns[0].Told {
		t.Errorf("turns = %+v, want one turn marked told", turns)
	}
}

func TestRestoreNoticeRebuildsTheNoticeAttentionSent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	turn := claimAttentionTurn(t, s)
	if err := s.CompleteTurn(ctx, runnerEpicID, turn, askingTurn(), claimedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	router := &fakeAttentionRouter{}
	attentionOver(s, router).pass(ctx)
	if len(router.notices) != 1 {
		t.Fatalf("notices after one pass = %d, want exactly one", len(router.notices))
	}
	sent := router.notices[0]
	if sent.Source.Kind != EpicSource {
		t.Fatalf("source kind = %q, want %q", sent.Source.Kind, EpicSource)
	}

	restored, err := RestoreNotice(ctx, sent.Source.Data, claimedAt().Unix())
	if err != nil {
		t.Fatalf("RestoreNotice: %v", err)
	}
	if got, want := shownOf(t, restored), shownOf(t, sent); got != want {
		t.Errorf("restored notice = %+v, want the sent notice %+v", got, want)
	}
}

func TestAttentionBodyShowsAnUnlabelledQuestionAsItsTextAndOtherOnly(t *testing.T) {
	t.Parallel()
	entry := store.EpicAttention{Open: []store.EpicQuestion{{
		Seq: 3, Turn: 1, Text: "an old question", State: store.QuestionOpen,
	}}}

	want := "Q3: an old question\n  other: answer in your own words"
	if got := bodyOf(needQuestions, entry); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestAttentionBodyOfAFinalParkShowsOnlyOther(t *testing.T) {
	t.Parallel()
	entry := store.EpicAttention{
		Parked: "budget spent",
		Open: []store.EpicQuestion{{
			Seq: 4, Turn: 0, Text: "budget spent", State: store.QuestionOpen, Label: store.ParkLabel,
			Choices: []store.EpicChoice{},
		}},
	}

	want := "parked: budget spent\nQ4 park:\n  other: answer in your own words"
	if got := bodyOf(needPark, entry); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}
