package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// questionedEpic creates claimEpic from fullEpic and completes its planner turn 1 with the given questions, so they are
// stored open in seq order from 1, each asked by turn 1.
func questionedEpic(t *testing.T, s *Store, questions ...EpicQuestionInput) {
	t.Helper()
	ctx := context.Background()
	createEpic(t, s, fullEpic(claimEpic))
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := plannedTurn()
	plan.Questions = questions
	if err := s.CompleteTurn(ctx, claimEpic, 1, plan, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// answerRowsOf returns the rows of every table an answer can write, by table name.
func answerRowsOf(t *testing.T, s *Store) map[string][][]any {
	t.Helper()
	out := make(map[string][][]any)
	for _, table := range []string{"epics", "epic_decisions", "epic_questions", "epic_triggers"} {
		out[table] = rowsOf(t, s.db.DB, table)
	}
	return out
}

func TestAnswerQuestionStoresTiedDecisionClosesQuestionAndAddsOneTrigger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	keepOld := shapedQuestion("keep-old-tasks", "Keep the old tasks?")
	whichStore := shapedQuestion("which-store", "Which store?")
	questionedEpic(t, s, keepOld, whichStore)

	at := time.Unix(1700000600, 0)
	got, err := s.AnswerQuestion(ctx, id, "D3", "Keep them", 2, ChoiceOther, at)
	if err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	answer := EpicDecision{Seq: 3, ID: "D3", Text: "Keep them", Time: at, Question: 2}
	if !reflect.DeepEqual(got, answer) {
		t.Errorf("AnswerQuestion = %+v, want %+v", got, answer)
	}

	epic, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if want := append(seededDecisions(), answer); !reflect.DeepEqual(epic.Decisions, want) {
		t.Errorf("decisions = %+v, want %+v", epic.Decisions, want)
	}
	answered := openStored(whichStore, 2, 1)
	answered.State, answered.Choice = "answered", ChoiceOther
	wantQuestions := []EpicQuestion{openStored(keepOld, 1, 1), answered}
	if !reflect.DeepEqual(epic.Questions, wantQuestions) {
		t.Errorf("questions = %+v, want %+v", epic.Questions, wantQuestions)
	}
	if !epic.Changed.Equal(at) {
		t.Errorf("Changed = %v, want %v", epic.Changed, at)
	}

	wantTriggers := [][]any{
		{id, createdTriggerID, createdTriggerCause, "consumed", int64(1), int64(1700000000)},
		{id, "answer:D3", "answer D3 to Q2", "pending", int64(0), int64(1700000600)},
	}
	if got := rowsOf(t, s.db.DB, "epic_triggers"); !reflect.DeepEqual(got, wantTriggers) {
		t.Errorf("epic_triggers after AnswerQuestion = %v, want %v", got, wantTriggers)
	}
}

func TestAnswerQuestionWithDeclaredChoiceRecordsTheChoiceAndKeepsTheText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	keepOld := shapedQuestion("keep-old-tasks", "Keep the old tasks?")
	whichStore := shapedQuestion("which-store", "Which store?")
	questionedEpic(t, s, keepOld, whichStore)

	at := time.Unix(1700000600, 0)
	withWords, err := s.AnswerQuestion(ctx, id, "D3", "Keep them, they still run", 1, "second", at)
	if err != nil {
		t.Fatalf("AnswerQuestion with a declared choice and words: %v", err)
	}
	withWordsWant := EpicDecision{Seq: 3, ID: "D3", Text: "Keep them, they still run", Time: at, Question: 1}
	if !reflect.DeepEqual(withWords, withWordsWant) {
		t.Errorf("AnswerQuestion = %+v, want %+v", withWords, withWordsWant)
	}
	bare, err := s.AnswerQuestion(ctx, id, "D4", "", 2, "first", at)
	if err != nil {
		t.Fatalf("AnswerQuestion with a declared choice and no words: %v", err)
	}
	if want := (EpicDecision{Seq: 4, ID: "D4", Text: "", Time: at, Question: 2}); !reflect.DeepEqual(bare, want) {
		t.Errorf("AnswerQuestion with no words = %+v, want %+v", bare, want)
	}

	epic, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	firstAnswered := openStored(keepOld, 1, 1)
	firstAnswered.State, firstAnswered.Choice = "answered", "second"
	secondAnswered := openStored(whichStore, 2, 1)
	secondAnswered.State, secondAnswered.Choice = "answered", "first"
	if want := []EpicQuestion{firstAnswered, secondAnswered}; !reflect.DeepEqual(epic.Questions, want) {
		t.Errorf("questions = %+v, want %+v", epic.Questions, want)
	}
}

func TestAnswerQuestionWithChoiceTheQuestionDoesNotOfferIsErrNoChoiceAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	before := answerRowsOf(t, s)

	for _, choice := range []string{"", "third"} {
		_, err := s.AnswerQuestion(ctx, id, "D3", "Keep them", 1, choice, claimedAt())
		if !errors.Is(err, ErrNoChoice) {
			t.Fatalf("AnswerQuestion with choice %q err = %v, want ErrNoChoice", choice, err)
		}
		if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
			t.Errorf("refused AnswerQuestion with choice %q changed the store:\n got %v\nwant %v", choice, got, before)
		}
	}
}

func TestAnswerQuestionWithOtherAndBlankTextIsErrOtherNeedsTextAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	before := answerRowsOf(t, s)

	for _, text := range []string{"", " \t "} {
		_, err := s.AnswerQuestion(ctx, id, "D3", text, 1, ChoiceOther, claimedAt())
		if !errors.Is(err, ErrOtherNeedsText) {
			t.Fatalf("AnswerQuestion of other with text %q err = %v, want ErrOtherNeedsText", text, err)
		}
		if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
			t.Errorf("refused AnswerQuestion of other with text %q changed the store:\n got %v\nwant %v",
				text, got, before)
		}
	}
}

func TestAnswerQuestionOfUnknownQuestionIsErrNoQuestionAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	before := answerRowsOf(t, s)

	_, err := s.AnswerQuestion(ctx, id, "D3", "Answer", 5, ChoiceOther, claimedAt())
	if !errors.Is(err, ErrNoQuestion) {
		t.Fatalf("AnswerQuestion of unknown question err = %v, want ErrNoQuestion", err)
	}
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("refused AnswerQuestion changed the store:\n got %v\nwant %v", got, before)
	}
}

func TestAnswerQuestionOfClosedQuestionIsErrQuestionClosedAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	if _, err := s.AnswerQuestion(ctx, id, "D3", "Keep them", 1, ChoiceOther, claimedAt()); err != nil {
		t.Fatalf("first AnswerQuestion: %v", err)
	}
	before := answerRowsOf(t, s)

	if _, err := s.AnswerQuestion(ctx, id, "D4", "Other answer", 1, ChoiceOther, completedAt()); !errors.Is(
		err, ErrQuestionClosed,
	) {
		t.Fatalf("AnswerQuestion of closed question err = %v, want ErrQuestionClosed", err)
	}
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("refused AnswerQuestion changed the store:\n got %v\nwant %v", got, before)
	}
}

func TestAnswerQuestionOfReplacedQuestionIsQuestionReplacedErrorAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	claimLaterTurn(t, s, 2)
	replacing := shapedQuestion("keep-new-tasks", "Keep the new tasks?")
	replacing.Replaces = "Q1"
	completeLaterTurn(t, s, 2, replacing)
	before := answerRowsOf(t, s)

	_, err := s.AnswerQuestion(ctx, id, "D3", "Keep them", 1, "first", completedAt())
	var replaced *QuestionReplacedError
	if !errors.As(err, &replaced) {
		t.Fatalf("AnswerQuestion of replaced question err = %v, want a *QuestionReplacedError", err)
	}
	if want := (QuestionReplacedError{Seq: 1, By: 2}); *replaced != want {
		t.Errorf("QuestionReplacedError = %+v, want %+v", *replaced, want)
	}
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("refused AnswerQuestion changed the store:\n got %v\nwant %v", got, before)
	}
}

func TestAnswerQuestionWithStoredIDAndTextReturnsStoredRowAfterItsQuestionClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	stored, err := s.AnswerQuestion(ctx, id, "D3", "Keep them", 1, "first", claimedAt())
	if err != nil {
		t.Fatalf("first AnswerQuestion: %v", err)
	}
	before := answerRowsOf(t, s)

	got, err := s.AnswerQuestion(ctx, id, "D3", "Keep them", 1, "first", completedAt())
	if err != nil {
		t.Fatalf("repeated AnswerQuestion: %v", err)
	}
	if !reflect.DeepEqual(got, stored) {
		t.Errorf("repeated AnswerQuestion = %+v, want stored %+v", got, stored)
	}
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("repeated AnswerQuestion changed the store:\n got %v\nwant %v", got, before)
	}
}

func TestAnswerQuestionWithStoredIDAndOtherTextIsErrDecisionConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	questionedEpic(t, s,
		shapedQuestion("keep-old-tasks", "Keep the old tasks?"),
		shapedQuestion("which-store", "Which store?"),
	)
	if _, err := s.AnswerQuestion(ctx, id, "D3", "Keep them", 1, "first", claimedAt()); err != nil {
		t.Fatalf("first AnswerQuestion: %v", err)
	}
	before := answerRowsOf(t, s)

	_, err := s.AnswerQuestion(ctx, id, "D3", "Other answer", 2, "first", completedAt())
	if !errors.Is(err, ErrDecisionConflict) {
		t.Fatalf("AnswerQuestion with other text err = %v, want ErrDecisionConflict", err)
	}
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("refused AnswerQuestion changed the store:\n got %v\nwant %v", got, before)
	}
}

func TestAnswerQuestionWithStoredIDAndOtherChoiceIsErrDecisionConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	if _, err := s.AnswerQuestion(ctx, id, "D3", "Keep them", 1, "first", claimedAt()); err != nil {
		t.Fatalf("first AnswerQuestion: %v", err)
	}
	before := answerRowsOf(t, s)

	_, err := s.AnswerQuestion(ctx, id, "D3", "Keep them", 1, "second", completedAt())
	if !errors.Is(err, ErrDecisionConflict) {
		t.Fatalf("AnswerQuestion with another choice err = %v, want ErrDecisionConflict", err)
	}
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("refused AnswerQuestion changed the store:\n got %v\nwant %v", got, before)
	}
}

func TestAnswerQuestionWithIDOfStoredNoteIsErrDecisionConflict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	if _, err := s.AnswerEpic(ctx, id, "D3", "Keep them", claimedAt()); err != nil {
		t.Fatalf("AnswerEpic note: %v", err)
	}
	before := answerRowsOf(t, s)

	_, err := s.AnswerQuestion(ctx, id, "D3", "Keep them", 1, "first", completedAt())
	if !errors.Is(err, ErrDecisionConflict) {
		t.Fatalf("AnswerQuestion over a stored note err = %v, want ErrDecisionConflict", err)
	}
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("refused AnswerQuestion changed the store:\n got %v\nwant %v", got, before)
	}
}

func TestAnswerEpicNoteLeavesEveryQuestionOpenAndAddsNoTrigger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	questionedEpic(t, s,
		shapedQuestion("keep-old-tasks", "Keep the old tasks?"),
		shapedQuestion("which-store", "Which store?"),
	)
	triggersBefore := rowsOf(t, s.db.DB, "epic_triggers")

	at := time.Unix(1700000600, 0)
	got, err := s.AnswerEpic(ctx, id, "D3", "A note", at)
	if err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	if want := (EpicDecision{Seq: 3, ID: "D3", Text: "A note", Time: at}); !reflect.DeepEqual(got, want) {
		t.Errorf("AnswerEpic = %+v, want %+v", got, want)
	}

	epic, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	for _, question := range epic.Questions {
		if question.State != "open" {
			t.Errorf("question Q%d state = %q after a note, want open", question.Seq, question.State)
		}
	}
	if triggers := rowsOf(t, s.db.DB, "epic_triggers"); !reflect.DeepEqual(triggers, triggersBefore) {
		t.Errorf("epic_triggers after AnswerEpic = %v, want %v", triggers, triggersBefore)
	}
}

func TestOpenQuestionsListsOnlyOpenQuestionsInSeqOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	first := shapedQuestion("first", "First?")
	third := shapedQuestion("third", "Third?")
	questionedEpic(t, s, first, shapedQuestion("second", "Second?"), third)
	if _, err := s.AnswerQuestion(ctx, id, "D3", "Answer two", 2, ChoiceOther, claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}

	got, err := s.OpenQuestions(ctx, id)
	if err != nil {
		t.Fatalf("OpenQuestions: %v", err)
	}
	want := []EpicQuestion{openStored(first, 1, 1), openStored(third, 3, 1)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OpenQuestions = %+v, want %+v", got, want)
	}

	for _, answer := range []struct {
		id       string
		question int64
	}{{"D4", 1}, {"D5", 3}} {
		if _, err := s.AnswerQuestion(
			ctx, id, answer.id, "Answer", answer.question, ChoiceOther, claimedAt(),
		); err != nil {
			t.Fatalf("AnswerQuestion %s: %v", answer.id, err)
		}
	}
	if got, err := s.OpenQuestions(ctx, id); err != nil || got != nil {
		t.Errorf("OpenQuestions with none open = %+v, %v, want nil, nil", got, err)
	}
}

func TestAnswerQuestionOfUnknownEpicIsErrNoEpic(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	_, err := s.AnswerQuestion(
		context.Background(), "orrery-1a2b3c4/missing", "D1", "text", 1, ChoiceOther, epicCreated(),
	)
	if !errors.Is(err, ErrNoEpic) {
		t.Fatalf("AnswerQuestion of unknown epic err = %v, want ErrNoEpic", err)
	}
}
