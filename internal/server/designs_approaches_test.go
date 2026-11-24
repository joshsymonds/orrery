package server_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// approachesServedDesign is the design the approach tests record on, whose conversation is designSession.
const approachesServedDesign = "design-approaches-served"

// TestRecordsApproachesServedByDesignsGet checks that Designs.Get serves a design's approaches oldest first with the
// one recommendation marked, and its newest choice with the maker it was made by, as the Stage RPCs stored them from
// the design's conversation session.
func TestRecordsApproachesServedByDesignsGet(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	ctx := t.Context()
	if err := st.CreateDesign(ctx, store.Design{
		ID: approachesServedDesign, Title: "Design approaches", Conversation: designSession,
		Created: time.Unix(designTime, 0),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	seedContractSession(t, st, designSession)
	stages := serveStageEngine(t, st)

	// Each call names the design's conversation in its session header, as the conversation makes it.
	recordApproach := func(name string, recommended bool) {
		t.Helper()
		req := connect.NewRequest(&enginev1.ApproachRequest{
			Design: approachesServedDesign, Name: name, Shape: "a scout beside the design", Cost: "one more step",
			Wins: "answers the question", Loses: "adds a step", Reconsider: "if the scout fails",
			Research: []string{"step-one"}, Recommended: recommended,
		})
		req.Header().Set(protocol.HeaderSession, designSession)
		if _, err := stages.Approach(ctx, req); err != nil {
			t.Fatalf("Approach %s: %v", name, err)
		}
	}
	chooseFromConversation := func(msg *enginev1.ChooseRequest) {
		t.Helper()
		req := connect.NewRequest(msg)
		req.Header().Set(protocol.HeaderSession, designSession)
		if _, err := stages.Choose(ctx, req); err != nil {
			t.Fatalf("Choose: %v", err)
		}
	}
	recordApproach("Keep the scout", false)
	recordApproach("Drop the scout", true)
	chooseFromConversation(&enginev1.ChooseRequest{
		Design: approachesServedDesign, Approach: 1, Words: "Keep the scout.",
		Maker: &enginev1.StageMaker{
			Kind: store.StageMakerOwnerConversation, Quote: "Keep the scout; it answers the question.",
		},
	})
	chooseFromConversation(&enginev1.ChooseRequest{
		Design: approachesServedDesign, Approach: 2, Words: "Drop the scout.",
		Maker: &enginev1.StageMaker{
			Kind: store.StageMakerConversation, Reason: "The owner delegated the pick.",
			Delegation: "Pick the one that answers in one step.",
		},
	})

	client := designsClient(t, contractsHandler(t, store.NewReader(path), nil))
	get, err := client.Get(ctx, connect.NewRequest(&orreryv1.DesignsGetRequest{Id: approachesServedDesign}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantApproaches := []*orreryv1.DesignApproach{
		{
			Seq: 1, Name: "Keep the scout", Shape: "a scout beside the design", Cost: "one more step",
			Wins: "answers the question", Loses: "adds a step", Reconsider: "if the scout fails",
			Research: []string{"step-one"}, Time: designAnswerTime,
		},
		{
			Seq: 2, Name: "Drop the scout", Shape: "a scout beside the design", Cost: "one more step",
			Wins: "answers the question", Loses: "adds a step", Reconsider: "if the scout fails",
			Research: []string{"step-one"}, Recommended: true, Time: designAnswerTime,
		},
	}
	got := get.Msg.GetApproaches()
	if len(got) != len(wantApproaches) {
		t.Fatalf("Get approaches = %v, want %v", got, wantApproaches)
	}
	for i := range wantApproaches {
		if !proto.Equal(got[i], wantApproaches[i]) {
			t.Fatalf("Get approach %d = %v, want %v", i, got[i], wantApproaches[i])
		}
	}
	wantChoice := &orreryv1.DesignChoice{
		Approach: 2, Words: "Drop the scout.", Time: designAnswerTime,
		Maker: &orreryv1.RecordMaker{
			Kind: store.StageMakerConversation, Reason: "The owner delegated the pick.",
			Delegation: "Pick the one that answers in one step.", Session: designSession,
		},
	}
	if !proto.Equal(get.Msg.GetChoice(), wantChoice) {
		t.Fatalf("Get choice = %v, want %v", get.Msg.GetChoice(), wantChoice)
	}
}
