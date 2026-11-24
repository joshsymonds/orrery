package stage

import (
	"reflect"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// questionSession is the sessions row the question tests answer from, as a conversation's session header names it.
const questionSession = "session-question"

// questionText, questionOptions and questionRecommendation are the question the question tests ask.
const questionText = "Should the scout stay?"

const questionRecommendation = "Keep the scout"

// questionOptions returns the options the question tests ask between, as a fresh slice.
func questionOptions() []string {
	return []string{"Keep the scout", "Drop the scout"}
}

// seedQuestionSession adds the sessions row questionSession to the store of fx.
func seedQuestionSession(t *testing.T, fx stageResearchFixture) {
	t.Helper()
	add := store.EngineAdd{ID: questionSession, Dir: "/synthetic/dir"}
	if err := fx.store.ApplyEngine(t.Context(), []store.EngineOp{add}, stageResearchNow()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// askQuestion asks the question of the tests on stageDesign through the service of fx, and returns the question Ask
// replies with.
func askQuestion(t *testing.T, fx stageResearchFixture) *v1.StageQuestion {
	t.Helper()
	reply, err := fx.service.Ask(t.Context(), connect.NewRequest(&v1.AskRequest{
		Design: stageDesign, Text: questionText, Options: questionOptions(), Recommendation: questionRecommendation,
	}))
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	return reply.Msg.GetQuestion()
}

// answer answers the question msg.Question of stageDesign through the service of fx, from the session header session,
// or from no session when session is "". It returns the question Answer replies with, or the error it returns.
func answer(
	t *testing.T, fx stageResearchFixture, session string, msg *v1.StageAnswerRequest,
) (*v1.StageQuestion, error) {
	t.Helper()
	msg.Design = stageDesign
	req := connect.NewRequest(msg)
	if session != "" {
		req.Header().Set(protocol.HeaderSession, session)
	}
	reply, err := fx.service.Answer(t.Context(), req)
	if err != nil {
		return nil, err
	}
	return reply.Msg.GetQuestion(), nil
}

// storedQuestions returns the questions the store of fx holds for stageDesign, oldest first.
func storedQuestions(t *testing.T, fx stageResearchFixture) []store.DesignQuestion {
	t.Helper()
	questions, err := fx.store.DesignQuestions(t.Context(), stageDesign)
	if err != nil {
		t.Fatalf("DesignQuestions: %v", err)
	}
	return questions
}

// ownerServe is the maker of an answer the owner gives through serve.
func ownerServe() *v1.StageMaker {
	return &v1.StageMaker{Kind: store.StageMakerOwnerServe}
}

func TestStageQuestionsAskStoresQuestionWithOptionsAndRecommendation(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	want := &v1.StageQuestion{
		Id: 1, Text: questionText, Options: questionOptions(), Recommendation: questionRecommendation,
		Asked: stageResearchNow().Unix(),
	}
	if got := askQuestion(t, fx); !proto.Equal(got, want) {
		t.Fatalf("Ask = %v, want %v", got, want)
	}

	stored := storedQuestions(t, fx)
	if len(stored) != 1 || stored[0].Seq != 1 || stored[0].Text != questionText ||
		!reflect.DeepEqual(
			stored[0].Options,
			questionOptions(),
		) || stored[0].Recommendation != questionRecommendation ||
		stored[0].Asked.Unix() != stageResearchNow().Unix() || !stored[0].Answered.IsZero() {
		t.Fatalf("stored questions = %+v, want one open question numbered 1 with its options", stored)
	}
	if second := askQuestion(t, fx); second.GetId() != 2 {
		t.Fatalf("second question id = %d, want 2, numbered within the design", second.GetId())
	}
}

func TestStageQuestionsAskNamesContractOfDesign(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	const contract = "contract-one"
	if err := fx.store.ProposeContractInDesign(t.Context(), stageDesign, contract, store.ContractVersion{
		Version: 1, Repository: "/repo", Title: "Contract one", Care: "serious", Body: `{"id":"contract-one"}`,
		Words: "words", Writer: "conversation-one", At: stageResearchNow().Unix(),
	}); err != nil {
		t.Fatalf("ProposeContractInDesign: %v", err)
	}
	reply, err := fx.service.Ask(t.Context(), connect.NewRequest(&v1.AskRequest{
		Design: stageDesign, Contract: contract, Text: questionText, Options: questionOptions(),
		Recommendation: questionRecommendation,
	}))
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got := reply.Msg.GetQuestion().GetContract(); got != contract {
		t.Fatalf("question contract = %q, want %q", got, contract)
	}
	if stored := storedQuestions(t, fx); len(stored) != 1 || stored[0].Contract != contract {
		t.Fatalf("stored questions = %+v, want one question on %q", stored, contract)
	}
}

func TestStageQuestionsAskRefusesInvalidQuestions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*v1.AskRequest)
		code   connect.Code
	}{
		{name: "blank text", mutate: func(req *v1.AskRequest) { req.Text = " " }, code: connect.CodeInvalidArgument},
		{name: "no options", mutate: func(req *v1.AskRequest) { req.Options = nil }, code: connect.CodeInvalidArgument},
		{name: "blank option", mutate: func(req *v1.AskRequest) {
			req.Options = []string{""}
			req.Recommendation = ""
		}, code: connect.CodeInvalidArgument},
		{name: "recommendation not an option", mutate: func(req *v1.AskRequest) {
			req.Recommendation = "Maybe"
		}, code: connect.CodeInvalidArgument},
		{name: "contract not of the design", mutate: func(req *v1.AskRequest) {
			req.Contract = "contract-missing"
		}, code: connect.CodeInvalidArgument},
		{name: "unknown design", mutate: func(req *v1.AskRequest) {
			req.Design = "no-such-design"
		}, code: connect.CodeNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newStageResearchFixture(t)
			req := &v1.AskRequest{
				Design:         stageDesign,
				Text:           questionText,
				Options:        questionOptions(),
				Recommendation: questionRecommendation,
			}
			tc.mutate(req)
			if _, err := fx.service.Ask(t.Context(), connect.NewRequest(req)); connect.CodeOf(err) != tc.code {
				t.Fatalf("Ask code = %v, want %v (err %v)", connect.CodeOf(err), tc.code, err)
			}
			if stored := storedQuestions(t, fx); len(stored) != 0 {
				t.Fatalf("stored questions = %+v, want none after a refused Ask", stored)
			}
		})
	}
}

func TestStageQuestionsAnswerAsOwnerConversationStoresQuote(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedQuestionSession(t, fx)
	askQuestion(t, fx)
	const quote = "Keep the scout; it answers the question."
	got, err := answer(t, fx, questionSession, &v1.StageAnswerRequest{
		Question: 1, Answer: questionRecommendation,
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: quote},
	})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	want := &v1.StageQuestion{
		Id: 1, Text: questionText, Options: questionOptions(), Recommendation: questionRecommendation,
		Asked: stageResearchNow().Unix(), Answer: questionRecommendation, Answered: stageResearchNow().Unix(),
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: quote},
	}
	if !proto.Equal(got, want) {
		t.Fatalf("Answer = %v, want %v", got, want)
	}
	stored := storedQuestions(t, fx)
	if len(stored) != 1 || stored[0].Maker.Quote != quote || stored[0].Answerer != questionSession {
		t.Fatalf("stored questions = %+v, want the quote and the answering session %s", stored, questionSession)
	}
}

func TestStageQuestionsAnswerAsOwnerConversationRefusedWithoutQuote(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedQuestionSession(t, fx)
	askQuestion(t, fx)
	_, err := answer(t, fx, questionSession, &v1.StageAnswerRequest{
		Question: 1, Answer: questionRecommendation, Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation},
	})
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("Answer code = %v, want %v (err %v)", code, connect.CodeInvalidArgument, err)
	}
	if !strings.Contains(err.Error(), "quote") {
		t.Fatalf("Answer error = %v, want it to name the quote", err)
	}
	if stored := storedQuestions(t, fx); len(stored) != 1 || !stored[0].Answered.IsZero() {
		t.Fatalf("stored questions = %+v, want the question still open", stored)
	}
}

func TestStageQuestionsAnswerAsConversationStoresReasonAndDelegation(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedQuestionSession(t, fx)
	askQuestion(t, fx)
	const reason, delegation = "The owner left the scout to the conversation.", "Decide the scout yourself."
	got, err := answer(t, fx, questionSession, &v1.StageAnswerRequest{
		Question: 1, Answer: questionRecommendation,
		Maker: &v1.StageMaker{Kind: store.StageMakerConversation, Reason: reason, Delegation: delegation},
	})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if maker := got.GetMaker(); maker.GetKind() != store.StageMakerConversation || maker.GetReason() != reason ||
		maker.GetDelegation() != delegation || maker.GetQuote() != "" {
		t.Fatalf("maker = %v, want conversation with its reason and delegation only", maker)
	}
	stored := storedQuestions(t, fx)
	if len(stored) != 1 || stored[0].Maker.Reason != reason || stored[0].Maker.Delegation != delegation {
		t.Fatalf("stored questions = %+v, want the reason and the delegation", stored)
	}
}

func TestStageQuestionsAnswerAsConversationRefusedWithoutReasonOrDelegation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		maker *v1.StageMaker
		field string
	}{
		{
			name:  "no reason",
			maker: &v1.StageMaker{Kind: store.StageMakerConversation, Delegation: "Decide it."},
			field: "reason",
		},
		{
			name:  "no delegation",
			maker: &v1.StageMaker{Kind: store.StageMakerConversation, Reason: "The owner left it."},
			field: "delegation",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newStageResearchFixture(t)
			seedQuestionSession(t, fx)
			askQuestion(t, fx)
			_, err := answer(t, fx, questionSession, &v1.StageAnswerRequest{
				Question: 1, Answer: questionRecommendation, Maker: tc.maker,
			})
			if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
				t.Fatalf("Answer code = %v, want %v (err %v)", code, connect.CodeInvalidArgument, err)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("Answer error = %v, want it to name %s", err, tc.field)
			}
			if stored := storedQuestions(t, fx); len(stored) != 1 || !stored[0].Answered.IsZero() {
				t.Fatalf("stored questions = %+v, want the question still open", stored)
			}
		})
	}
}

func TestStageQuestionsAnswerAsOwnerServeOnlyFromNoSession(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedQuestionSession(t, fx)
	askQuestion(t, fx)

	_, err := answer(t, fx, questionSession, &v1.StageAnswerRequest{
		Question: 1, Answer: questionRecommendation, Maker: ownerServe(),
	})
	if code := connect.CodeOf(err); code != connect.CodePermissionDenied {
		t.Fatalf("owner_serve from a session: code = %v, want %v (err %v)", code, connect.CodePermissionDenied, err)
	}
	if stored := storedQuestions(t, fx); len(stored) != 1 || !stored[0].Answered.IsZero() {
		t.Fatalf("stored questions = %+v, want the question still open after the refusal", stored)
	}

	got, err := answer(t, fx, "", &v1.StageAnswerRequest{
		Question: 1, Answer: questionRecommendation, Maker: ownerServe(),
	})
	if err != nil {
		t.Fatalf("owner_serve with no session: %v", err)
	}
	if got.GetMaker().GetKind() != store.StageMakerOwnerServe || got.GetAnswer() != questionRecommendation {
		t.Fatalf("Answer = %v, want the owner's answer made through serve", got)
	}
	if stored := storedQuestions(t, fx); len(stored) != 1 || stored[0].Answerer != "" {
		t.Fatalf("stored questions = %+v, want no answering session for owner_serve", stored)
	}
}

func TestStageQuestionsAcceptStoresRecommendation(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	askQuestion(t, fx)
	got, err := answer(t, fx, "", &v1.StageAnswerRequest{Question: 1, Accept: true, Maker: ownerServe()})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if got.GetAnswer() != questionRecommendation || !got.GetAccepted() {
		t.Fatalf("Answer = %v, want the recommendation %q accepted", got, questionRecommendation)
	}
	stored := storedQuestions(t, fx)
	if len(stored) != 1 || stored[0].Answer != questionRecommendation || !stored[0].Accepted {
		t.Fatalf("stored questions = %+v, want the recommendation stored as accepted", stored)
	}
}

func TestStageQuestionsAnswerRefusesAcceptWithWordsAndBlankAnswer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  *v1.StageAnswerRequest
	}{
		{name: "accept with words", msg: &v1.StageAnswerRequest{Question: 1, Answer: "Drop", Accept: true}},
		{name: "no accept and no words", msg: &v1.StageAnswerRequest{Question: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newStageResearchFixture(t)
			askQuestion(t, fx)
			tc.msg.Maker = ownerServe()
			_, err := answer(t, fx, "", tc.msg)
			if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
				t.Fatalf("Answer code = %v, want %v (err %v)", code, connect.CodeInvalidArgument, err)
			}
			if stored := storedQuestions(t, fx); len(stored) != 1 || !stored[0].Answered.IsZero() {
				t.Fatalf("stored questions = %+v, want the question still open", stored)
			}
		})
	}
}

func TestStageQuestionsSecondAnswerIsFailedPrecondition(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	askQuestion(t, fx)
	if _, err := answer(t, fx, "", &v1.StageAnswerRequest{
		Question: 1, Answer: questionRecommendation, Maker: ownerServe(),
	}); err != nil {
		t.Fatalf("first Answer: %v", err)
	}
	_, err := answer(t, fx, "", &v1.StageAnswerRequest{
		Question: 1, Answer: questionOptions()[1], Maker: ownerServe(),
	})
	if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Fatalf("second Answer code = %v, want %v (err %v)", code, connect.CodeFailedPrecondition, err)
	}
	if stored := storedQuestions(t, fx); len(stored) != 1 || stored[0].Answer != questionRecommendation {
		t.Fatalf("stored questions = %+v, want the first answer kept", stored)
	}
}

func TestStageQuestionsAnswerRefusesUnknownKindAndUnknownQuestion(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	askQuestion(t, fx)

	_, err := answer(t, fx, "", &v1.StageAnswerRequest{
		Question: 1, Answer: questionRecommendation, Maker: &v1.StageMaker{Kind: "owner"},
	})
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("unknown kind code = %v, want %v (err %v)", code, connect.CodeInvalidArgument, err)
	}
	_, err = answer(t, fx, "", &v1.StageAnswerRequest{
		Question: 9, Answer: questionRecommendation, Maker: ownerServe(),
	})
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("unknown question code = %v, want %v (err %v)", code, connect.CodeNotFound, err)
	}
	if stored := storedQuestions(t, fx); len(stored) != 1 || !stored[0].Answered.IsZero() {
		t.Fatalf("stored questions = %+v, want the question still open", stored)
	}
}

func TestStageQuestionsGetStageReportsQuestionsOldestFirst(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	first := askQuestion(t, fx)
	second := askQuestion(t, fx)
	answered, err := answer(t, fx, "", &v1.StageAnswerRequest{
		Question: first.GetId(), Answer: questionRecommendation, Maker: ownerServe(),
	})
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}

	got, err := fx.service.GetStage(t.Context(), connect.NewRequest(&v1.GetStageRequest{Design: stageDesign}))
	if err != nil {
		t.Fatalf("GetStage: %v", err)
	}
	questions := got.Msg.GetStage().GetQuestions()
	if len(questions) != 2 || !proto.Equal(questions[0], answered) || !proto.Equal(questions[1], second) {
		t.Fatalf("stage questions = %v, want the answered %v then the open %v", questions, answered, second)
	}
}
