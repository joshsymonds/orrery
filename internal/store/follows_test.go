package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// followsOther is the second epic the follow tests create beside claimEpic.
const followsOther = "orrery-1a2b3c4/epic-other"

// followExec runs query with args on the test store's db and fails the test when it errors.
func followExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.fixtureExec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestFollowsCountsAnsweredRecommendationsPerLabelAcrossEpics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	createEpic(t, s, fullEpic(followsOther))
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn %s: %v", claimEpic, err)
	}
	if _, err := s.ClaimTurn(ctx, followsOther, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn %s: %v", followsOther, err)
	}

	// CompleteTurn refuses a question with no label or no recommendation, so those two rows are stored valid and
	// their label or recommendation is cleared afterwards.
	// Seq 1 is answered as recommended, 2 with other, 3 stays open, 4 is replaced, 5 has no label and 6 no
	// recommendation.
	first := EpicPlan{Outcome: "wait", Questions: []EpicQuestionInput{
		shapedQuestion("which-store", "Which store holds the plan?"),
		shapedQuestion("which-store", "Which store holds the tells?"),
		shapedQuestion("which-store", "Which store holds the notes?"),
		shapedQuestion("tick-rate", "How often does the runner tick?"),
		shapedQuestion("unlabelled-check", "Unlabelled?"),
		shapedQuestion("tick-rate", "Recommends nothing?"),
	}}
	if err := s.CompleteTurn(ctx, claimEpic, 1, first, completedAt()); err != nil {
		t.Fatalf("CompleteTurn %s: %v", claimEpic, err)
	}
	// Seq 1 and 2 are answered as recommended; seq 3 is answered with a declared choice that is not recommended.
	second := EpicPlan{Outcome: "wait", Questions: []EpicQuestionInput{
		shapedQuestion("which-store", "Which store for the second epic?"),
		shapedQuestion("backend", "Which backend?"),
		shapedQuestion("tick-rate", "How often in the second epic?"),
	}}
	if err := s.CompleteTurn(ctx, followsOther, 1, second, completedAt()); err != nil {
		t.Fatalf("CompleteTurn %s: %v", followsOther, err)
	}

	for _, answer := range []struct {
		epic   string
		seq    int64
		choice string
	}{
		{claimEpic, 1, "first"},
		{claimEpic, 2, ChoiceOther},
		{claimEpic, 5, "first"},
		{claimEpic, 6, "first"},
		{followsOther, 1, "first"},
		{followsOther, 2, "first"},
		{followsOther, 3, "second"},
	} {
		if err := s.fixtureQueries().SetEpicQuestionAnswered(ctx, sqlcgen.SetEpicQuestionAnsweredParams{
			Choice: answer.choice, Epic: answer.epic, Seq: answer.seq,
		}); err != nil {
			t.Fatalf("answer question %d of %s: %v", answer.seq, answer.epic, err)
		}
	}
	followExec(t, s, `UPDATE epic_questions SET state = 'replaced' WHERE epic = ? AND seq = 4`, claimEpic)
	followExec(t, s, `UPDATE epic_questions SET label = '' WHERE epic = ? AND seq = 5`, claimEpic)
	followExec(t, s, `UPDATE epic_questions SET recommended = '' WHERE epic = ? AND seq = 6`, claimEpic)
	noted := completedAt().Add(time.Minute)
	if _, err := s.AnswerEpic(ctx, claimEpic, "D3", "Keep the cheap path.", Maker{}, noted); err != nil {
		t.Fatalf("AnswerEpic note: %v", err)
	}

	got, err := s.Follows(ctx)
	if err != nil {
		t.Fatalf("Follows: %v", err)
	}
	want := []LabelFollow{
		{Label: "backend", Answered: 1, Followed: 1},
		{Label: "tick-rate", Answered: 1, Followed: 0},
		{Label: "which-store", Answered: 3, Followed: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Follows = %+v, want %+v", got, want)
	}
}

func TestFollowsIsNoneWithoutAnAnsweredRecommendation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	got, err := s.Follows(ctx)
	if err != nil {
		t.Fatalf("Follows of an empty store: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Follows of an empty store = %+v, want none", got)
	}

	createEpic(t, s, fullEpic(claimEpic))
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	open := EpicPlan{Outcome: "wait", Questions: []EpicQuestionInput{shapedQuestion("which-store", "Which store?")}}
	if err := s.CompleteTurn(ctx, claimEpic, 1, open, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	got, err = s.Follows(ctx)
	if err != nil {
		t.Fatalf("Follows with an open question: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Follows with only an open question = %+v, want none", got)
	}
}
