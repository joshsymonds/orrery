package server_test

import (
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// The ids of the steps seedDesignStage gives designAnswerDesign: a research step and a discovery step whose counted
// return holds the finding S1.
const (
	designStageResearch  = "step-research"
	designStageDiscovery = "step-discovery"
)

// designStageResearchReport is the report of the research step, and designStageDiscoveryReturn the counted discovery
// return of the discovery step, which holds one finding, S1.
const (
	designStageResearchReport  = "The scout reads the store."
	designStageDiscoveryReturn = `{"status":"done","pass":"discovery","verdict":"REVISE",` +
		`"strongest_case":"the draft holds","strongest_alternative":"a queue",` +
		`"findings":[{"id":"S1","contract":"","impact":"the scout reads the store",` +
		`"evidence":"read the store","change":"name the store"}],` +
		`"evidence_and_coverage":"read the repository"}`
)

// designStageDiscoveryAt is the unix second the discovery step of designAnswerDesign starts at, one after the research
// step, which starts at designAnswerTime.
const designStageDiscoveryAt = designAnswerTime + 1

// seedDesignStage gives designAnswerDesign, which seedDesignQuestion creates, a research step and a discovery step,
// each started and ended done with its return kept, in st. Each step's design_steps row names the design.
func seedDesignStage(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := t.Context()
	for _, step := range []struct {
		id, agent, kind, subject, result string
		at                               int64
	}{
		{designStageResearch, "scout-agent", store.StepResearch, "Does the scout read the store?",
			designStageResearchReport, designAnswerTime},
		{designStageDiscovery, "steelman-agent", store.StepDiscovery, "Find the gaps in the scout.",
			designStageDiscoveryReturn, designStageDiscoveryAt},
	} {
		started := time.Unix(step.at, 0)
		if err := st.ApplyEngine(ctx, []store.EngineOp{
			store.StepAdd{
				ID: step.id, Agent: step.agent, Prompt: "work the packet", Workspace: "/synthetic/repo",
				ReadOnly: true, DoneValue: "done",
			},
			store.StepReturn{ID: step.id, Result: step.result},
			store.StepEnd{ID: step.id, End: "done", Status: "done", Reason: "returned"},
		}, started); err != nil {
			t.Fatalf("ApplyEngine %s: %v", step.id, err)
		}
		if err := st.AddDesignStep(ctx, store.DesignStep{
			Step: step.id, Design: designAnswerDesign, Kind: step.kind, Subject: step.subject,
			Repository: "/synthetic/repo", Revision: "abc1234", Created: started,
		}); err != nil {
			t.Fatalf("AddDesignStep %s: %v", step.id, err)
		}
	}
}

// wantDesignStage is the stage of designAnswerDesign once seedDesignStage has run and the owner has accepted Q1: the
// two steps oldest first, the question answered by the owner through serve, and the one finding, open, of the counted
// discovery pass.
func wantDesignStage() *orreryv1.DesignStage {
	return &orreryv1.DesignStage{
		Steps: []*orreryv1.DesignStep{
			{
				Step: designStageResearch, Kind: store.StepResearch, Subject: "Does the scout read the store?",
				Status: "done", Report: designStageResearchReport, Created: designAnswerTime,
			},
			{
				Step: designStageDiscovery, Kind: store.StepDiscovery, Subject: "Find the gaps in the scout.",
				Status: "done", Report: designStageDiscoveryReturn, Verdict: "REVISE", Counted: true,
				Created: designStageDiscoveryAt,
			},
		},
		Questions: []*orreryv1.DesignQuestion{{
			Id: 1, Text: "Should the scout stay?", Options: []string{"Keep the scout", "Drop the scout"},
			Recommendation: "Keep the scout", Asked: designAnswerTime, Answer: "Keep the scout", Accepted: true,
			Maker:    &orreryv1.RecordMaker{Kind: store.StageMakerOwnerServe},
			Answered: designAnswerTime,
		}},
		Ledger: []*orreryv1.DesignLedgerItem{{
			Id: "S1", Origin: "discovery", Impact: "the scout reads the store", Evidence: "read the store",
			Change: "name the store", Disposition: store.DispositionOpen,
		}},
		DiscoveryCounted: true,
	}
}

// downTransport is an HTTP transport whose every round trip fails with an error that is not a refused connection, so
// a stage engine behind it is down and its calls are not retried.
type downTransport struct{}

// RoundTrip closes the request body, as an HTTP transport must, and fails the round trip.
func (downTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	return nil, errors.New("stage engine is down")
}

// TestDesignsGetStage checks that Designs.Get answers the design's stage from the stage engine: the steps, questions
// and ledger the engine holds, mapped to their messages. A nil stage client leaves the stage unset, and a stage engine
// that is down fails Get.
func TestDesignsGetStage(t *testing.T) {
	t.Parallel()
	t.Run("maps the stage the stage engine holds", func(t *testing.T) {
		t.Parallel()
		st, path := openContractStore(t)
		seedDesignQuestion(t, st)
		seedDesignStage(t, st)
		client := designsClient(t, designsAnswerHandler(t, store.NewReader(path), serveStageEngine(t, st)))
		if _, err := client.Answer(t.Context(), connect.NewRequest(&orreryv1.DesignsAnswerRequest{
			Design: designAnswerDesign, Question: 1, Accept: true,
		})); err != nil {
			t.Fatalf("Answer: %v", err)
		}

		get, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.DesignsGetRequest{Id: designAnswerDesign}))
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got, want := get.Msg.GetStage(), wantDesignStage(); !proto.Equal(got, want) {
			t.Fatalf("Stage = %v, want %v", got, want)
		}
	})

	t.Run("nil stage client leaves stage unset", func(t *testing.T) {
		t.Parallel()
		st, path := openContractStore(t)
		seedDesignQuestion(t, st)
		seedDesignStage(t, st)
		client := designsClient(t, designsAnswerHandler(t, store.NewReader(path), nil))

		get, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.DesignsGetRequest{Id: designAnswerDesign}))
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got := get.Msg.GetDesign().GetId(); got != designAnswerDesign {
			t.Fatalf("Design id = %q, want %q", got, designAnswerDesign)
		}
		if got := get.Msg.GetStage(); got != nil {
			t.Fatalf("Stage = %v, want unset", got)
		}
	})

	t.Run("stage engine that is down fails Get", func(t *testing.T) {
		t.Parallel()
		st, path := openContractStore(t)
		seedDesignQuestion(t, st)
		down := enginev1connect.NewStageClient(&http.Client{Transport: downTransport{}}, "http://stage.invalid")
		client := designsClient(t, designsAnswerHandler(t, store.NewReader(path), down))

		_, err := client.Get(t.Context(), connect.NewRequest(&orreryv1.DesignsGetRequest{Id: designAnswerDesign}))
		if code := connect.CodeOf(err); code != connect.CodeUnavailable {
			t.Fatalf("Get code = %v, want %v (err %v)", code, connect.CodeUnavailable, err)
		}
	})
}

// TestDesignItemNamesQuestions checks that an engine_design 'asked' event of a question named Q1 reaches the
// conversation as a design item whose design names what 'asked' and the id Q1 it touched.
func TestDesignItemNamesQuestions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	activityDesign(t, s, "design-asked", eventEngine)
	at := int64(eventAt)
	if err := s.AppendDesignLog(ctx, store.DesignLogLine{
		Design: "design-asked", At: at + 1, Maker: store.LogMakerConversation, Session: eventEngine, What: "asked",
		IDs: []string{"Q1"}, Words: "Should the scout stay?",
	}); err != nil {
		t.Fatalf("AppendDesignLog question: %v", err)
	}

	clock := newBgClock(at + 5)
	stream := openSessionStream(t, bgWatchClient(t, path, s, clock, activityTimers), sessionReq(eventEngine, 0, 0))
	want := &orreryv1.ConversationItem{
		Kind: "design", Time: at + 1, Text: "asked\nShould the scout stay?",
		Design: &orreryv1.EngineDesign{Design: "design-asked", What: "asked", Ids: []string{"Q1"}},
	}
	items := stream.next(t).GetItems()
	if len(items) != 1 || !proto.Equal(items[0], want) {
		t.Fatalf("items = %v, want [%v]", items, want)
	}
}
