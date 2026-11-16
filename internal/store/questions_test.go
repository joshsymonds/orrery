package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
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

	firstStored := openStored(first, 1, 1)
	firstStored.State, firstStored.ReplacedBy = QuestionReplaced, 2
	secondStored := openStored(second, 2, 2)
	secondStored.Replaces = 1
	want := []EpicQuestion{firstStored, secondStored}
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
	if !reflect.DeepEqual(open, want[1:]) {
		t.Errorf("OpenQuestions = %+v, want %+v", open, want[1:])
	}
}

// claimLaterTurn stores a pending trigger for turn and claims it on claimEpic, as a later planner trigger would.
func claimLaterTurn(t *testing.T, s *Store, turn int64) {
	t.Helper()
	insertPendingTrigger(t, s, claimEpic, fmt.Sprintf("turn-%d", turn), "later trigger")
	at := claimedAt().Add(time.Duration(turn) * time.Minute)
	if _, err := s.ClaimTurn(context.Background(), claimEpic, at); err != nil {
		t.Fatalf("ClaimTurn of turn %d: %v", turn, err)
	}
}

// completeLaterTurn completes the running turn on claimEpic with a plan of the questions given.
func completeLaterTurn(t *testing.T, s *Store, turn int64, questions ...EpicQuestionInput) {
	t.Helper()
	plan := EpicPlan{Questions: questions, Outcome: "wait"}
	if err := s.CompleteTurn(context.Background(), claimEpic, turn, plan,
		completedAt().Add(time.Duration(turn)*time.Minute)); err != nil {
		t.Fatalf("CompleteTurn of turn %d: %v", turn, err)
	}
}

// replacing returns a shaped question with label and text that replaces the question replaces, a Q<n>.
func replacing(label, text, replaces string) EpicQuestionInput {
	question := shapedQuestion(label, text)
	question.Replaces = replaces
	return question
}

func TestCompleteTurnReplacingAnsweredQuestionClosesNothingAndOpensTheNewOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	questionedEpic(t, s, shapedQuestion("which-store", "Which store?"))
	if err := sqlcgen.New(s.db).SetEpicQuestionAnswered(ctx, sqlcgen.SetEpicQuestionAnsweredParams{
		Choice: ChoiceOther, Epic: id, Seq: 1,
	}); err != nil {
		t.Fatalf("answer question 1: %v", err)
	}
	before, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic before: %v", err)
	}
	claimLaterTurn(t, s, 2)
	second := replacing("which-cache", "Which cache?", "Q1")
	completeLaterTurn(t, s, 2, second)

	epic, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if answered := epic.Questions[0]; !reflect.DeepEqual(answered, before.Questions[0]) {
		t.Errorf("answered question after replacing it = %+v, want unchanged %+v", answered, before.Questions[0])
	}
	if got := epic.Questions[0].State; got != QuestionAnswered {
		t.Errorf("answered question state = %q, want %q", got, QuestionAnswered)
	}
	secondStored := openStored(second, 2, 2)
	secondStored.Replaces = 1
	if got := epic.Questions[1]; !reflect.DeepEqual(got, secondStored) {
		t.Errorf("new question = %+v, want %+v", got, secondStored)
	}
	open, err := s.OpenQuestions(ctx, id)
	if err != nil {
		t.Fatalf("OpenQuestions: %v", err)
	}
	if !reflect.DeepEqual(open, []EpicQuestion{secondStored}) {
		t.Errorf("OpenQuestions = %+v, want only the new question %+v", open, secondStored)
	}
}

func TestCompleteTurnReplacingReplacedQuestionClosesNothingAndOpensTheNewOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	questionedEpic(t, s, shapedQuestion("which-store", "Which store?"))
	claimLaterTurn(t, s, 2)
	completeLaterTurn(t, s, 2, replacing("which-cache", "Which cache?", "Q1"))
	before, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic before: %v", err)
	}
	claimLaterTurn(t, s, 3)
	third := replacing("which-index", "Which index?", "Q1")
	completeLaterTurn(t, s, 3, third)

	epic, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if replaced := epic.Questions[0]; !reflect.DeepEqual(replaced, before.Questions[0]) {
		t.Errorf("replaced question after replacing it again = %+v, want unchanged %+v", replaced, before.Questions[0])
	}
	if got := epic.Questions[0]; got.State != QuestionReplaced || got.ReplacedBy != 2 {
		t.Errorf("replaced question = state %q replaced by %d, want %q by 2",
			got.State, got.ReplacedBy, QuestionReplaced)
	}
	thirdStored := openStored(third, 3, 3)
	thirdStored.Replaces = 1
	if got := epic.Questions[2]; !reflect.DeepEqual(got, thirdStored) {
		t.Errorf("new question = %+v, want %+v", got, thirdStored)
	}
	open, err := s.OpenQuestions(ctx, id)
	if err != nil {
		t.Fatalf("OpenQuestions: %v", err)
	}
	if want := []EpicQuestion{epic.Questions[1], thirdStored}; !reflect.DeepEqual(open, want) {
		t.Errorf("OpenQuestions = %+v, want %+v", open, want)
	}
}

func TestCompleteTurnReplacingUnknownQuestionIsBadQuestionAndWritesNothing(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	questionedEpic(t, s, shapedQuestion("which-store", "Which store?"))
	claimLaterTurn(t, s, 2)
	before := planRowsOf(t, s)

	bad := replacing("which-cache", "Which cache?", "Q9")
	err := s.CompleteTurn(context.Background(), claimEpic, 2,
		EpicPlan{Questions: []EpicQuestionInput{bad}, Outcome: "wait"}, completedAt().Add(2*time.Minute))
	if !errors.Is(err, ErrBadQuestion) {
		t.Fatalf("CompleteTurn replacing Q9 = %v, want ErrBadQuestion", err)
	}
	if !strings.Contains(err.Error(), `"which-cache"`) || !strings.Contains(err.Error(), "Q9") {
		t.Errorf("CompleteTurn error %q does not name the question and Q9", err)
	}
	if after := planRowsOf(t, s); !reflect.DeepEqual(after, before) {
		t.Errorf("rows after a refused CompleteTurn = %v, want the rows before %v", after, before)
	}
}
