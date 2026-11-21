package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// escalationTime is the fixed time the escalations of these tests are made at.
func escalationTime() time.Time {
	return time.Unix(1700000700, 0)
}

// escalatedStored is openStored as an escalation of reason, made by maker at at, leaves it.
func escalatedStored(question EpicQuestionInput, seq int64, reason string, maker Maker, at time.Time) EpicQuestion {
	stored := openStored(question, seq, 1)
	stored.Escalated, stored.EscalationReason, stored.EscalationMaker = at, reason, maker
	return stored
}

func TestEscalate_MarksOpenQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	keepOld := shapedQuestion("keep-old-tasks", "Keep the old tasks?")
	whichStore := shapedQuestion("which-store", "Which store?")
	questionedEpic(t, s, keepOld, whichStore)

	maker := Maker{By: MakerConversation, Session: "session-escalator"}
	at := escalationTime()
	got, err := s.EscalateQuestion(ctx, claimEpic, 2, "No answer in the chain fits", maker, at)
	if err != nil {
		t.Fatalf("EscalateQuestion: %v", err)
	}
	escalated := escalatedStored(whichStore, 2, "No answer in the chain fits", maker, at)
	if !reflect.DeepEqual(got, escalated) {
		t.Errorf("EscalateQuestion = %+v, want %+v", got, escalated)
	}

	epic, err := s.Epic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if want := []EpicQuestion{openStored(keepOld, 1, 1), escalated}; !reflect.DeepEqual(epic.Questions, want) {
		t.Errorf("questions = %+v, want %+v", epic.Questions, want)
	}
}

func TestEscalate_RefusesAnsweredQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	if _, err := s.AnswerQuestion(ctx, claimEpic, "D3", "Keep them", 1, ChoiceOther, Maker{}, claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	before := answerRowsOf(t, s)

	_, err := s.EscalateQuestion(
		ctx, claimEpic, 1, "No answer in the chain fits", Maker{By: MakerOwner}, escalationTime(),
	)
	var refusal RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("EscalateQuestion of an answered question err = %v, want a RefusalError", err)
	}
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("refused EscalateQuestion changed the store:\n got %v\nwant %v", got, before)
	}
}

func TestEscalate_RefusesUnknownQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	before := answerRowsOf(t, s)

	maker := Maker{By: MakerOwner}
	_, err := s.EscalateQuestion(ctx, claimEpic, 9, "No answer in the chain fits", maker, escalationTime())
	if !errors.Is(err, ErrNoQuestion) {
		t.Fatalf("EscalateQuestion of question 9 err = %v, want ErrNoQuestion", err)
	}
	_, err = s.EscalateQuestion(
		ctx, "orrery-0000000/no-such-epic", 1, "No answer in the chain fits", maker, escalationTime(),
	)
	if !errors.Is(err, ErrNoEpic) {
		t.Fatalf("EscalateQuestion of an unknown epic err = %v, want ErrNoEpic", err)
	}
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("refused EscalateQuestion changed the store:\n got %v\nwant %v", got, before)
	}
}

func TestEscalate_RefusesEmptyReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	before := answerRowsOf(t, s)

	for _, reason := range []string{"", " \t "} {
		_, err := s.EscalateQuestion(ctx, claimEpic, 1, reason, Maker{By: MakerOwner}, escalationTime())
		var refusal RefusalError
		if !errors.As(err, &refusal) {
			t.Fatalf("EscalateQuestion with reason %q err = %v, want a RefusalError", reason, err)
		}
	}
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("refused EscalateQuestion changed the store:\n got %v\nwant %v", got, before)
	}
}

func TestEscalate_RefusesSecondEscalation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	whichStore := shapedQuestion("which-store", "Which store?")
	questionedEpic(t, s, whichStore)
	first := Maker{By: MakerConversation, Session: "session-first"}
	firstAt := escalationTime()
	if _, err := s.EscalateQuestion(ctx, claimEpic, 1, "First reason", first, firstAt); err != nil {
		t.Fatalf("first EscalateQuestion: %v", err)
	}

	_, err := s.EscalateQuestion(ctx, claimEpic, 1, "Second reason", Maker{By: MakerOrreryd}, time.Unix(1700000800, 0))
	var refusal RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("second EscalateQuestion err = %v, want a RefusalError", err)
	}

	epic, err := s.Epic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	want := []EpicQuestion{escalatedStored(whichStore, 1, "First reason", first, firstAt)}
	if !reflect.DeepEqual(epic.Questions, want) {
		t.Errorf("questions after refused second escalation = %+v, want %+v", epic.Questions, want)
	}
}

func TestEscalate_MarkEscalationTold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	keepOld := shapedQuestion("keep-old-tasks", "Keep the old tasks?")
	whichStore := shapedQuestion("which-store", "Which store?")
	questionedEpic(t, s, keepOld, whichStore)
	maker := Maker{By: MakerConversation, Session: "session-escalator"}
	at := escalationTime()
	if _, err := s.EscalateQuestion(ctx, claimEpic, 2, "No answer in the chain fits", maker, at); err != nil {
		t.Fatalf("EscalateQuestion: %v", err)
	}

	if err := s.MarkEscalationTold(ctx, claimEpic, 2); err != nil {
		t.Fatalf("MarkEscalationTold: %v", err)
	}
	epic, err := s.Epic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	told := escalatedStored(whichStore, 2, "No answer in the chain fits", maker, at)
	told.EscalationTold = true
	if want := []EpicQuestion{openStored(keepOld, 1, 1), told}; !reflect.DeepEqual(epic.Questions, want) {
		t.Errorf("questions after MarkEscalationTold = %+v, want %+v", epic.Questions, want)
	}

	if err := s.MarkEscalationTold(ctx, claimEpic, 9); !errors.Is(err, ErrNoQuestion) {
		t.Errorf("MarkEscalationTold of question 9 err = %v, want ErrNoQuestion", err)
	}
}
