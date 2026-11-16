package enginehost

import (
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// askQuestions completes one planner turn on epicServiceID that asks the questions with the texts given, so they are
// stored open from Q1 in the order given.
func askQuestions(t *testing.T, st *store.Store, texts ...string) {
	t.Helper()
	turn, err := st.ClaimTurn(t.Context(), epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	questions := make([]store.EpicQuestionInput, 0, len(texts))
	for i, text := range texts {
		questions = append(questions, syntheticQuestion(fmt.Sprintf("synthetic-question-%d", i+1), text))
	}
	plan := plannedReturn()
	plan.Questions = questions
	if err = st.CompleteTurn(t.Context(), epicServiceID, turn.Seq, plan, time.Unix(turnCompleted, 0)); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// answerEpic sends an answer with id, text, question and choice to epicServiceID through client. question and choice
// are empty for a note. It returns the reply's message, or the error the call returned.
func answerEpic(
	t *testing.T,
	client enginev1connect.EpicsClient,
	id, text, question, choice string,
) (*v1.AnswerEpicResponse, error) {
	t.Helper()
	reply, err := client.AnswerEpic(t.Context(), connect.NewRequest(&v1.AnswerEpicRequest{
		EpicId: epicServiceID, Id: id, Text: text, Question: question, Choice: choice,
	}))
	if err != nil {
		return nil, err
	}
	return reply.Msg, nil
}

func TestEpicAnswerToOpenQuestionTiesDecisionAndClosesIt(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")

	reply, err := answerEpic(t, client, "A1", "Ship it.", "Q1", store.ChoiceOther)
	if err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	decision := reply.GetDecision()
	if decision.GetSeq() != 4 || decision.GetQuestion() != "Q1" || decision.GetText() != "Ship it." {
		t.Fatalf("decision = %v, want seq 4 answering Q1 with %q", decision, "Ship it.")
	}
	if reply.GetWarning() != "" {
		t.Fatalf("warning = %q, want none for an answer to a question", reply.GetWarning())
	}
	answered := syntheticQuestionMessage("Q1", 1, "synthetic-question-1", "Synthetic question one?")
	answered.State, answered.Choice = "answered", store.ChoiceOther
	wantProtos(t, "questions", getEpicOK(t, client).GetQuestions(), []*v1.EpicQuestion{answered})
}

func TestEpicAnswerWithDeclaredChoiceRecordsTheChoiceWithItsText(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")

	reply, err := answerEpic(t, client, "A1", "", "Q1", "synthetic-a")
	if err != nil {
		t.Fatalf("AnswerEpic with a declared choice and no words: %v", err)
	}
	decision := reply.GetDecision()
	if decision.GetSeq() != 4 || decision.GetQuestion() != "Q1" || decision.GetText() != "" {
		t.Fatalf("decision = %v, want seq 4 answering Q1 with no text", decision)
	}
	answered := syntheticQuestionMessage("Q1", 1, "synthetic-question-1", "Synthetic question one?")
	answered.State, answered.Choice = "answered", "synthetic-a"
	wantProtos(t, "questions", getEpicOK(t, client).GetQuestions(), []*v1.EpicQuestion{answered})
}

func TestEpicNoteWithOpenQuestionsWarnsNamingThem(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?", "Synthetic question two?")

	reply, err := answerEpic(t, client, "A1", "Ship it.", "", "")
	if err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	decision := reply.GetDecision()
	if decision.GetSeq() != 4 || decision.GetQuestion() != "" || decision.GetText() != "Ship it." {
		t.Fatalf("decision = %v, want seq 4 as a note with %q", decision, "Ship it.")
	}
	want := "recorded as a note: it answers no question, and Q1, Q2 are open"
	if reply.GetWarning() != want {
		t.Fatalf("warning = %q, want %q", reply.GetWarning(), want)
	}
	wantProtos(t, "questions", getEpicOK(t, client).GetQuestions(), []*v1.EpicQuestion{
		syntheticQuestionMessage("Q1", 1, "synthetic-question-1", "Synthetic question one?"),
		syntheticQuestionMessage("Q2", 1, "synthetic-question-2", "Synthetic question two?"),
	})
}

func TestEpicNoteWithNoOpenQuestionHasNoWarning(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")
	if _, err := answerEpic(t, client, "A1", "Ship it.", "Q1", store.ChoiceOther); err != nil {
		t.Fatalf("AnswerEpic of Q1: %v", err)
	}

	reply, err := answerEpic(t, client, "A2", "Hold it.", "", "")
	if err != nil {
		t.Fatalf("AnswerEpic note: %v", err)
	}
	if reply.GetWarning() != "" {
		t.Fatalf("warning = %q, want none when no question is open", reply.GetWarning())
	}
	if reply.GetDecision().GetQuestion() != "" {
		t.Fatalf("question = %q, want empty for a note", reply.GetDecision().GetQuestion())
	}
}

func TestEpicAnswerRefusedRequestsRecordNoDecision(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")
	cases := []struct {
		text     string
		question string
		choice   string
		want     string
	}{
		{
			text:   "Ship it.",
			choice: "synthetic-a",
			want:   "choice: a note answers no question",
		},
		{
			text:     "Ship it.",
			question: "Q1",
			want:     "choice: empty",
		},
		{
			text:     "Ship it.",
			question: "Q1",
			choice:   "synthetic-z",
			want:     "choice: Q1 offers no choice synthetic-z",
		},
		{
			text:     " \t ",
			question: "Q1",
			choice:   store.ChoiceOther,
			want:     "text: empty",
		},
	}
	// The rows run in the test body, not as subtests, so the decisions check below runs after every row.
	for _, tc := range cases {
		_, err := answerEpic(t, client, "A1", tc.text, tc.question, tc.choice)
		wantConnectError(t, err, connect.CodeInvalidArgument, tc.want)
	}
	if decisions := getEpicOK(t, client).GetDecisions(); len(decisions) != 3 {
		t.Fatalf("decisions = %d after refused answers, want the three seeds", len(decisions))
	}
}

func TestEpicAnswerNamingMissingQuestionIsNotFound(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")

	_, err := answerEpic(t, client, "A1", "Ship it.", "Q9", store.ChoiceOther)
	wantConnectError(t, err, connect.CodeNotFound, "question: no question Q9 on epic "+epicServiceID)
	if decisions := getEpicOK(t, client).GetDecisions(); len(decisions) != 3 {
		t.Fatalf("decisions = %d after a refused answer, want the three seeds", len(decisions))
	}
}

func TestEpicAnswerNamingMalformedQuestionIsNotFound(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")

	for _, question := range []string{"banana", "Q0"} {
		_, err := answerEpic(t, client, "A1", "Ship it.", question, store.ChoiceOther)
		wantConnectError(t, err, connect.CodeNotFound, "question: no question "+question+" on epic "+epicServiceID)
	}
	if decisions := getEpicOK(t, client).GetDecisions(); len(decisions) != 3 {
		t.Fatalf("decisions = %d after refused answers, want the three seeds", len(decisions))
	}
}

func TestEpicAnswerToClosedQuestionIsFailedPrecondition(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")
	if _, err := answerEpic(t, client, "A1", "Ship it.", "Q1", store.ChoiceOther); err != nil {
		t.Fatalf("AnswerEpic of Q1: %v", err)
	}

	_, err := answerEpic(t, client, "A2", "Hold it.", "Q1", store.ChoiceOther)
	wantConnectError(t, err, connect.CodeFailedPrecondition, "question: Q1 is closed")
	if decisions := getEpicOK(t, client).GetDecisions(); len(decisions) != 4 {
		t.Fatalf("decisions = %d after a refused answer, want the three seeds and A1", len(decisions))
	}
}

func TestEpicAnswerToReplacedQuestionIsFailedPrecondition(t *testing.T) {
	t.Parallel()
	st, path := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")
	insertPendingTrigger(t, path, "t2")
	second, err := st.ClaimTurn(t.Context(), epicServiceID, time.Unix(turnRestarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn of second turn: %v", err)
	}
	replacing := syntheticQuestion("synthetic-question-two", "Synthetic question two?")
	replacing.Replaces = "Q1"
	plan := plannedReturn()
	plan.Questions = []store.EpicQuestionInput{replacing}
	if err = st.CompleteTurn(t.Context(), epicServiceID, second.Seq, plan, time.Unix(turnFreed, 0)); err != nil {
		t.Fatalf("CompleteTurn of second turn: %v", err)
	}

	_, err = answerEpic(t, client, "A1", "Ship it.", "Q1", store.ChoiceOther)
	wantConnectError(t, err, connect.CodeFailedPrecondition, "question: Q1 was replaced by Q2")
	if decisions := getEpicOK(t, client).GetDecisions(); len(decisions) != 3 {
		t.Fatalf("decisions = %d after a refused answer, want the three seeds", len(decisions))
	}
}

func TestEpicRepeatAnswerToQuestionReturnsStoredDecision(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")

	first, err := answerEpic(t, client, "A1", "Ship it.", "Q1", store.ChoiceOther)
	if err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	if first.GetDecision().GetQuestion() != "Q1" {
		t.Fatalf("question = %q, want Q1", first.GetDecision().GetQuestion())
	}

	repeat, err := answerEpic(t, client, "A1", "Ship it.", "Q1", store.ChoiceOther)
	if err != nil {
		t.Fatalf("repeat AnswerEpic: %v", err)
	}
	if !proto.Equal(repeat.GetDecision(), first.GetDecision()) {
		t.Fatalf("repeat decision = %v, want the stored %v", repeat.GetDecision(), first.GetDecision())
	}
	if decisions := getEpicOK(t, client).GetDecisions(); len(decisions) != 4 {
		t.Fatalf("decisions = %d after a repeat, want the three seeds and A1", len(decisions))
	}
}
