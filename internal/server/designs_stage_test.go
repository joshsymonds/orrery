package server_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// designClosedID is the design whose stage holds a counted closure step, where designAnswerDesign holds a research step,
// a counted discovery step and a recorded closure skip.
const designClosedID = "design-closed"

// The ids of the steps the stage tests seed, and the returns they keep.
const (
	stageResearchStep  = "step-research"
	stageDiscoveryStep = "step-discovery"
	stageClosureStep   = "step-closure"
)

// stageDiscoveryReturn is a counted discovery return with two findings, S1 and S2, which concern R1 and P1.
const stageDiscoveryReturn = `{"status":"done","pass":"discovery","verdict":"REVISE",` +
	`"strongest_case":"the draft holds","strongest_alternative":"a queue",` +
	`"findings":[` +
	`{"id":"S1","contract":"R1","impact":"impact of S1","evidence":"evidence of S1","change":"change S1"},` +
	`{"id":"S2","contract":"P1","impact":"impact of S2","evidence":"evidence of S2","change":"change S2"}],` +
	`"evidence_and_coverage":"read the repository"}`

// stageClosureReturn is a counted closure return with no ledger entries and no concerns.
const stageClosureReturn = `{"status":"done","pass":"closure","verdict":"STILL_OPEN","ledger":[],"concerns":[],` +
	`"evidence_and_coverage":"read the revision"}`

// seedSecondQuestion asks a second question of designAnswerDesign through stages, which recommends its second option,
// and answers it as the owner in the conversation of designAnswerSession, accepting the recommendation.
func seedSecondQuestion(t *testing.T, stages enginev1connect.StageClient) {
	t.Helper()
	ctx := t.Context()
	asked, err := stages.Ask(ctx, connect.NewRequest(&enginev1.AskRequest{
		Design: designAnswerDesign, Text: "Which reader stays?", Options: []string{"Read the rows", "Read the store"},
		Recommendation: "Read the store",
	}))
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	answer := connect.NewRequest(&enginev1.StageAnswerRequest{
		Design: designAnswerDesign, Question: asked.Msg.GetQuestion().GetId(), Accept: true,
		Maker: &enginev1.StageMaker{
			Kind: store.StageMakerOwnerConversation, Quote: "Read the store, as the scout reads it.",
		},
	})
	answer.Header().Set(protocol.HeaderSession, designAnswerSession)
	if _, err := stages.Answer(ctx, answer); err != nil {
		t.Fatalf("Answer: %v", err)
	}
}

// seedDesignStep starts step as a stage step that ends done with result as its return, and stores its design step row
// with the time of designAnswerTime as its start.
func seedDesignStep(t *testing.T, st *store.Store, step store.DesignStep, result string) {
	t.Helper()
	ctx := t.Context()
	at := time.Unix(designAnswerTime, 0)
	if err := st.ApplyEngine(ctx, []store.EngineOp{
		store.StepAdd{
			ID: step.Step, Agent: "stage-agent", Prompt: "stage packet", Workspace: "/synthetic/repo",
			ReadOnly: true, DoneValue: "done",
		},
		store.StepReturn{ID: step.Step, Result: result},
		store.StepEnd{ID: step.Step, End: "done", Status: "done", Reason: "returned"},
	}, at); err != nil {
		t.Fatalf("ApplyEngine step %s: %v", step.Step, err)
	}
	step.Repository, step.Revision, step.Created = "/synthetic/repo", "abc1234", at
	if err := st.AddDesignStep(ctx, step); err != nil {
		t.Fatalf("AddDesignStep %s: %v", step.Step, err)
	}
}

// seedStageDesign gives designAnswerDesign a second question answered through serve's stages, a research step, a
// counted discovery step whose first finding the owner adopts through serve, and a closure skip.
func seedStageDesign(t *testing.T, st *store.Store, stages enginev1connect.StageClient) {
	t.Helper()
	seedDesignQuestion(t, st)
	seedContractSession(t, st, designAnswerSession)
	seedSecondQuestion(t, stages)
	seedDesignStep(t, st, store.DesignStep{
		Step: stageResearchStep, Design: designAnswerDesign, Kind: store.StepResearch,
		Subject: "Does the reader read the store?",
	}, "The scout's report.")
	seedDesignStep(t, st, store.DesignStep{
		Step: stageDiscoveryStep, Design: designAnswerDesign, Kind: store.StepDiscovery, Subject: "steelman packet",
	}, stageDiscoveryReturn)
	if _, err := stages.Dispose(t.Context(), connect.NewRequest(&enginev1.DisposeRequest{
		Design: designAnswerDesign, Item: "S1", Disposition: store.DispositionAdopted,
		Maker: &enginev1.StageMaker{Kind: store.StageMakerOwnerServe},
	})); err != nil {
		t.Fatalf("Dispose: %v", err)
	}
	if err := st.RecordClosureSkip(t.Context(), store.ClosureSkip{
		Design: designAnswerDesign, Reason: "The design is settled.",
		Maker: store.StageMaker{Kind: store.StageMakerOwnerServe}, At: time.Unix(designAnswerTime, 0),
	}); err != nil {
		t.Fatalf("RecordClosureSkip: %v", err)
	}
}

// seedClosedDesign creates designClosedID in st with a counted closure step and no question.
func seedClosedDesign(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.CreateDesign(t.Context(), store.Design{
		ID: designClosedID, Title: "Closed design", Conversation: designSession,
		Created: time.Unix(designAnswerTime, 0),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	seedDesignStep(t, st, store.DesignStep{
		Step: stageClosureStep, Design: designClosedID, Kind: store.StepClosure, Subject: "closure packet",
		Delta: "the revision",
	}, stageClosureReturn)
}

// engineStage returns the stage orreryd's GetStage reports for the design id through stages.
func engineStage(t *testing.T, stages enginev1connect.StageClient, id string) *enginev1.DesignStage {
	t.Helper()
	reply, err := stages.GetStage(t.Context(), connect.NewRequest(&enginev1.GetStageRequest{Design: id}))
	if err != nil {
		t.Fatalf("GetStage %s: %v", id, err)
	}
	return reply.Msg.GetStage()
}

// wantStageMessage converts stage, the stage of GetStage's reply, to the DesignStage Get answers, field for field.
func wantStageMessage(stage *enginev1.DesignStage) *orreryv1.DesignStage {
	want := &orreryv1.DesignStage{
		Frozen: stage.GetFrozen(), ClosureSkipped: stage.GetClosureSkipped(),
		DiscoveryCounted: stage.GetDiscoveryCounted(), ClosureCounted: stage.GetClosureCounted(),
	}
	for _, step := range stage.GetSteps() {
		want.Steps = append(want.Steps, &orreryv1.DesignStep{
			Step: step.GetStep(), Kind: step.GetKind(), Subject: step.GetSubject(), Repository: step.GetRepository(),
			Revision: step.GetRevision(), Delta: step.GetDelta(), Created: step.GetCreated(), Status: step.GetStatus(),
			Report: step.GetReport(), Verdict: step.GetVerdict(), Counted: step.GetCounted(),
		})
	}
	for _, question := range stage.GetQuestions() {
		want.Questions = append(want.Questions, &orreryv1.DesignQuestion{
			Id: question.GetId(), Contract: question.GetContract(), Text: question.GetText(),
			Options: question.GetOptions(), Recommendation: question.GetRecommendation(), Asked: question.GetAsked(),
			Answer: question.GetAnswer(), Accepted: question.GetAccepted(), Maker: wantRecordMaker(question.GetMaker()),
			Answered: question.GetAnswered(),
		})
	}
	for _, item := range stage.GetLedger() {
		want.Ledger = append(want.Ledger, &orreryv1.DesignLedgerItem{
			Id: item.GetId(), Contract: item.GetContract(), Origin: item.GetOrigin(), Impact: item.GetImpact(),
			Evidence: item.GetEvidence(), Change: item.GetChange(), Disposition: item.GetDisposition(),
			Reason: item.GetReason(), Boundary: item.GetBoundary(), Maker: wantRecordMaker(item.GetMaker()),
			ClosureWord: item.GetClosureWord(), ClosureNote: item.GetClosureNote(),
		})
	}
	return want
}

// wantRecordMaker converts maker, a stage maker, to the RecordMaker it becomes, or nil for none.
func wantRecordMaker(maker *enginev1.StageMaker) *orreryv1.RecordMaker {
	if maker == nil {
		return nil
	}
	return &orreryv1.RecordMaker{
		Kind: maker.GetKind(), Quote: maker.GetQuote(), Reason: maker.GetReason(), Delegation: maker.GetDelegation(),
	}
}

// stageGetFailure is a stage client whose GetStage answers an internal failure, and which makes no other call.
type stageGetFailure struct{ enginev1connect.StageClient }

// GetStage answers the internal failure.
func (stageGetFailure) GetStage(
	context.Context, *connect.Request[enginev1.GetStageRequest],
) (*connect.Response[enginev1.GetStageResponse], error) {
	return nil, connect.NewError(connect.CodeInternal, errors.New("store read failed"))
}

func TestDesignsGetStageCarriesQuestionsWithTheirAnswers(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedDesignQuestion(t, st)
	seedContractSession(t, st, designAnswerSession)
	stages := serveStageEngine(t, st)
	seedSecondQuestion(t, stages)
	client := designsClient(t, designsAnswerHandler(t, store.NewReader(path), stages))

	get, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.DesignsGetRequest{Id: designAnswerDesign}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := &orreryv1.DesignStage{Questions: []*orreryv1.DesignQuestion{
		{
			Id: 1, Text: "Should the scout stay?", Options: []string{"Keep the scout", "Drop the scout"},
			Recommendation: "Keep the scout", Asked: designAnswerTime,
		},
		{
			Id: 2, Text: "Which reader stays?", Options: []string{"Read the rows", "Read the store"},
			Recommendation: "Read the store", Asked: designAnswerTime, Answer: "Read the store", Accepted: true,
			Maker: &orreryv1.RecordMaker{
				Kind: store.StageMakerOwnerConversation, Quote: "Read the store, as the scout reads it.",
			},
			Answered: designAnswerTime,
		},
	}}
	if !proto.Equal(get.Msg.GetStage(), want) {
		t.Fatalf("Get stage = %v, want %v", get.Msg.GetStage(), want)
	}
}

func TestDesignsGetStageMatchesEngineGetStage(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	stages := serveStageEngine(t, st)
	seedStageDesign(t, st, stages)
	seedClosedDesign(t, st)
	client := designsClient(t, designsAnswerHandler(t, store.NewReader(path), stages))

	// The engine's replies hold what the seeding stored, so the comparison covers steps, questions, ledger items and
	// every flag.
	answer := engineStage(t, stages, designAnswerDesign)
	closed := engineStage(t, stages, designClosedID)
	if len(answer.GetSteps()) != 2 || len(answer.GetQuestions()) != 2 || len(answer.GetLedger()) != 2 ||
		!answer.GetClosureSkipped() || !answer.GetDiscoveryCounted() || !closed.GetClosureCounted() ||
		!closed.GetFrozen() {
		t.Fatalf("engine stages = %v and %v, want the seeded steps, questions, ledger and flags", answer, closed)
	}

	for _, design := range []struct {
		id     string
		engine *enginev1.DesignStage
	}{{designAnswerDesign, answer}, {designClosedID, closed}} {
		get, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.DesignsGetRequest{Id: design.id}))
		if err != nil {
			t.Fatalf("Get %s: %v", design.id, err)
		}
		want := wantStageMessage(design.engine)
		if !proto.Equal(get.Msg.GetStage(), want) {
			t.Fatalf("Get stage of %s = %v, want the engine's GetStage as %v", design.id, get.Msg.GetStage(), want)
		}
	}
}

func TestDesignsGetStageUnsetWithoutStageClient(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedDesignQuestion(t, st)
	client := designsClient(t, designsAnswerHandler(t, store.NewReader(path), nil))

	get, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.DesignsGetRequest{Id: designAnswerDesign}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stage := get.Msg.GetStage(); stage != nil {
		t.Fatalf("Get stage = %v, want unset with no stage client", stage)
	}
	if got := get.Msg.GetDesign().GetId(); got != designAnswerDesign {
		t.Fatalf("Get design id = %q, want %q", got, designAnswerDesign)
	}
}

func TestDesignsGetStageForwardFailureAnswersError(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	seedDesignQuestion(t, st)
	client := designsClient(t, designsAnswerHandler(t, store.NewReader(path), stageGetFailure{}))

	_, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.DesignsGetRequest{Id: designAnswerDesign}))
	if code := connect.CodeOf(err); code != connect.CodeInternal {
		t.Fatalf("Get code = %v, want %v (err %v)", code, connect.CodeInternal, err)
	}
}
