package store

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// callsAt is the second the question call tests complete their calling turn at.
func callsAt() time.Time {
	return time.Unix(1700004000, 0)
}

// callsGotPast is the reason the question call tests give for calling Q1 overtaken.
const callsGotPast = "the old tasks were kept by moving them under the new store."

// claimSecondTurn adds a pending trigger to claimEpic, whose turn 1 asked its questions, and claims turn 2 on it.
func claimSecondTurn(t *testing.T, s *Store) {
	t.Helper()
	addPendingTrigger(t, s)
	turn, err := s.ClaimTurn(context.Background(), claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if turn.Seq != 2 {
		t.Fatalf("claimed turn %d, want turn 2", turn.Seq)
	}
}

// callsPlan returns a plan with outcome and calls and nothing else.
func callsPlan(outcome string, calls ...QuestionCallInput) EpicPlan {
	return EpicPlan{Outline: "Go on with the store.", Outcome: outcome, QuestionCalls: calls}
}

// questionCallRows returns every row of epic_question_calls of claimEpic, as (question, turn, call, reason).
func questionCallRows(t *testing.T, s *Store) [][]any {
	t.Helper()
	rows, err := s.fixtureQueries().ListEpicQuestionCalls(context.Background(), claimEpic)
	if err != nil {
		t.Fatalf("ListEpicQuestionCalls: %v", err)
	}
	out := make([][]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, []any{row.Question, row.Turn, row.Call, row.Reason})
	}
	return out
}

// TestATurnThatCallsItsQuestionOvertakenClosesItAndRefusesItsAnswer is the incident: a planner asked Q1 at turn 1 and
// got past the problem by other means. Turn 2 calls Q1 overtaken beside outcome complete, and in the turn's write Q1
// becomes overtaken with the call stored, orreryd appends overtaken:Q1 naming the turn and its reason, and a tell its
// owner and steerer are told says Q1 takes no answer. A later answer to Q1 is refused and writes nothing, and the release
// that follows finds no question left to overtake.
func TestATurnThatCallsItsQuestionOvertakenClosesItAndRefusesItsAnswer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	questionedEpic(t, s, overtakenKeepOld())
	claimSecondTurn(t, s)
	decisions := len(storedEpicRow(t, s).Decisions)

	plan := callsPlan(PlanOutcomeComplete, QuestionCallInput{Question: "Q1", Call: CallOvertaken, Reason: callsGotPast})
	if err := s.CompleteTurn(ctx, claimEpic, 2, plan, callsAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}

	overtaken := overtakenKeepOldQ1()
	overtaken.Calls = []QuestionCall{{Turn: 2, Call: CallOvertaken, Reason: callsGotPast}}
	if got, want := questionsOf(t, s), []EpicQuestion{overtaken}; !reflect.DeepEqual(got, want) {
		t.Errorf("questions after the call = %+v, want %+v", got, want)
	}
	const why = "turn 2 went on without its answer: the old tasks were kept by moving them under the new store"
	wantDecision := EpicDecision{
		Seq: int64(decisions + 1), ID: "overtaken:Q1", Text: "Q1 keep-old-tasks closed as overtaken: " + why + ".",
		Time: callsAt(), Maker: Maker{By: MakerOrreryd},
	}
	if got := lastDecision(t, s); !reflect.DeepEqual(got, wantDecision) {
		t.Errorf("last decision after the call = %+v, want %+v", got, wantDecision)
	}
	tell := "Q1 keep-old-tasks is overtaken and takes no answer: " + why + "."
	marks := tellMarksOf(t, s, claimEpic)
	if len(marks) == 0 || !reflect.DeepEqual(marks[len(marks)-1], []any{tell, int64(0), int64(0)}) {
		t.Errorf("tells after the call (text, told, delivered) = %v, want the last %q untold and undelivered",
			marks, tell)
	}

	before := answerRowsOf(t, s)
	_, err := s.AnswerQuestion(ctx, claimEpic, "D9", "", 1, "first", Maker{By: MakerOwner}, callsAt())
	var refused *QuestionOvertakenError
	if !errors.As(err, &refused) || refused.Seq != 1 {
		t.Fatalf("AnswerQuestion of Q1 = %v, want a *QuestionOvertakenError for Q1", err)
	}
	if got := answerRowsOf(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("the refused answer changed the store:\n got %v\nwant %v", got, before)
	}

	releaseClaimEpic(t, s, callsAt().Add(time.Minute))
	if got := overtakenDecisionsOf(t, s); len(got) != 1 {
		t.Errorf("overtaken decisions after the release = %+v, want only the call's overtaken:Q1", got)
	}
}

// TestAStandsCallIsStoredAndKeepsItsQuestionOpen checks that a stands call stores its row and leaves its question open,
// with no decision and no tell, and that a later turn's call is stored after it, so the question reads its calls in
// turn order.
func TestAStandsCallIsStoredAndKeepsItsQuestionOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	keepOld := overtakenKeepOld()
	questionedEpic(t, s, keepOld)
	claimSecondTurn(t, s)
	decisions, tells := storedEpicRow(t, s).Decisions, claimTells(t, s)

	first := QuestionCallInput{Question: "Q1", Call: CallStands, Reason: "the old tasks still decide the layout"}
	if err := s.CompleteTurn(ctx, claimEpic, 2, callsPlan("wait", first), callsAt()); err != nil {
		t.Fatalf("CompleteTurn of turn 2: %v", err)
	}
	if got := storedEpicRow(t, s).Decisions; !reflect.DeepEqual(got, decisions) {
		t.Errorf("decisions after the stands call = %+v, want them unchanged %+v", got, decisions)
	}
	if got := claimTells(t, s); !reflect.DeepEqual(got, tells) {
		t.Errorf("tells after the stands call = %q, want them unchanged %q", got, tells)
	}

	addPendingTrigger3(t, s)
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn of turn 3: %v", err)
	}
	second := QuestionCallInput{Question: "Q1", Call: CallStands, Reason: "nothing changed since turn 2"}
	if err := s.CompleteTurn(ctx, claimEpic, 3, callsPlan("wait", second), callsAt()); err != nil {
		t.Fatalf("CompleteTurn of turn 3: %v", err)
	}
	open := openStored(keepOld, 1, 1)
	open.Calls = []QuestionCall{
		{Turn: 2, Call: CallStands, Reason: first.Reason}, {Turn: 3, Call: CallStands, Reason: second.Reason},
	}
	if got, want := questionsOf(t, s), []EpicQuestion{open}; !reflect.DeepEqual(got, want) {
		t.Errorf("questions after two stands calls = %+v, want %+v", got, want)
	}
}

// addPendingTrigger3 stores a pending trigger, t3, on claimEpic, for a third turn.
func addPendingTrigger3(t *testing.T, s *Store) {
	t.Helper()
	if err := s.fixtureQueries().InsertEpicTrigger(context.Background(), sqlcgen.InsertEpicTriggerParams{
		Epic: claimEpic, ID: "t3", Cause: "third trigger", State: triggerPending, Created: epicCreated().Unix(),
	}); err != nil {
		t.Fatalf("insert trigger t3: %v", err)
	}
}

// TestACallOnAQuestionAnsweredWhileTheTurnRanChangesNothing checks that the answer wins over a call: Q1 is answered
// while turn 2 runs, and turn 2's overtaken call on Q1 completes the turn and leaves Q1 answered, with no call row, no
// overtaken decision, and the answer's trigger pending for the next turn.
func TestACallOnAQuestionAnsweredWhileTheTurnRanChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	keepOld := overtakenKeepOld()
	questionedEpic(t, s, keepOld)
	claimSecondTurn(t, s)
	if _, err := s.AnswerQuestion(
		ctx,
		claimEpic,
		"D3",
		"",
		1,
		"first",
		Maker{By: MakerOwner},
		completedAt(),
	); err != nil {
		t.Fatalf("AnswerQuestion of Q1: %v", err)
	}

	plan := callsPlan(PlanOutcomeComplete, QuestionCallInput{Question: "Q1", Call: CallOvertaken, Reason: callsGotPast})
	if err := s.CompleteTurn(ctx, claimEpic, 2, plan, callsAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	answered := openStored(keepOld, 1, 1)
	answered.State, answered.Choice = QuestionAnswered, "first"
	if got, want := questionsOf(t, s), []EpicQuestion{answered}; !reflect.DeepEqual(got, want) {
		t.Errorf("questions after the call = %+v, want Q1 answered as it was %+v", got, want)
	}
	if got := overtakenDecisionsOf(t, s); len(got) != 0 {
		t.Errorf("overtaken decisions = %+v, want none", got)
	}
	if got := questionCallRows(t, s); len(got) != 0 {
		t.Errorf("call rows = %v, want none for a question no longer open", got)
	}
	if state, _ := triggerRow(t, s, claimEpic, "answer:D3"); state != triggerPending {
		t.Errorf("trigger answer:D3 = %q, want pending for the next turn", state)
	}
}

// TestCompleteTurnRefusesABadQuestionCallAndWritesNothing checks each refusal of a call: a question that is not Q<n>, a
// question called twice, a call that is not stands or overtaken, a blank reason, a stands call beside outcome complete,
// a question the epic does not hold, and a question no planner turn asked. Each is ErrBadQuestion naming the call, and
// the turn stays running with nothing stored.
func TestCompleteTurnRefusesABadQuestionCallAndWritesNothing(t *testing.T) {
	t.Parallel()
	stands := func(question string) QuestionCallInput {
		return QuestionCallInput{Question: question, Call: CallStands, Reason: "it still decides the layout"}
	}
	cases := []struct {
		name string
		plan EpicPlan
		want string
	}{
		{"not Q<n>", callsPlan("wait", stands("1")), `question call 1: question "1" is not Q<n>`},
		{"called twice", callsPlan("wait", stands("Q1"), stands("Q1")), "question call 2: Q1 is called twice"},
		{"unknown call", callsPlan("wait", QuestionCallInput{Question: "Q1", Call: "later", Reason: "r"}),
			`question call 1 Q1: call "later" is not stands or overtaken`},
		{"blank reason", callsPlan("wait", QuestionCallInput{Question: "Q1", Call: CallOvertaken, Reason: " "}),
			"question call 1 Q1: reason is empty"},
		{"stands beside complete", callsPlan(PlanOutcomeComplete, stands("Q1")),
			"question call 1 Q1: stands beside outcome complete"},
		{"no such question", callsPlan("wait", stands("Q9")), "question call 1: Q9: no such question"},
		{"asked by no planner turn", callsPlan("wait", stands("Q1"), stands("Q2")),
			"question call 2: Q2 was not asked by a planner turn"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			questionedEpic(t, s, overtakenKeepOld())
			if err := s.fixtureQueries().InsertEpicQuestion(ctx, sqlcgen.InsertEpicQuestionParams{
				Epic: claimEpic, Seq: 2, Turn: 0, Text: "credit ran out", Label: ParkLabel, Choices: "[]",
			}); err != nil {
				t.Fatalf("insert park question Q2: %v", err)
			}
			claimSecondTurn(t, s)
			questions := questionsOf(t, s)

			err := s.CompleteTurn(ctx, claimEpic, 2, c.plan, callsAt())
			if !errors.Is(err, ErrBadQuestion) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("CompleteTurn = %v, want ErrBadQuestion naming %q", err, c.want)
			}
			running, err := s.RunningTurns(ctx)
			if err != nil || len(running) != 1 || running[0].Seq != 2 {
				t.Errorf("running turns after the refusal = %+v (err %v), want turn 2 still running", running, err)
			}
			if got := questionsOf(t, s); !reflect.DeepEqual(got, questions) {
				t.Errorf("questions after the refusal = %+v, want them unchanged %+v", got, questions)
			}
			if got := questionCallRows(t, s); len(got) != 0 {
				t.Errorf("call rows after the refusal = %v, want none", got)
			}
		})
	}
}

// askQuestion is the question the ask tests' orchestrator puts to the owner on task 1.
func askQuestion() EpicQuestionInput {
	return EpicQuestionInput{
		Label: "capture-broken", Text: "T1 (contract-cannot-hold): the capture exits 2",
		Choices:     []EpicChoice{{ID: "edit", Does: "edit the contract"}, {ID: "end", Does: "end the epic"}},
		Recommended: "edit", Why: "nothing in the epic changes it", Confidence: "high",
	}
}

// askTask1 holds task 1 of epic, whose build is from, asked on askQuestion, as an orchestrator's ask does, and returns
// the seq of the question it opened.
func askTask1(t *testing.T, s *Store, epic string, from TaskBuild) int64 {
	t.Helper()
	ctx := context.Background()
	to := from
	to.Held, to.Decisions = "asked", from.Decisions+1
	question := askQuestion()
	if err := s.AskTask(ctx, epic, 1, from, to, question, question.Text,
		TaskDecision{ID: "T1-ask-1", Text: "T1 asks the owner"}, sendBackAt()); err != nil {
		t.Fatalf("AskTask: %v", err)
	}
	row, err := s.Epic(ctx, epic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	return row.Questions[len(row.Questions)-1].Seq
}

// questionOfEpic returns question seq of epic as the store reads it.
func questionOfEpic(t *testing.T, s *Store, epic string, seq int64) EpicQuestion {
	t.Helper()
	row, err := s.Epic(context.Background(), epic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	i := slices.IndexFunc(row.Questions, func(question EpicQuestion) bool { return question.Seq == seq })
	if i < 0 {
		t.Fatalf("epic %s holds no question %d", epic, seq)
	}
	return row.Questions[i]
}

// requireAskOvertaken checks that question seq of epic, an orchestrator's ask on task 1, is overtaken, with its
// overtaken decision naming how task 1 moved on, and a tell that says it takes no answer.
func requireAskOvertaken(t *testing.T, s *Store, epic string, seq int64, how string) {
	t.Helper()
	if got := questionOfEpic(t, s, epic, seq).State; got != QuestionOvertaken {
		t.Errorf("Q%d after task 1 moved on = %q, want overtaken", seq, got)
	}
	name := "Q" + strconv.FormatInt(seq, 10) + " capture-broken"
	why := "T1 moved on without its answer: " + how
	if got := decisionNamed(
		t,
		s,
		epic,
		"overtaken:Q"+strconv.FormatInt(seq, 10),
	).Text; got != name+" closed as overtaken: "+why+"." {
		t.Errorf("decision overtaken:Q%d = %q, want it to say %q", seq, got, why)
	}
	marks := tellMarksOf(t, s, epic)
	tell := name + " is overtaken and takes no answer: " + why + "."
	if len(marks) == 0 || marks[len(marks)-1][0] != tell {
		t.Errorf("tells of epic %s = %v, want the last %q", epic, marks, tell)
	}
}

// TestFailTaskOvertakesTheOpenAskOfItsTask checks that a fail_task of a task held asked climbs it off the build that
// waits on its question and overtakes the question in the same write, so no question is left open with no asker. The
// planner's own question of turn 1 stays open.
func TestFailTaskOvertakesTheOpenAskOfItsTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := sendBackStore(t)
	from := TaskBuild{Stage: "orchestrate", Base: sendBackBase, Attempt: 1, Step: "step-orchestrate"}
	if err := s.SetTaskBuild(ctx, sendBackEpic, 1, TaskBuild{}, from); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	asked := askTask1(t, s, sendBackEpic, from)

	sendBackFail(t, s, 1, "the capture cannot pass on this base")

	requireAskOvertaken(t, s, sendBackEpic, asked, "it climbed to rung 2")
	if got := questionOfEpic(t, s, sendBackEpic, 1).State; got != QuestionOpen {
		t.Errorf("the planner's Q1 after the fail = %q, want open", got)
	}
	if task := sendBackTask(t, s, 1); task.Build.Question != 0 || task.Build.Held != "" {
		t.Errorf("task 1 build after the fail = %+v, want no hold and no question", task.Build)
	}
}

// TestSendBackOvertakesTheOpenAskOfItsTask checks that a send_back of a task held asked reopens it on a fresh build and
// overtakes the question the old build waited on.
func TestSendBackOvertakesTheOpenAskOfItsTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := sendBackStore(t)
	from := TaskBuild{Stage: "orchestrate", Base: sendBackBase, Attempt: 2, Step: "step-orchestrate"}
	if err := s.SetTaskBuild(ctx, sendBackEpic, 1, TaskBuild{}, from); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	asked := askTask1(t, s, sendBackEpic, from)

	sendBackCarry(t, s, sendBackAction(t, s, 1, sendBackInput()))

	requireAskOvertaken(t, s, sendBackEpic, asked, "it was reopened at implement on rung 1")
}

// TestAClimbLeavesAnAnsweredAskAsItIs checks that a climb of a task whose ask the owner answered overtakes nothing: the
// question stays answered and no overtaken decision is recorded.
func TestAClimbLeavesAnAnsweredAskAsItIs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := sendBackStore(t)
	from := TaskBuild{Stage: "orchestrate", Base: sendBackBase, Attempt: 1, Step: "step-orchestrate"}
	if err := s.SetTaskBuild(ctx, sendBackEpic, 1, TaskBuild{}, from); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	asked := askTask1(t, s, sendBackEpic, from)
	if _, err := s.AnswerQuestion(ctx, sendBackEpic, "D-edit", "", asked, "edit", Maker{By: MakerOwner},
		sendBackAt()); err != nil {
		t.Fatalf("AnswerQuestion of Q%d: %v", asked, err)
	}

	sendBackFail(t, s, 1, "the capture cannot pass on this base")

	if got := questionOfEpic(t, s, sendBackEpic, asked); got.State != QuestionAnswered || got.Choice != "edit" {
		t.Errorf("Q%d after the climb = %q %q, want answered edit", asked, got.State, got.Choice)
	}
	for _, decision := range closeRow(t, s, sendBackEpic).Decisions {
		if strings.HasPrefix(decision.ID, "overtaken:") {
			t.Errorf("decision %s after the climb, want no overtaken decision", decision.ID)
		}
	}
}

// TestSplitOvertakesTheOpenAskOfItsTask checks that a split of a task whose build is held asked ends the task split and
// overtakes its question, naming the descendants, since a split task never runs again.
func TestSplitOvertakesTheOpenAskOfItsTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	splitSeed(t, s, "")
	asked := askTask1(t, s, climbEpic, climbTaskOf(ctx, t, s, 1).Build)

	if _, err := s.CarryOutAction(ctx, splitAction(t, s, splitDescendants()), closeAt()); err != nil {
		t.Fatalf("CarryOutAction of the split: %v", err)
	}

	if got := climbTaskOf(ctx, t, s, 1).Status; got != TaskSplit {
		t.Fatalf("task 1 after the split = %q, want split", got)
	}
	requireAskOvertaken(t, s, climbEpic, asked, "it was split into T2, T3")
}

// TestClaimTurnRefusesAnEpicInAFinalPhase checks that no turn is claimed on an epic in a final phase, whatever trigger it
// holds pending, and that an epic ended but not final is still claimed.
func TestClaimTurnRefusesAnEpicInAFinalPhase(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{
		phaseMetAtBase, phaseReleased, phaseDone, PhaseClosedSuperseded, PhaseClosedShipped, PhaseClosedAbandoned,
		PhaseClean, phaseEndedWithGaps, PhaseStopped,
	} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			createEpic(t, s, fullEpic(claimEpic))
			if _, err := s.fixtureExec(ctx, `UPDATE epics SET phase = ? WHERE id = ?`, phase, claimEpic); err != nil {
				t.Fatalf("set phase %s: %v", phase, err)
			}
			_, err := s.ClaimTurn(ctx, claimEpic, claimedAt())
			if FinalPhase(phase) {
				if !errors.Is(err, ErrNotClaimable) {
					t.Errorf("ClaimTurn of an epic in %s = %v, want ErrNotClaimable", phase, err)
				}
				return
			}
			if err != nil {
				t.Errorf("ClaimTurn of an epic in %s = %v, want a claimed turn", phase, err)
			}
		})
	}
}

// TestAnAnswerOnAMetAtBaseEpicWritesNoTrigger checks that the end answer that moves an epic into met_at_base consumes
// its own answer: trigger in that write, and that an answer to the final park question opened there after writes none,
// so no trigger of the epic is left pending.
func TestAnAnswerOnAMetAtBaseEpicWritesNoTrigger(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	parkRedMetAtBase(t, s)
	if _, err := s.AnswerQuestion(ctx, claimEpic, "D3", "", 1, ChoiceEnd, Maker{}, claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion end: %v", err)
	}
	if err := s.ParkEpic(ctx, claimEpic, ParkClassBranch, "credit ran out", completedAt()); err != nil {
		t.Fatalf("ParkEpic on the epic ended at base: %v", err)
	}

	if _, err := s.AnswerQuestion(ctx, claimEpic, "D4", "Keep the words", 2, ChoiceOther, Maker{},
		callsAt()); err != nil {
		t.Fatalf("AnswerQuestion of the final park: %v", err)
	}

	for _, trigger := range triggersOf(t, s) {
		if trigger.ID == "answer:D4" {
			t.Errorf("trigger %s = %+v, want none for an answer on an epic in met_at_base", trigger.ID, trigger)
		}
		if trigger.State == triggerPending {
			t.Errorf("trigger %s is pending on an epic in met_at_base, want it consumed", trigger.ID)
		}
	}
}

// TestATurnFreedOnAnEpicThatEndedForGoodConsumesItsTriggers checks that a turn still running when its epic is released
// and then freed hands its claimed triggers to no one: they are consumed, not returned to pending, since no turn of the
// released epic would claim them.
func TestATurnFreedOnAnEpicThatEndedForGoodConsumesItsTriggers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	releaseClaimEpic(t, s, completedAt())

	if err := s.FreeTurn(ctx, claimEpic, 1, TurnFree{Reason: "engine_restarted"}, callsAt()); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	if got, want := triggerStatesOf(t, s), []string{"consumed", "consumed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("trigger states after the free = %v, want %v", got, want)
	}
}

// triggerStatesOf returns the state of each trigger of claimEpic, in the order triggersOf reads them.
func triggerStatesOf(t *testing.T, s *Store) []string {
	t.Helper()
	triggers := triggersOf(t, s)
	states := make([]string, 0, len(triggers))
	for _, trigger := range triggers {
		states = append(states, trigger.State)
	}
	return states
}

// TestFinalPhaseTriggersConsumedByTheQuestionCallsMigration stages the store at version 122 with one epic in each final
// phase and three epics that are not final, each holding a pending trigger beside a consumed one, and steps it up to
// 00123. Each pending trigger of an epic in a final phase is consumed, and every other trigger keeps its state.
func TestFinalPhaseTriggersConsumedByTheQuestionCallsMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, provider := storeAtVersion(t, 122)
	phases := []string{
		"met_at_base", "released", "done", "closed_superseded", "closed_shipped", "closed_abandoned",
		"building", "clean", "ended_with_gaps",
	}
	for i, phase := range phases {
		id := "orrery-0123456/epic-" + phase
		if _, err := s.fixtureExec(ctx, `INSERT INTO epics
			(id, repository, title, approved, intent, care, approach, release_then, checks, phase, created, changed)
			VALUES (?, '/home/user/project', 'Epic', 'approved line', 'intent', 'serious', 'approach', 'then',
			'checks', ?, 1700000000, 1700000000)`, id, phase); err != nil {
			t.Fatalf("insert epic in %s: %v", phase, err)
		}
		for _, trigger := range []struct{ id, state string }{{"created", "consumed"}, {"answer:D1", "pending"}} {
			if _, err := s.fixtureExec(ctx, `INSERT INTO epic_triggers (epic, id, cause, state, turn, created)
				VALUES (?, ?, 'a cause', ?, 0, ?)`, id, trigger.id, trigger.state, 1700000000+i); err != nil {
				t.Fatalf("insert trigger %s of the epic in %s: %v", trigger.id, phase, err)
			}
		}
	}

	if _, err := provider.UpByOne(ctx); err != nil {
		t.Fatalf("Up of 00123: %v", err)
	}
	for _, phase := range phases {
		want := "pending"
		if FinalPhase(phase) {
			want = "consumed"
		}
		var state string
		if err := s.db.QueryRowContext(ctx, `SELECT state FROM epic_triggers WHERE epic = ? AND id = 'answer:D1'`,
			"orrery-0123456/epic-"+phase).Scan(&state); err != nil {
			t.Fatalf("read trigger of the epic in %s: %v", phase, err)
		}
		if state != want {
			t.Errorf("answer:D1 of the epic in %s after the Up = %q, want %q", phase, state, want)
		}
	}
	var consumed int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM epic_triggers WHERE id = 'created' AND state = 'consumed'`).
		Scan(&consumed); err != nil ||
		consumed != len(phases) {
		t.Errorf("consumed created triggers after the Up = %d (err %v), want all %d kept", consumed, err, len(phases))
	}
}

// questionCallsDownAndUp is the walk's check of 00123_epic_question_calls.sql: a stored call goes with its table on the
// Down, and the Up makes the table again with no call in it.
func questionCallsDownAndUp() migrationCheck {
	return migrationCheck{
		seed: func(t *testing.T, s *Store) {
			t.Helper()
			if _, err := s.db.DB.Exec(`INSERT INTO epic_question_calls (epic, question, turn, call, reason)
				VALUES ('repo/epic', 1, 2, 'stands', 'it still decides the layout')`); err != nil {
				t.Fatalf("insert call row: %v", err)
			}
		},
		down: func(t *testing.T, s *Store) {
			t.Helper()
			if tableNames(t, s)["epic_question_calls"] {
				t.Errorf("epic_question_calls after Down: present, want absent")
			}
		},
		up: func(t *testing.T, s *Store) {
			t.Helper()
			var calls int
			if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM epic_question_calls`).
				Scan(&calls); err != nil ||
				calls != 0 {
				t.Errorf(
					"epic_question_calls after Down and Up holds %d rows (err %v), want an empty table",
					calls,
					err,
				)
			}
		},
	}
}
