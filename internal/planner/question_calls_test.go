package planner

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// callsReturn returns a planner return with outcome and calls, the question_calls string it carries, and nothing else.
func callsReturn(outcome, calls string) json.RawMessage {
	encoded, err := json.Marshal(calls)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(`{"status":"done","decisions":[],"tasks":[],"outline":"o","questions":[],"actions":[],` +
		`"question_calls":` + string(encoded) + `,"outcome":"` + outcome + `"}`)
}

// TestParseReadsTheQuestionCallsFromTheirString checks that Parse reads question_calls, a JSON array written as a
// string, into the return's calls, reads a blank string as none, keeps why a string that is not such an array did not
// read for the check to refuse, and refuses a question_calls that is not a string.
func TestParseReadsTheQuestionCallsFromTheirString(t *testing.T) {
	t.Parallel()
	ret, err := Parse(callsReturn("wait", `[{"question":"Q1","call":"stands","reason":"r"}]`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := []QuestionCall{{Question: "Q1", Call: "stands", Reason: "r"}}; !reflect.DeepEqual(
		ret.QuestionCalls, want,
	) || ret.QuestionCallsError != "" {
		t.Errorf("calls = %+v (error %q), want %+v", ret.QuestionCalls, ret.QuestionCallsError, want)
	}
	for _, text := range []string{"", "  "} {
		ret, err = Parse(callsReturn("wait", text))
		if err != nil || ret.QuestionCalls != nil || ret.QuestionCallsError != "" {
			t.Errorf("Parse of question_calls %q = %+v, %v; want no calls", text, ret.QuestionCalls, err)
		}
	}
	for _, text := range []string{`null`, `{"question":"Q1"}`, `[{"question":"Q1","when":"now"}]`, `[] []`} {
		ret, err = Parse(callsReturn("wait", text))
		if err != nil || ret.QuestionCalls != nil || ret.QuestionCallsError == "" {
			t.Errorf("Parse of question_calls %q = %+v (error %q), %v; want the reason it did not read",
				text, ret.QuestionCalls, ret.QuestionCallsError, err)
		}
	}
	raw := json.RawMessage(`{"status":"done","decisions":[],"tasks":[],"outline":"o","questions":[],"actions":[],` +
		`"question_calls":[],"outcome":"wait"}`)
	if _, err = Parse(raw); err == nil || !strings.Contains(err.Error(), "question_calls") {
		t.Errorf("Parse of a question_calls array = %v, want an error naming question_calls", err)
	}
}

// TestPlanOfPassesTheQuestionCallsThrough checks that the plan a return stores carries its calls as given.
func TestPlanOfPassesTheQuestionCallsThrough(t *testing.T) {
	t.Parallel()
	ret := Return{QuestionCalls: []QuestionCall{
		{Question: "Q1", Call: store.CallOvertaken, Reason: "got past it"},
		{Question: "Q4", Call: store.CallStands, Reason: "still waits"},
	}}
	want := []store.QuestionCallInput{
		{Question: "Q1", Call: store.CallOvertaken, Reason: "got past it"},
		{Question: "Q4", Call: store.CallStands, Reason: "still waits"},
	}
	if got := planOf(ret).QuestionCalls; !reflect.DeepEqual(got, want) {
		t.Errorf("planOf calls = %+v, want %+v", got, want)
	}
}

// callsQuestions are the questions the call check reads: Q1 and Q4 open and asked by planner turns, so each takes a
// call; Q2 an open park question orrery asked; Q3 a planner question already answered.
func callsQuestions() []store.EpicQuestion {
	return []store.EpicQuestion{
		{Seq: 1, Turn: 1, State: store.QuestionOpen, Label: "keep-old-tasks"},
		{Seq: 2, Turn: 0, State: store.QuestionOpen, Label: store.ParkLabel},
		{Seq: 3, Turn: 2, State: store.QuestionAnswered, Label: "cache-ttl"},
		{Seq: 4, Turn: 2, State: store.QuestionOpen, Label: "which-store"},
	}
}

// TestCheckQuestionCallsRefusesEachBadCall checks each line of checkQuestionCalls against callsQuestions: a return that
// calls Q1 and Q4, or replaces one of them with a new question, passes, beside outcome complete too when no call
// stands; every other return gets the lines named here, in entry order, then one line per question left with no call.
func TestCheckQuestionCallsRefusesEachBadCall(t *testing.T) {
	t.Parallel()
	call := func(question, kind, reason string) QuestionCall {
		return QuestionCall{Question: question, Call: kind, Reason: reason}
	}
	over := func(question string) QuestionCall { return call(question, store.CallOvertaken, "got past it") }
	stands := func(question string) QuestionCall { return call(question, store.CallStands, "still waits") }
	missingQ4 := "question_calls: Q4 " + reasonNoCall
	cases := []struct {
		name string
		ret  Return
		want []string
	}{
		{"each open planner question called", Return{
			Outcome: "wait", QuestionCalls: []QuestionCall{over("Q1"), stands("Q4")},
		}, nil},
		{"a replaced question needs no call", Return{
			Outcome: "wait", QuestionCalls: []QuestionCall{over("Q1")}, Questions: []Question{{Replaces: "Q4"}},
		}, nil},
		{"complete with every call overtaken", Return{
			Outcome: store.PlanOutcomeComplete, QuestionCalls: []QuestionCall{over("Q1"), over("Q4")},
		}, nil},
		{"a question with no call", Return{Outcome: "wait", QuestionCalls: []QuestionCall{over("Q1")}},
			[]string{missingQ4}},
		{"no calls at all", Return{Outcome: "wait"},
			[]string{"question_calls: Q1 " + reasonNoCall, missingQ4}},
		{"not Q<n>", Return{Outcome: "wait", QuestionCalls: []QuestionCall{over("Q1"), over("4")}},
			[]string{"question_calls[1] question: not Q<n>", missingQ4}},
		{
			"no such question",
			Return{Outcome: "wait", QuestionCalls: []QuestionCall{over("Q1"), stands("Q4"), over("Q9")}},
			[]string{"question_calls[2] question: Q9 names no question of the epic"},
		},
		{"a question orrery asked", Return{
			Outcome: "wait", QuestionCalls: []QuestionCall{over("Q1"), stands("Q4"), stands("Q2")},
		}, []string{"question_calls[2] question: Q2 " + reasonNotPlanner}},
		{"a question no longer open", Return{
			Outcome: "wait", QuestionCalls: []QuestionCall{over("Q1"), stands("Q4"), over("Q3")},
		}, []string{"question_calls[2] question: Q3 is answered, not open"}},
		{"a question called twice", Return{
			Outcome: "wait", QuestionCalls: []QuestionCall{over("Q1"), stands("Q1"), stands("Q4")},
		}, []string{"question_calls[1] question: Q1 " + reasonCalledEarlier}},
		{"a call on a question the return replaces", Return{
			Outcome: "wait", QuestionCalls: []QuestionCall{over("Q1"), stands("Q4")},
			Questions: []Question{{Replaces: "Q4"}},
		}, []string{"question_calls[1] question: Q4 is replaced by questions[0] of this return, which is its call"}},
		{"a call that is neither", Return{
			Outcome: "wait", QuestionCalls: []QuestionCall{call("Q1", "maybe", "r"), stands("Q4")},
		}, []string{"question_calls[0] call: " + reasonNotCall}},
		{"a blank reason", Return{
			Outcome: "wait", QuestionCalls: []QuestionCall{call("Q1", store.CallOvertaken, " "), stands("Q4")},
		}, []string{"question_calls[0] reason: " + reasonEmpty}},
		{"a stands call beside complete", Return{
			Outcome: store.PlanOutcomeComplete, QuestionCalls: []QuestionCall{over("Q1"), stands("Q4")},
		}, []string{"question_calls[1] call: " + reasonStandsAtEnd}},
		{"calls that do not read", Return{Outcome: "wait", QuestionCallsError: "decode: not a JSON array"},
			[]string{"question_calls: " + reasonCallsNotArray}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := checkQuestionCalls(c.ret, callsQuestions()); !reflect.DeepEqual(got, c.want) {
				t.Errorf("checkQuestionCalls = %q, want %q", got, c.want)
			}
		})
	}
}

// TestReturnCheckSendsBackAReturnThatForgetsAnOpenQuestion checks that the planner step's return check runs the call
// check against the epic's questions: a return that leaves the open planner question Q1 uncalled is sent back with the
// line that names it, and the same return calling Q1 passes.
func TestReturnCheckSendsBackAReturnThatForgetsAnOpenQuestion(t *testing.T) {
	t.Parallel()
	epic := store.EpicRow{Phase: "accepted", Questions: []store.EpicQuestion{
		{Seq: 1, Turn: 1, State: store.QuestionOpen, Label: "keep-old-tasks"},
	}}
	check := returnCheck(context.Background(), epic, store.Review{}, testResolver{}, nil, false)
	if got, want := check(
		callsReturn("complete", ""),
	), []string{
		"question_calls: Q1 " + reasonNoCall,
	}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Errorf("returnCheck of a return with no call = %q, want %q", got, want)
	}
	called := `[{"question":"Q1","call":"overtaken","reason":"got past it"}]`
	if got := check(callsReturn("complete", called)); got != nil {
		t.Errorf("returnCheck of a return that calls Q1 = %q, want no refusal", got)
	}
}

// TestOpenQuestionsShowTheLastCallOfEachQuestionAPlannerTurnAsked checks that the Open questions lines give each open
// question a planner turn asked its last call, the newest of its calls, and none for a question orrery or an
// orchestrator asked, which takes no call.
func TestOpenQuestionsShowTheLastCallOfEachQuestionAPlannerTurnAsked(t *testing.T) {
	t.Parallel()
	questions := []store.EpicQuestion{
		{Seq: 1, Turn: 1, State: store.QuestionOpen, Label: "keep-old-tasks", Text: "Keep the old tasks?",
			Calls: []store.QuestionCall{
				{Turn: 2, Call: store.CallStands, Reason: "the layout waits on it"},
				{Turn: 3, Call: store.CallStands, Reason: "nothing changed since turn 2"},
			}},
		{Seq: 2, Turn: 0, State: store.QuestionOpen, Label: "capture-broken", Text: "T1: the capture exits 2"},
	}
	want := []string{
		"- Q1 (turn 1, keep-old-tasks): Keep the old tasks?",
		"  last call: stands at turn 3: nothing changed since turn 2",
		"- Q2 (turn 0, capture-broken): T1: the capture exits 2",
	}
	if got := questionLines(questions); !reflect.DeepEqual(got, want) {
		t.Errorf("questionLines = %q, want %q", got, want)
	}
}

// TestRoleTellsThePlannerToCallEachOpenQuestionEachTurn checks that the role text names question_calls, says which
// questions take a call and what each call does, and says a complete return calls no question stands.
func TestRoleTellsThePlannerToCallEachOpenQuestionEachTurn(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		`question_calls: a JSON array written as a string, like args, of your calls on the questions still open; "" ` +
			`when none is open.`,
		"Each turn, call every open question a planner turn asked",
		"call, stands when the question still waits on its answer, or overtaken when the epic has gone on without it",
		"A question this return replaces with a new one needs no call.",
		"An overtaken call closes the question in this turn's write",
		"A question answered while this turn ran keeps its answer, whatever you call it",
		"a return whose outcome is complete calls no question stands: call it overtaken, or return wait.",
		"wait only when this turn asks a catastrophe question or a question stands",
	} {
		if !strings.Contains(role, want) {
			t.Errorf("role text lacks %q", want)
		}
	}
}

// TestRunnerTurnThatGoesOnWithoutAnAnswerCallsItsQuestionOvertaken is the incident through the runner: turn 1 asks Q1,
// and turn 2, woken by a kick, got past the problem by other means. Turn 2's prompt shows Q1 with no call yet, its return
// check sends back a return that forgets Q1, and the return that calls Q1 overtaken beside outcome complete completes
// the turn: Q1 is overtaken with its decision, and an answer to Q1 is refused.
func TestRunnerTurnThatGoesOnWithoutAnAnswerCallsItsQuestionOvertaken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunnerStore(t)
	createRunnerEpic(t, s)
	steps := newFakeSteps()
	steps.watchStore(s)
	var reads atomic.Int64
	runner := runRunner(t, runnerConfig(s, steps, agentReturning(runnerAgent, nil, &reads)))
	waitFor(t, &steps.changeNotice, "a planner step to start", func() bool { return len(steps.started()) == 1 })
	steps.end("step-1", store.StepRow{End: "done", Result: validPlan})
	waitFor(t, &steps.changeNotice, "turn 1 to succeed",
		func() bool { return epicOf(t, s).Turns[0].State == "succeeded" })
	if err := s.KickEpic(
		ctx,
		runnerEpicID,
		"the store moved",
		store.Maker{By: store.MakerOwner},
		claimedAt(),
	); err != nil {
		t.Fatalf("KickEpic: %v", err)
	}
	runner.Pass(t.Context())
	waitFor(t, &steps.changeNotice, "a second planner step to start", func() bool { return len(steps.started()) == 2 })
	waitFor(t, &steps.changeNotice, "step-2 recorded on turn 2", func() bool {
		turns := epicOf(t, s).Turns
		return len(turns) == 2 && turns[1].Step == "step-2"
	})

	start := steps.started()[1]
	if want := "  last call: none\n"; !strings.Contains(start.Prompt, want) {
		t.Errorf("prompt of turn 2 lacks %q; prompt is:\n%s", want, start.Prompt)
	}
	if got, want := start.ReturnCheck(
		callsReturn("complete", ""),
	), []string{
		"question_calls: Q1 " + reasonNoCall,
	}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Errorf("return check of a return that forgets Q1 = %q, want %q", got, want)
	}
	called := callsReturn("complete", `[{"question":"Q1","call":"overtaken","reason":"the store moved on its own"}]`)
	if got := start.ReturnCheck(called); got != nil {
		t.Fatalf("return check of the return that calls Q1 = %q, want no refusal", got)
	}
	steps.end("step-2", store.StepRow{End: "done", Result: string(called)})
	waitFor(t, &steps.changeNotice, "turn 2 to succeed",
		func() bool { return epicOf(t, s).Turns[1].State == "succeeded" })

	epic := epicOf(t, s)
	if got := epic.Questions[0]; got.State != store.QuestionOvertaken || !reflect.DeepEqual(got.Calls,
		[]store.QuestionCall{{Turn: 2, Call: store.CallOvertaken, Reason: "the store moved on its own"}}) {
		t.Errorf("Q1 after turn 2 = %+v, want overtaken with turn 2's call", got)
	}
	last := epic.Decisions[len(epic.Decisions)-1]
	if want := "Q1 which-store closed as overtaken: turn 2 went on without its answer: the store moved on its own."; last.ID !=
		"overtaken:Q1" ||
		last.Text != want {
		t.Errorf("last decision = %+v, want overtaken:Q1 saying %q", last, want)
	}
	_, err := s.AnswerQuestion(ctx, runnerEpicID, "answer-1", "use sqlite", 1, "sqlite", store.Maker{}, claimedAt())
	var refused *store.QuestionOvertakenError
	if !errors.As(err, &refused) {
		t.Errorf("AnswerQuestion of Q1 = %v, want a *store.QuestionOvertakenError", err)
	}
}
