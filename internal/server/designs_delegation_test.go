package server_test

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/stage"
	"github.com/joshsymonds/orrery/internal/store"
)

// delegationServedDesign is the design the delegation test delegates on, whose conversation is designSession.
const delegationServedDesign = "design-delegation-served"

// delegationTime is the unix seconds the delegation test creates its design at, and the time of its first record.
const delegationTime int64 = 1_700_000_400

// delegationMessage is the owner's message the delegation test delegates with, which Designs.Get serves whole.
const delegationMessage = "Decide the scout yourself.\n\nKeep my words as I wrote them."

// delegationFromConversation names the design's conversation in the session header of req, as the conversation makes
// its calls, and returns req.
func delegationFromConversation[T any](req *connect.Request[T]) *connect.Request[T] {
	req.Header().Set(protocol.HeaderSession, designSession)
	return req
}

// TestRecordsDelegationServedByDesignsGet checks that Designs.Get serves a design's delegation with its message whole,
// and the decision the conversation made after it, as the Stage RPCs stored them from the design's conversation
// session. A decision the conversation made before the delegation, and an answer the owner gave in the conversation
// after it, are left out.
func TestRecordsDelegationServedByDesignsGet(t *testing.T) {
	t.Parallel()
	st, path := openContractStore(t)
	ctx := t.Context()
	if err := st.CreateDesign(ctx, store.Design{
		ID: delegationServedDesign, Title: "Design delegation", Conversation: designSession,
		Created: time.Unix(delegationTime, 0),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	seedContractSession(t, st, designSession)
	now := delegationTime
	stages := stage.NewService(stage.Config{
		Store: st,
		Now:   func() time.Time { return time.Unix(now, 0) },
	})

	for range 3 {
		if _, err := stages.Ask(ctx, delegationFromConversation(connect.NewRequest(&enginev1.AskRequest{
			Design: delegationServedDesign, Text: "Should the scout stay?",
			Options: []string{"Keep the scout", "Drop the scout"}, Recommendation: "Keep the scout",
		}))); err != nil {
			t.Fatalf("Ask: %v", err)
		}
	}
	// Question 1 is answered by the conversation before the delegation, so it is left out.
	now = delegationTime + 5
	if _, err := stages.Answer(ctx, delegationFromConversation(connect.NewRequest(&enginev1.StageAnswerRequest{
		Design:   delegationServedDesign,
		Question: 1,
		Answer:   "Keep the scout",
		Maker: &enginev1.StageMaker{
			Kind:       store.StageMakerConversation,
			Reason:     "Decided before.",
			Delegation: "Decide.",
		},
	}))); err != nil {
		t.Fatalf("Answer Q1 by conversation: %v", err)
	}
	now = delegationTime + 10
	if _, err := stages.Delegate(ctx, delegationFromConversation(connect.NewRequest(&enginev1.DelegateRequest{
		Design: delegationServedDesign, Kinds: []string{store.DecisionAnswer}, Message: delegationMessage,
	}))); err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	// Question 2 is answered by the owner in the conversation after the delegation, so it is left out.
	now = delegationTime + 20
	if _, err := stages.Answer(ctx, delegationFromConversation(connect.NewRequest(&enginev1.StageAnswerRequest{
		Design: delegationServedDesign, Question: 2, Answer: "Drop the scout",
		Maker: &enginev1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: "Drop the scout."},
	}))); err != nil {
		t.Fatalf("Answer Q2 by owner_conversation: %v", err)
	}
	// Question 3 is answered by the conversation after the delegation, so it is listed under it.
	now = delegationTime + 30
	if _, err := stages.Answer(ctx, delegationFromConversation(connect.NewRequest(&enginev1.StageAnswerRequest{
		Design:   delegationServedDesign,
		Question: 3,
		Answer:   "Keep the scout",
		Maker: &enginev1.StageMaker{
			Kind:       store.StageMakerConversation,
			Reason:     "Decided after.",
			Delegation: "Decide.",
		},
	}))); err != nil {
		t.Fatalf("Answer Q3 by conversation: %v", err)
	}

	client := designsClient(t, contractsHandler(t, store.NewReader(path), nil))
	get, err := client.Get(ctx, connect.NewRequest(&orreryv1.DesignsGetRequest{Id: delegationServedDesign}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := &orreryv1.DesignDelegation{
		Seq: 1, Kinds: []string{store.DecisionAnswer}, Message: delegationMessage, Session: designSession,
		Time: delegationTime + 10,
		Decisions: []*orreryv1.DesignDecision{{
			Kind: store.DecisionAnswer, Ref: "Q3", Text: "Keep the scout", Reason: "Decided after.",
			Time: delegationTime + 30,
		}},
	}
	got := get.Msg.GetDelegations()
	if len(got) != 1 || !proto.Equal(got[0], want) {
		t.Fatalf("Get delegations = %v, want [%v]", got, want)
	}
}
