package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// shapedQuestion returns a question that passes QuestionRefusals: the label and text given, two synthetic choices with
// the first recommended, a medium confidence and a synthetic reason.
func shapedQuestion(label, text string) EpicQuestionInput {
	return EpicQuestionInput{
		Label: label, Text: text,
		Choices: []EpicChoice{
			{ID: "first", Does: "Synthetic first choice."},
			{ID: "second", Does: "Synthetic second choice."},
		},
		Recommended: "first", Why: "Synthetic reason.", Confidence: "medium",
	}
}

// syntheticChoicesJSON is the stored choices of shapedQuestion.
const syntheticChoicesJSON = `[{"id":"first","does":"Synthetic first choice."},` +
	`{"id":"second","does":"Synthetic second choice."}]`

// openStored returns the open question a store reads back for question, stored at seq and asked by turn. It replaces
// no question.
func openStored(question EpicQuestionInput, seq, turn int64) EpicQuestion {
	return EpicQuestion{
		Seq: seq, Turn: turn, Text: question.Text, State: "open", Label: question.Label, Choices: question.Choices,
		Recommended: question.Recommended, Why: question.Why, Confidence: question.Confidence,
	}
}

// refusalRule is a rule of QuestionRefusals: mutate turns a shaped question into one that breaks only that rule, and
// want is the one line the rule returns for it.
type refusalRule struct {
	name   string
	mutate func(q *EpicQuestionInput)
	want   string
}

// refusalRules returns one case per rule of QuestionRefusals, in the rules' order.
func refusalRules() []refusalRule {
	return []refusalRule{
		{"label empty", func(q *EpicQuestionInput) { q.Label = "" }, "label: empty"},
		{"label not kebab-case", func(q *EpicQuestionInput) { q.Label = "Which Store" }, "label: not kebab-case"},
		{"label park", func(q *EpicQuestionInput) { q.Label = ParkLabel }, "label: park is reserved for orrery"},
		{"text empty", func(q *EpicQuestionInput) { q.Text = "" }, "text: empty"},
		{"one choice", func(q *EpicQuestionInput) { q.Choices = q.Choices[:1] }, "choices: fewer than two choices"},
		{"choice id empty", func(q *EpicQuestionInput) {
			q.Choices[0].ID, q.Recommended = "", "second"
		}, "choices[0].id: empty"},
		{"choice id other", func(q *EpicQuestionInput) {
			q.Choices[1].ID = ChoiceOther
		}, "choices[1].id: other is offered by orrery"},
		{"choice id named twice", func(q *EpicQuestionInput) {
			q.Choices[1].ID = "first"
		}, "choices[1].id: named twice"},
		{"choice does empty", func(q *EpicQuestionInput) { q.Choices[0].Does = "" }, "choices[0].does: empty"},
		{"recommended names no choice", func(q *EpicQuestionInput) {
			q.Recommended = "missing"
		}, "recommended: names no declared choice"},
		{"recommended empty", func(q *EpicQuestionInput) {
			q.Recommended = ""
		}, "recommended: names no declared choice"},
		{"why empty", func(q *EpicQuestionInput) { q.Why = "" }, "why: empty"},
		{"confidence not low, medium or high", func(q *EpicQuestionInput) {
			q.Confidence = "certain"
		}, "confidence: not low, medium or high"},
		{"replaces not Q<n>", func(q *EpicQuestionInput) {
			q.Replaces = "1"
		}, "replaces: not empty or Q<n> with n >= 1"},
		{"replaces Q0", func(q *EpicQuestionInput) {
			q.Replaces = "Q0"
		}, "replaces: not empty or Q<n> with n >= 1"},
	}
}

func TestQuestionRefusalsNamesTheRuleEachBrokenQuestionBreaks(t *testing.T) {
	t.Parallel()
	if got := QuestionRefusals(shapedQuestion("which-store", "Which store?")); len(got) != 0 {
		t.Errorf("QuestionRefusals of a shaped question = %q, want none", got)
	}
	for _, rule := range refusalRules() {
		t.Run(rule.name, func(t *testing.T) {
			t.Parallel()
			question := shapedQuestion("which-store", "Which store?")
			rule.mutate(&question)
			if got := QuestionRefusals(question); !reflect.DeepEqual(got, []string{rule.want}) {
				t.Errorf("QuestionRefusals = %q, want [%q]", got, rule.want)
			}
		})
	}
}

func TestCompleteTurnRefusesEachBrokenQuestionAndWritesNothing(t *testing.T) {
	t.Parallel()
	for _, rule := range refusalRules() {
		t.Run(rule.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			const id = claimEpic
			createEpic(t, s, fullEpic(id))
			addPendingTrigger(t, s)
			if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
				t.Fatalf("ClaimTurn: %v", err)
			}
			question := shapedQuestion("which-store", "Which store?")
			rule.mutate(&question)
			plan := plannedTurn()
			plan.Questions = []EpicQuestionInput{question}
			before := planRowsOf(t, s)

			err := s.CompleteTurn(ctx, id, 1, plan, completedAt())
			want := fmt.Sprintf("%v: question 1 %q: %s", ErrBadQuestion, question.Label, rule.want)
			if err == nil || err.Error() != want {
				t.Fatalf("CompleteTurn = %v, want %q", err, want)
			}
			if !errors.Is(err, ErrBadQuestion) {
				t.Errorf("CompleteTurn error does not match ErrBadQuestion: %v", err)
			}
			if after := planRowsOf(t, s); !reflect.DeepEqual(after, before) {
				t.Errorf("rows after a refused CompleteTurn = %v, want the rows before %v", after, before)
			}
		})
	}
}

func TestCompleteTurnStoresShapedQuestionsAndReadsThemBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("first ClaimTurn: %v", err)
	}
	first := EpicQuestionInput{
		Label: "which-store", Text: "Which store holds the plan?",
		Choices: []EpicChoice{
			{ID: "sqlite", Does: "Keep the plan in the epic tables."},
			{ID: "files", Does: "Write the plan to a file."},
		},
		Recommended: "sqlite", Why: "The tables already hold the epic.", Confidence: "high",
	}
	if err := s.CompleteTurn(ctx, id, 1, EpicPlan{Questions: []EpicQuestionInput{first}, Outcome: "wait"},
		completedAt()); err != nil {
		t.Fatalf("first CompleteTurn: %v", err)
	}

	insertPendingTrigger(t, s, id, "t3", "later trigger")
	if _, err := s.ClaimTurn(ctx, id, claimedAt().Add(time.Minute)); err != nil {
		t.Fatalf("second ClaimTurn: %v", err)
	}
	second := EpicQuestionInput{
		Label: "tick-rate", Text: "How often does the runner tick?",
		Choices: []EpicChoice{
			{ID: "fast", Does: "Tick every second."},
			{ID: "slow", Does: "Tick every minute."},
			{ID: "idle", Does: "Tick only on triggers."},
		},
		Recommended: "idle", Why: "Triggers already wake the runner.", Confidence: "low", Replaces: "Q1",
	}
	if err := s.CompleteTurn(ctx, id, 2, EpicPlan{Questions: []EpicQuestionInput{second}, Outcome: "wait"},
		completedAt().Add(time.Minute)); err != nil {
		t.Fatalf("second CompleteTurn: %v", err)
	}

	want := []EpicQuestion{
		{
			Seq: 1, Turn: 1, Text: first.Text, State: "open", Label: first.Label, Choices: first.Choices,
			Recommended: first.Recommended, Why: first.Why, Confidence: first.Confidence,
		},
		{
			Seq: 2, Turn: 2, Text: second.Text, State: "open", Label: second.Label, Choices: second.Choices,
			Recommended: second.Recommended, Why: second.Why, Confidence: second.Confidence, Replaces: 1,
		},
	}
	epic, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if !reflect.DeepEqual(epic.Questions, want) {
		t.Errorf("Epic questions = %+v, want %+v", epic.Questions, want)
	}
	open, err := s.OpenQuestions(ctx, id)
	if err != nil {
		t.Fatalf("OpenQuestions: %v", err)
	}
	if !reflect.DeepEqual(open, want) {
		t.Errorf("OpenQuestions = %+v, want %+v", open, want)
	}
}
