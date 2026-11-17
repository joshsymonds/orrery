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
		{"label with underscores", func(q *EpicQuestionInput) { q.Label = "Which_Epic" }, "label: not kebab-case"},
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
		{"recommended other", func(q *EpicQuestionInput) {
			q.Recommended = ChoiceOther
		}, "recommended: names no declared choice"},
		{"why empty", func(q *EpicQuestionInput) { q.Why = "" }, "why: empty"},
		{"confidence not low, medium or high", func(q *EpicQuestionInput) {
			q.Confidence = "certain"
		}, "confidence: not low, medium or high"},
		{"confidence empty", func(q *EpicQuestionInput) {
			q.Confidence = ""
		}, "confidence: not low, medium or high"},
		{"confidence capitalized", func(q *EpicQuestionInput) {
			q.Confidence = "High"
		}, "confidence: not low, medium or high"},
		{"replaces not Q<n>", func(q *EpicQuestionInput) {
			q.Replaces = "1"
		}, "replaces: not empty or Q<n> with n >= 1"},
		{"replaces Q0", func(q *EpicQuestionInput) {
			q.Replaces = "Q0"
		}, "replaces: not empty or Q<n> with n >= 1"},
		{"replaces leading zero", func(q *EpicQuestionInput) {
			q.Replaces = "Q01"
		}, "replaces: not empty or Q<n> with n >= 1"},
		{"replaces lower case", func(q *EpicQuestionInput) {
			q.Replaces = "q1"
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

// parkQuestionOf returns the open park question a store reads back for a park with text at seq that is not final: one
// declared choice, retry, recommended with a low confidence.
func parkQuestionOf(seq int64, text string) EpicQuestion {
	return EpicQuestion{
		Seq: seq, Turn: 0, Text: text, State: QuestionOpen, Label: ParkLabel,
		Choices: []EpicChoice{{
			ID:   "retry",
			Does: "end the park and start a turn on the waiting triggers, as orrery epic retry does",
		}},
		Recommended: "retry",
		Why:         "no model judges a park; retry runs the epic again on its waiting triggers",
		Confidence:  "low",
	}
}

// finalParkQuestionOf returns the open park question a store reads back for a final park with text at seq: no
// declared choice and no recommendation.
func finalParkQuestionOf(seq int64, text string) EpicQuestion {
	return EpicQuestion{Seq: seq, Turn: 0, Text: text, State: QuestionOpen, Label: ParkLabel, Choices: []EpicChoice{}}
}

// questionsOf returns the questions of claimEpic a store reads back.
func questionsOf(t *testing.T, s *Store) []EpicQuestion {
	t.Helper()
	epic, err := s.Epic(context.Background(), claimEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	return epic.Questions
}

func TestThreeFailedTurnsInARowOpenAParkQuestion(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	addPendingTrigger(t, s)
	claimAndFail(t, s, "first failure")
	claimAndFail(t, s, "second failure")
	claimAndFail(t, s, "third failure")

	want := []EpicQuestion{parkQuestionOf(1, parkPrefix+"third failure")}
	if got := questionsOf(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("questions after the third failure = %+v, want %+v", got, want)
	}
}

func TestFreeTurnWithParkOpensAParkQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	turn, err := s.ClaimTurn(ctx, claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.FreeTurn(ctx, claimEpic, turn.Seq, TurnFree{Reason: "stopped", Park: "needs a human"},
		failedAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}

	want := []EpicQuestion{parkQuestionOf(1, "needs a human")}
	if got := questionsOf(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("questions after a free park = %+v, want %+v", got, want)
	}
}

func TestParkEpicOpensAParkQuestionAndItsTellCarriesItsSeq(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	if err := s.ParkEpic(ctx, claimEpic, ParkClassBranch, "credit ran out", completedAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}

	want := []EpicQuestion{parkQuestionOf(1, "credit ran out")}
	if got := questionsOf(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("questions after ParkEpic = %+v, want %+v", got, want)
	}
	tells, err := sqlcgen.New(s.db).ListEpicTells(ctx, claimEpic)
	if err != nil {
		t.Fatalf("ListEpicTells: %v", err)
	}
	if len(tells) != 1 || tells[0].Text != "credit ran out" || tells[0].Question != 1 {
		t.Errorf("tells after ParkEpic = %+v, want one tell %q carrying question 1", tells, "credit ran out")
	}
}

func TestParkAgainReplacesTheOpenParkQuestion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	if err := s.ParkEpic(ctx, claimEpic, ParkClassBranch, "credit ran out", completedAt()); err != nil {
		t.Fatalf("first ParkEpic: %v", err)
	}
	if err := s.ParkEpic(ctx, claimEpic, ParkClassBranch, "needs a human", completedAt().Add(time.Minute)); err != nil {
		t.Fatalf("second ParkEpic: %v", err)
	}

	replaced := parkQuestionOf(1, "credit ran out")
	replaced.State, replaced.ReplacedBy = QuestionReplaced, 2
	successor := parkQuestionOf(2, "needs a human")
	successor.Replaces = 1
	if got, want := questionsOf(t, s), []EpicQuestion{replaced, successor}; !reflect.DeepEqual(got, want) {
		t.Errorf("questions after parking again = %+v, want %+v", got, want)
	}
	open, err := s.OpenQuestions(ctx, claimEpic)
	if err != nil {
		t.Fatalf("OpenQuestions: %v", err)
	}
	if want := []EpicQuestion{successor}; !reflect.DeepEqual(open, want) {
		t.Errorf("OpenQuestions after parking again = %+v, want %+v", open, want)
	}
}

// TestParkNodeMetAtBaseOpensThreeChoiceQuestionOnlyWhenItParks checks that ParkNodeMetAtBase opens its park question
// only when it parks a run. R4: the park used to open a final question that declared no choice, with the phase set to
// met_at_base. It now opens the question of the three choices and leaves the phase as it was.
func TestParkNodeMetAtBaseOpensThreeChoiceQuestionOnlyWhenItParks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	runs := []NodeRun{{Path: "red", ID: "id-red", Node: "red", Kind: "evidence", Attempt: 1}}
	if err := s.StartNodes(ctx, claimEpic, runs, nil, claimedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}

	if err := s.ParkNodeMetAtBase(ctx, claimEpic, "red", 2, "failed", "wrong attempt", metAtBaseParkReason,
		[]string{"R1"}, completedAt()); err != nil {
		t.Fatalf("ParkNodeMetAtBase at another attempt: %v", err)
	}
	if got := questionsOf(t, s); len(got) != 0 {
		t.Errorf("questions after a ParkNodeMetAtBase that parks nothing = %+v, want none", got)
	}

	if err := s.ParkNodeMetAtBase(ctx, claimEpic, "red", 1, "failed", "R1 red: failed", metAtBaseParkReason,
		[]string{"R1"}, completedAt()); err != nil {
		t.Fatalf("ParkNodeMetAtBase: %v", err)
	}
	want := []EpicQuestion{metAtBaseQuestionR1()}
	if got := questionsOf(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("questions after ParkNodeMetAtBase = %+v, want %+v", got, want)
	}
	head, err := sqlcgen.New(s.db).GetEpic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("GetEpic: %v", err)
	}
	if head.Phase != "accepted" {
		t.Errorf("phase after ParkNodeMetAtBase = %q, want accepted", head.Phase)
	}
}
