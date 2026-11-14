package enginehost

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// askQuestions completes one planner turn on epicServiceID that asks questions, so they are stored open from Q1 in
// the order given.
func askQuestions(t *testing.T, st *store.Store, questions ...string) {
	t.Helper()
	turn, err := st.ClaimTurn(t.Context(), epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := plannedReturn()
	plan.Questions = questions
	if err = st.CompleteTurn(t.Context(), epicServiceID, turn.Seq, plan, time.Unix(turnCompleted, 0)); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// answerEpic sends an answer with id, text and question to epicServiceID through client. question is empty for a
// note. It returns the reply's message, or the error the call returned.
func answerEpic(
	t *testing.T,
	client enginev1connect.EpicsClient,
	id, text, question string,
) (*v1.AnswerEpicResponse, error) {
	t.Helper()
	reply, err := client.AnswerEpic(t.Context(), connect.NewRequest(&v1.AnswerEpicRequest{
		EpicId: epicServiceID, Id: id, Text: text, Question: question,
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

	reply, err := answerEpic(t, client, "A1", "Ship it.", "Q1")
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
	wantProtos(t, "questions", getEpicOK(t, client).GetQuestions(), []*v1.EpicQuestion{
		{Id: "Q1", Turn: 1, Text: "Synthetic question one?", State: "answered"},
	})
}

func TestEpicNoteWithOpenQuestionsWarnsNamingThem(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?", "Synthetic question two?")

	reply, err := answerEpic(t, client, "A1", "Ship it.", "")
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
		{Id: "Q1", Turn: 1, Text: "Synthetic question one?", State: "open"},
		{Id: "Q2", Turn: 1, Text: "Synthetic question two?", State: "open"},
	})
}

func TestEpicNoteWithNoOpenQuestionHasNoWarning(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")
	if _, err := answerEpic(t, client, "A1", "Ship it.", "Q1"); err != nil {
		t.Fatalf("AnswerEpic of Q1: %v", err)
	}

	reply, err := answerEpic(t, client, "A2", "Hold it.", "")
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

func TestEpicAnswerNamingMissingQuestionIsNotFound(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")

	_, err := answerEpic(t, client, "A1", "Ship it.", "Q9")
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
		_, err := answerEpic(t, client, "A1", "Ship it.", question)
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
	if _, err := answerEpic(t, client, "A1", "Ship it.", "Q1"); err != nil {
		t.Fatalf("AnswerEpic of Q1: %v", err)
	}

	_, err := answerEpic(t, client, "A2", "Hold it.", "Q1")
	wantConnectError(t, err, connect.CodeFailedPrecondition, "question: Q1 is closed")
	if decisions := getEpicOK(t, client).GetDecisions(); len(decisions) != 4 {
		t.Fatalf("decisions = %d after a refused answer, want the three seeds and A1", len(decisions))
	}
}

func TestEpicRepeatAnswerToQuestionReturnsStoredDecision(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	askQuestions(t, st, "Synthetic question one?")

	first, err := answerEpic(t, client, "A1", "Ship it.", "Q1")
	if err != nil {
		t.Fatalf("AnswerEpic: %v", err)
	}
	if first.GetDecision().GetQuestion() != "Q1" {
		t.Fatalf("question = %q, want Q1", first.GetDecision().GetQuestion())
	}

	repeat, err := answerEpic(t, client, "A1", "Ship it.", "Q1")
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
