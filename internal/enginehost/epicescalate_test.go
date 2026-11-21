package enginehost

import (
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// escalateReason is the reason the escalation tests give.
const escalateReason = "Synthetic reason for escalating."

// escalateWorld returns a client of the epic epicServiceID, created by the conversation steerOnlySteerer, with question Q1
// asked and open. The store also holds steerOnlyOther, a conversation that does not steer the epic. The client's calls
// read the steps through steps.
func escalateWorld(t *testing.T, steps StepCommands) enginev1connect.EpicsClient {
	t.Helper()
	st := epicTestStore(t)
	engines := []store.EngineOp{
		store.EngineAdd{ID: steerOnlySteerer, Dir: "/work/steerer"},
		store.EngineAdd{ID: steerOnlyOther, Dir: "/work/other"},
	}
	if err := st.ApplyEngine(t.Context(), engines, fixedClock()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	client := newEpicsClient(t, startControlServer(t, st, steps))
	if err := steererSetCreate(t, st, client, stepGitTestRepo(t), sessionHeader(steerOnlySteerer)); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	askQuestions(t, st, "Synthetic question one?")
	return client
}

// openQuestionOne returns the message of Q1 of epicServiceID as escalateWorld asks it, open and not escalated.
func openQuestionOne() *v1.EpicQuestion {
	return syntheticQuestionMessage("Q1", 1, "synthetic-question-1", "Synthetic question one?")
}

// TestEpicServiceEscalate_FromSteerer escalates Q1 from the steering conversation. The reply and a GetEpic read both show
// Q1 escalated with its reason, by the conversation and its session.
func TestEpicServiceEscalate_FromSteerer(t *testing.T) {
	t.Parallel()
	client := escalateWorld(t, amendSteps{})

	reply, err := client.EscalateEpic(t.Context(), steerOnlyRequest(sessionHeader(steerOnlySteerer),
		&v1.EscalateEpicRequest{Id: epicServiceID, Question: "Q1", Reason: escalateReason}))
	if err != nil {
		t.Fatalf("EscalateEpic: %v", err)
	}
	got := reply.Msg.GetQuestion()
	if got.GetEscalated() == 0 {
		t.Fatalf("escalated of the reply = 0, want the time of the escalation")
	}
	want := openQuestionOne()
	want.Escalated = got.GetEscalated()
	want.EscalationReason = escalateReason
	want.EscalationBy = store.MakerConversation
	want.EscalationSession = steerOnlySteerer
	if !proto.Equal(got, want) {
		t.Errorf("escalated question = %v, want %v", got, want)
	}
	wantProtos(t, "questions", getEpicOK(t, client).GetQuestions(), []*v1.EpicQuestion{want})
}

// TestEpicServiceEscalate_FromOwner escalates Q1 with no caller headers. The escalation names the owner as its maker, with
// no session.
func TestEpicServiceEscalate_FromOwner(t *testing.T) {
	t.Parallel()
	client := escalateWorld(t, amendSteps{})

	reply, err := client.EscalateEpic(t.Context(), connect.NewRequest(&v1.EscalateEpicRequest{
		Id: epicServiceID, Question: "Q1", Reason: escalateReason,
	}))
	if err != nil {
		t.Fatalf("EscalateEpic: %v", err)
	}
	got := reply.Msg.GetQuestion()
	if got.GetEscalated() == 0 {
		t.Fatalf("escalated of the reply = 0, want the time of the escalation")
	}
	if got.GetEscalationBy() != store.MakerOwner || got.GetEscalationSession() != "" {
		t.Errorf("escalation maker = %q session %q, want the owner with no session",
			got.GetEscalationBy(), got.GetEscalationSession())
	}
}

// TestEpicServiceEscalate_StepOfEpicRefused escalates Q1 from a step of the epic. The call is FailedPrecondition and Q1
// stays unescalated.
func TestEpicServiceEscalate_StepOfEpicRefused(t *testing.T) {
	t.Parallel()
	steps := amendSteps{rows: map[string]store.StepRow{
		steerOnlyStep: {ID: steerOnlyStep, Epic: epicServiceID},
	}}
	client := escalateWorld(t, steps)

	_, err := client.EscalateEpic(t.Context(), steerOnlyRequest(sessionHeader(steerOnlyStep),
		&v1.EscalateEpicRequest{Id: epicServiceID, Question: "Q1", Reason: escalateReason}))
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v (%v), want FailedPrecondition", got, err)
	}
	if !strings.Contains(err.Error(), "step "+steerOnlyStep+" of epic "+epicServiceID) {
		t.Errorf("refusal %q does not name the step and the epic", err)
	}
	wantProtos(t, "questions", getEpicOK(t, client).GetQuestions(), []*v1.EpicQuestion{openQuestionOne()})
}

// TestEpicServiceEscalate_OtherConversationRefused escalates Q1 from a conversation that does not steer the epic. The call
// is FailedPrecondition, naming the steerer and epic_steer, and Q1 stays unescalated.
func TestEpicServiceEscalate_OtherConversationRefused(t *testing.T) {
	t.Parallel()
	client := escalateWorld(t, amendSteps{})

	_, err := client.EscalateEpic(t.Context(), steerOnlyRequest(sessionHeader(steerOnlyOther),
		&v1.EscalateEpicRequest{Id: epicServiceID, Question: "Q1", Reason: escalateReason}))
	wantSteerRefusal(t, err, steerOnlyOther, steerOnlySteerer)
	wantProtos(t, "questions", getEpicOK(t, client).GetQuestions(), []*v1.EpicQuestion{openQuestionOne()})
}

// TestEpicServiceEscalate_EmptyReasonInvalid escalates Q1 with a reason that is empty or white space. Each call is
// InvalidArgument and Q1 stays unescalated.
func TestEpicServiceEscalate_EmptyReasonInvalid(t *testing.T) {
	t.Parallel()
	client := escalateWorld(t, amendSteps{})

	for _, reason := range []string{"", " \t "} {
		_, err := client.EscalateEpic(t.Context(), connect.NewRequest(&v1.EscalateEpicRequest{
			Id: epicServiceID, Question: "Q1", Reason: reason,
		}))
		wantConnectError(t, err, connect.CodeInvalidArgument, "reason: empty")
	}
	wantProtos(t, "questions", getEpicOK(t, client).GetQuestions(), []*v1.EpicQuestion{openQuestionOne()})
}

// TestEpicServiceEscalate_UnknownQuestionNotFound escalates Q9, which the epic does not hold. The call is NotFound on
// question.
func TestEpicServiceEscalate_UnknownQuestionNotFound(t *testing.T) {
	t.Parallel()
	client := escalateWorld(t, amendSteps{})

	_, err := client.EscalateEpic(t.Context(), connect.NewRequest(&v1.EscalateEpicRequest{
		Id: epicServiceID, Question: "Q9", Reason: escalateReason,
	}))
	wantConnectError(t, err, connect.CodeNotFound, "question: no question Q9 on epic "+epicServiceID)
}

// TestEpicServiceEscalate_AnsweredQuestionRefused answers Q1, then escalates it. The call is FailedPrecondition with the
// store's refusal, and Q1 stays answered and unescalated.
func TestEpicServiceEscalate_AnsweredQuestionRefused(t *testing.T) {
	t.Parallel()
	client := escalateWorld(t, amendSteps{})
	if _, err := answerEpic(t, client, "A1", "Ship it.", "Q1", store.ChoiceOther); err != nil {
		t.Fatalf("AnswerEpic of Q1: %v", err)
	}

	_, err := client.EscalateEpic(t.Context(), connect.NewRequest(&v1.EscalateEpicRequest{
		Id: epicServiceID, Question: "Q1", Reason: escalateReason,
	}))
	wantConnectError(t, err, connect.CodeFailedPrecondition,
		fmt.Sprintf("question 1 of epic %s is answered, not open", epicServiceID))
	answered := openQuestionOne()
	answered.State, answered.Choice = "answered", store.ChoiceOther
	wantProtos(t, "questions", getEpicOK(t, client).GetQuestions(), []*v1.EpicQuestion{answered})
}
