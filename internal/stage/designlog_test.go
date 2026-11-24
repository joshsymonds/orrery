package stage

import (
	"reflect"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// askFromSession asks the question of the tests on stageDesign from the session header session.
func askFromSession(t *testing.T, fx stageResearchFixture, session string) {
	t.Helper()
	req := connect.NewRequest(&v1.AskRequest{
		Design: stageDesign, Text: questionText, Options: questionOptions(), Recommendation: questionRecommendation,
	})
	req.Header().Set(protocol.HeaderSession, session)
	if _, err := fx.service.Ask(t.Context(), req); err != nil {
		t.Fatalf("Ask from %s: %v", session, err)
	}
}

// storedLog returns the design log the store of fx holds for stageDesign, oldest first.
func storedLog(t *testing.T, fx stageResearchFixture) []store.DesignLogLine {
	t.Helper()
	lines, err := fx.store.DesignLog(t.Context(), stageDesign)
	if err != nil {
		t.Fatalf("DesignLog: %v", err)
	}
	return lines
}

// seedAskAndOwnerServeAnswer asks the question of the tests from questionSession, and then answers it through serve as
// the owner, with no session.
func seedAskAndOwnerServeAnswer(t *testing.T, fx stageResearchFixture) {
	t.Helper()
	seedQuestionSession(t, fx)
	askFromSession(t, fx, questionSession)
	if _, err := answer(t, fx, "", &v1.StageAnswerRequest{
		Question: 1, Answer: "Drop the scout", Maker: ownerServe(),
	}); err != nil {
		t.Fatalf("Answer: %v", err)
	}
}

func TestDesignLogRecordsLogStageAskThenOwnerServeAnswer(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedAskAndOwnerServeAnswer(t, fx)

	at := stageResearchNow().Unix()
	want := []store.DesignLogLine{
		{
			Design: stageDesign, Seq: 1, At: at, Maker: store.LogMakerConversation, Session: questionSession,
			What: "asked", IDs: []string{"Q1"}, Words: questionText,
		},
		{
			Design: stageDesign, Seq: 2, At: at, Maker: store.LogMakerOwner, Session: "", What: "answered",
			IDs: []string{"Q1"}, Words: "Drop the scout",
		},
	}
	if got := storedLog(t, fx); !reflect.DeepEqual(got, want) {
		t.Fatalf("design log = %+v, want %+v", got, want)
	}
}

func TestDesignLogRecordsLogStageGetStageReturnsLines(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedAskAndOwnerServeAnswer(t, fx)

	at := stageResearchNow().Unix()
	want := []*v1.StageLogLine{
		{
			Seq: 1, At: at, Maker: &v1.StageMaker{Kind: store.LogMakerConversation}, Session: questionSession,
			What: "asked", Ids: []string{"Q1"}, Words: questionText,
		},
		{
			Seq: 2, At: at, Maker: &v1.StageMaker{Kind: store.LogMakerOwner}, What: "answered", Ids: []string{"Q1"},
			Words: "Drop the scout",
		},
	}
	got := ledgerStage(t, fx).GetLog()
	if len(got) != len(want) {
		t.Fatalf("GetStage log = %v, want %d lines", got, len(want))
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Fatalf("GetStage log line %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestDesignLogRecordsLogStageResearchAndReproduceStarts(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedQuestionSession(t, fx)
	const question, command = "Does Foo call Bar?", "go test ./internal/store/"

	research := connect.NewRequest(&v1.ResearchRequest{
		Design: stageDesign, Repository: "/repo", Revision: "abc1234", Question: question,
	})
	research.Header().Set(protocol.HeaderSession, questionSession)
	researched, err := fx.service.Research(t.Context(), research)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	reproduced, err := fx.service.Reproduce(t.Context(), connect.NewRequest(&v1.ReproduceRequest{
		Design: stageDesign, Repository: "/repo", Revision: "abc1234", Command: command,
	}))
	if err != nil {
		t.Fatalf("Reproduce: %v", err)
	}

	at := stageResearchNow().Unix()
	want := []store.DesignLogLine{
		{
			Design: stageDesign, Seq: 1, At: at, Maker: store.LogMakerConversation, Session: questionSession,
			What: "research started", IDs: []string{researched.Msg.GetStep().GetStep()}, Words: question,
		},
		{
			Design: stageDesign, Seq: 2, At: at, Maker: store.LogMakerOwner, Session: "",
			What: "reproduce started", IDs: []string{reproduced.Msg.GetStep().GetStep()}, Words: command,
		},
	}
	if got := storedLog(t, fx); !reflect.DeepEqual(got, want) {
		t.Fatalf("design log = %+v, want %+v", got, want)
	}
}

func TestDesignLogRecordsLogStageSteelmanStartAndClosureSkip(t *testing.T) {
	t.Parallel()
	fx := newStageSteelmanFixture(t)
	proposeSteelmanContract(t, fx, steelmanFixtureContract(steelmanIDA, "Contract A"))

	started, err := steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{})
	if err != nil {
		t.Fatalf("Steelman discovery: %v", err)
	}
	discovery := started.GetStep().GetStep()
	returnSteelmanStep(t, fx, discovery, ledgerDiscoveryReturn(t))
	if _, err = steelmanPass(t, fx, stageDesign, &v1.SteelmanRequest{
		SkipClosure: true, Reason: "a rename", Maker: &v1.StageMaker{Kind: store.StageMakerOwnerServe},
	}); err != nil {
		t.Fatalf("Steelman skip: %v", err)
	}

	at := stageResearchNow().Unix()
	want := []store.DesignLogLine{
		{
			Design: stageDesign, Seq: 1, At: at, Maker: store.LogMakerOwner, Session: "", What: "steelman started",
			IDs: []string{discovery},
		},
		{
			Design: stageDesign, Seq: 2, At: at, Maker: store.LogMakerOwner, Session: "", What: "closure skipped",
			Words: "a rename",
		},
	}
	if got := storedLog(t, fx); !reflect.DeepEqual(got, want) {
		t.Fatalf("design log = %+v, want %+v", got, want)
	}
}

func TestDesignLogRecordsLogStageDisposeLines(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedQuestionSession(t, fx)
	seedLedgerStep(t, fx, ledgerDiscovery, store.StepDiscovery, ledgerDiscoveryReturn(t))

	dispositions := []struct {
		session string
		msg     *v1.DisposeRequest
	}{
		{"", &v1.DisposeRequest{Item: "S1", Disposition: store.DispositionAdopted, Maker: ownerServe()}},
		{questionSession, &v1.DisposeRequest{
			Item: "S2", Disposition: store.DispositionRejected, Reason: "outside the owner's words",
			Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: "keep R2 out"},
		}},
		{questionSession, &v1.DisposeRequest{
			Item: "S3", Disposition: store.DispositionDeferred, Boundary: "after the v2 release",
			Maker: &v1.StageMaker{
				Kind: store.StageMakerConversation, Reason: "the owner left the call to me",
				Delegation: "decide the boundary",
			},
		}},
		{"", &v1.DisposeRequest{
			Item: "S4", Disposition: store.DispositionDeferred, Boundary: "after the v3 release",
			Maker: ownerServe(),
		}},
	}
	for _, disposition := range dispositions {
		if _, err := disposeLedger(t, fx, disposition.session, disposition.msg); err != nil {
			t.Fatalf("Dispose of %s: %v", disposition.msg.GetItem(), err)
		}
	}

	at := stageResearchNow().Unix()
	want := []store.DesignLogLine{
		{
			Design: stageDesign, Seq: 1, At: at, Maker: store.LogMakerOwner, Session: "", What: "disposed",
			IDs: []string{"S1"},
		},
		{
			Design: stageDesign, Seq: 2, At: at, Maker: store.LogMakerOwner, Session: questionSession,
			What: "disposed", IDs: []string{"S2"}, Words: "outside the owner's words",
		},
		{
			Design: stageDesign, Seq: 3, At: at, Maker: store.LogMakerConversation, Session: questionSession,
			What: "disposed", IDs: []string{"S3"}, Words: "the owner left the call to me",
		},
		{
			Design: stageDesign, Seq: 4, At: at, Maker: store.LogMakerOwner, Session: "", What: "disposed",
			IDs: []string{"S4"}, Words: "after the v3 release",
		},
	}
	if got := storedLog(t, fx); !reflect.DeepEqual(got, want) {
		t.Fatalf("design log = %+v, want %+v", got, want)
	}
}
