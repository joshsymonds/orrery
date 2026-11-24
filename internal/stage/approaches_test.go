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

// approachConversation is the session the approach tests call the Stage RPCs from. It is the conversation
// newStageResearchFixture gives stageDesign, so the calls come from the design's own conversation.
const approachConversation = "conversation-one"

// approachKeep and approachDrop are the names of the two approaches the approach tests record.
const (
	approachKeep = "Keep the scout"
	approachDrop = "Drop the scout"
)

// approachReason is the reason the recommended approach gives.
const approachReason = "It answers the question in one step."

// fromSession sets the session header of req to session, and returns req.
func fromSession[T any](req *connect.Request[T], session string) *connect.Request[T] {
	req.Header().Set(protocol.HeaderSession, session)
	return req
}

// seedApproachConversation adds the sessions row approachConversation to the store of fx.
func seedApproachConversation(t *testing.T, fx stageResearchFixture) {
	t.Helper()
	add := store.EngineAdd{ID: approachConversation, Dir: "/synthetic/dir"}
	if err := fx.store.ApplyEngine(t.Context(), []store.EngineOp{add}, stageResearchNow()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// approachRequestOf returns the approach request the tests record on stageDesign, named name. A recommended approach
// carries the planner's reason, and the others none.
func approachRequestOf(name string, recommended bool) *v1.ApproachRequest {
	req := &v1.ApproachRequest{
		Design: stageDesign, Name: name, Shape: "a scout beside the design", Cost: "one more step",
		Wins: "answers the question", Loses: "adds a step", Reconsider: "if the scout fails",
		Research: []string{"step-one"},
	}
	if recommended {
		req.Recommended = true
		req.Reason = approachReason
	}
	return req
}

// approachWant returns the StageApproach the tests expect Approach to store and return for approachRequestOf, numbered
// seq.
func approachWant(seq int64, name string, recommended bool) *v1.StageApproach {
	req := approachRequestOf(name, recommended)
	return &v1.StageApproach{
		Seq: seq, Name: name, Shape: req.GetShape(), Cost: req.GetCost(), Wins: req.GetWins(),
		Loses: req.GetLoses(), Reconsider: req.GetReconsider(), Research: req.GetResearch(),
		Recommended: req.GetRecommended(), Reason: req.GetReason(), Session: approachConversation,
		At: stageResearchNow().Unix(),
	}
}

// recordApproach records the approach req on stageDesign from the conversation session, and returns the approach
// Approach replies with.
func recordApproach(t *testing.T, fx stageResearchFixture, req *v1.ApproachRequest) *v1.StageApproach {
	t.Helper()
	reply, err := fx.service.Approach(t.Context(), fromSession(connect.NewRequest(req), approachConversation))
	if err != nil {
		t.Fatalf("Approach %s: %v", req.GetName(), err)
	}
	return reply.Msg.GetApproach()
}

// chooseApproach makes the choice req on stageDesign from the conversation session, and returns the choice Choose
// replies with, or the error it returns.
func chooseApproach(t *testing.T, fx stageResearchFixture, req *v1.ChooseRequest) (*v1.StageChoice, error) {
	t.Helper()
	reply, err := fx.service.Choose(t.Context(), fromSession(connect.NewRequest(req), approachConversation))
	if err != nil {
		return nil, err
	}
	return reply.Msg.GetChoice(), nil
}

// ownerConversationChoice is the choice of approach seq the owner made in the conversation, with the owner's words and
// the quote they give.
func ownerConversationChoice(seq int64, words, quote string) *v1.ChooseRequest {
	return &v1.ChooseRequest{
		Design: stageDesign, Approach: seq, Words: words,
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: quote},
	}
}

// conversationChoice is the choice of approach seq the conversation made for the owner, with its reason and the
// delegation it rests on.
func conversationChoice(seq int64, words, reason, delegation string) *v1.ChooseRequest {
	return &v1.ChooseRequest{
		Design: stageDesign, Approach: seq, Words: words,
		Maker: &v1.StageMaker{Kind: store.StageMakerConversation, Reason: reason, Delegation: delegation},
	}
}

// TestRecordsApproachesStoresTwoWithOneRecommended checks that Approach stores each approach at the next seq from the
// caller's session, returns it as stored, and logs it as approach recorded with its seq.
func TestRecordsApproachesStoresTwoWithOneRecommended(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedApproachConversation(t, fx)

	first := recordApproach(t, fx, approachRequestOf(approachKeep, false))
	if want := approachWant(1, approachKeep, false); !proto.Equal(first, want) {
		t.Fatalf("first Approach = %v, want %v", first, want)
	}
	second := recordApproach(t, fx, approachRequestOf(approachDrop, true))
	if want := approachWant(2, approachDrop, true); !proto.Equal(second, want) {
		t.Fatalf("second Approach = %v, want %v", second, want)
	}

	at := stageResearchNow().Unix()
	wantLog := []store.DesignLogLine{
		{
			Design: stageDesign, Seq: 1, At: at, Maker: store.LogMakerConversation, Session: approachConversation,
			What: "approach recorded", IDs: []string{"1"}, Words: approachKeep,
		},
		{
			Design: stageDesign, Seq: 2, At: at, Maker: store.LogMakerConversation, Session: approachConversation,
			What: "approach recorded", IDs: []string{"2"}, Words: approachDrop,
		},
	}
	if got := storedLog(t, fx); !reflect.DeepEqual(got, wantLog) {
		t.Fatalf("design log = %+v, want %+v", got, wantLog)
	}
}

// TestRecordsApproachesChoiceByOwnerConversationStoresQuote checks that Choose stores a choice the owner made in the
// conversation with its quote, returns it, and logs it as approach chosen by the owner.
func TestRecordsApproachesChoiceByOwnerConversationStoresQuote(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedApproachConversation(t, fx)
	recordApproach(t, fx, approachRequestOf(approachKeep, false))
	recordApproach(t, fx, approachRequestOf(approachDrop, true))
	const quote = "Keep the scout; it answers the question."

	got, err := chooseApproach(t, fx, ownerConversationChoice(1, approachKeep, quote))
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	want := &v1.StageChoice{
		Approach: 1, Words: approachKeep, Session: approachConversation, At: stageResearchNow().Unix(),
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: quote},
	}
	if !proto.Equal(got, want) {
		t.Fatalf("Choose = %v, want %v", got, want)
	}
	wantLine := store.DesignLogLine{
		Design: stageDesign, Seq: 3, At: stageResearchNow().Unix(), Maker: store.LogMakerOwner,
		Session: approachConversation, What: "approach chosen", IDs: []string{"1"}, Words: approachKeep,
	}
	if log := storedLog(t, fx); len(log) != 3 || !reflect.DeepEqual(log[2], wantLine) {
		t.Fatalf("design log = %+v, want the choice as %+v last", log, wantLine)
	}
}

// TestRecordsApproachesChoiceByConversationNewestIsChoice checks that a choice the conversation makes for the owner,
// with its reason and delegation, is the newest choice GetStage reads, after an earlier choice by the owner, and that
// its log line carries the reason as its words.
func TestRecordsApproachesChoiceByConversationNewestIsChoice(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedApproachConversation(t, fx)
	recordApproach(t, fx, approachRequestOf(approachKeep, false))
	recordApproach(t, fx, approachRequestOf(approachDrop, true))
	if _, err := chooseApproach(t, fx, ownerConversationChoice(1, approachKeep, "Keep it.")); err != nil {
		t.Fatalf("Choose by owner_conversation: %v", err)
	}
	const reason, delegation = "The owner delegated the pick.", "Pick the one that answers in one step."

	got, err := chooseApproach(t, fx, conversationChoice(2, approachDrop, reason, delegation))
	if err != nil {
		t.Fatalf("Choose by conversation: %v", err)
	}
	want := &v1.StageChoice{
		Approach: 2, Words: approachDrop, Session: approachConversation, At: stageResearchNow().Unix(),
		Maker: &v1.StageMaker{Kind: store.StageMakerConversation, Reason: reason, Delegation: delegation},
	}
	if !proto.Equal(got, want) {
		t.Fatalf("Choose = %v, want %v", got, want)
	}
	if stage := ledgerStage(t, fx); !proto.Equal(stage.GetChoice(), want) {
		t.Fatalf("GetStage choice = %v, want the newest choice %v", stage.GetChoice(), want)
	}
	if log := storedLog(t, fx); len(log) != 4 || log[3].Words != reason || log[3].Maker != store.LogMakerConversation {
		t.Fatalf("design log = %+v, want the conversation's choice logged with its reason last", log)
	}
}

// TestRecordsApproachesGetStageReadsApproachesRecommendationAndChoice checks that GetStage returns the design's
// approaches oldest first with the one recommendation marked, and its choice with the maker it was made by.
func TestRecordsApproachesGetStageReadsApproachesRecommendationAndChoice(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedApproachConversation(t, fx)
	recordApproach(t, fx, approachRequestOf(approachKeep, false))
	recordApproach(t, fx, approachRequestOf(approachDrop, true))
	const quote = "Drop the scout; the one step answers it."
	if _, err := chooseApproach(t, fx, ownerConversationChoice(2, approachDrop, quote)); err != nil {
		t.Fatalf("Choose: %v", err)
	}

	stage := ledgerStage(t, fx)
	wantApproaches := []*v1.StageApproach{approachWant(1, approachKeep, false), approachWant(2, approachDrop, true)}
	if got := stage.GetApproaches(); len(got) != len(wantApproaches) ||
		!proto.Equal(got[0], wantApproaches[0]) || !proto.Equal(got[1], wantApproaches[1]) {
		t.Fatalf("GetStage approaches = %v, want %v", got, wantApproaches)
	}
	wantChoice := &v1.StageChoice{
		Approach: 2, Words: approachDrop, Session: approachConversation, At: stageResearchNow().Unix(),
		Maker: &v1.StageMaker{Kind: store.StageMakerOwnerConversation, Quote: quote},
	}
	if !proto.Equal(stage.GetChoice(), wantChoice) {
		t.Fatalf("GetStage choice = %v, want %v", stage.GetChoice(), wantChoice)
	}
}

// TestRecordsApproachesRefusesChoiceWithoutEvidence checks that a choice is refused as an invalid argument when the
// maker lacks the evidence its kind takes: an owner_conversation choice without its quote, and a conversation choice
// without its delegation. A refused choice stores nothing and logs nothing.
func TestRecordsApproachesRefusesChoiceWithoutEvidence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		req  *v1.ChooseRequest
	}{
		{name: "owner_conversation without its quote", req: ownerConversationChoice(1, approachKeep, "")},
		{
			name: "conversation without its delegation",
			req:  conversationChoice(1, approachKeep, "The owner delegated the pick.", ""),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newStageResearchFixture(t)
			seedApproachConversation(t, fx)
			recordApproach(t, fx, approachRequestOf(approachKeep, false))
			recordApproach(t, fx, approachRequestOf(approachDrop, true))

			_, err := chooseApproach(t, fx, tc.req)
			if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
				t.Fatalf("Choose code = %v, want %v (err %v)", code, connect.CodeInvalidArgument, err)
			}
			if stage := ledgerStage(t, fx); stage.GetChoice() != nil {
				t.Fatalf("GetStage choice = %v, want none after a refused Choose", stage.GetChoice())
			}
			if log := storedLog(t, fx); len(log) != 2 {
				t.Fatalf("design log = %+v, want only the two approaches after a refused Choose", log)
			}
		})
	}
}

// TestStageApproachesRefusesInvalidApproaches checks that Approach refuses a blank design or name as an invalid
// argument and an unknown design as not found, and that a refused approach stores nothing.
func TestStageApproachesRefusesInvalidApproaches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*v1.ApproachRequest)
		code   connect.Code
	}{
		{
			name:   "blank design",
			mutate: func(req *v1.ApproachRequest) { req.Design = " " },
			code:   connect.CodeInvalidArgument,
		},
		{
			name:   "blank name",
			mutate: func(req *v1.ApproachRequest) { req.Name = "" },
			code:   connect.CodeInvalidArgument,
		},
		{
			name:   "unknown design",
			mutate: func(req *v1.ApproachRequest) { req.Design = "no-such-design" },
			code:   connect.CodeNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newStageResearchFixture(t)
			seedApproachConversation(t, fx)
			req := approachRequestOf(approachKeep, false)
			tc.mutate(req)
			_, err := fx.service.Approach(t.Context(), fromSession(connect.NewRequest(req), approachConversation))
			if code := connect.CodeOf(err); code != tc.code {
				t.Fatalf("Approach code = %v, want %v (err %v)", code, tc.code, err)
			}
			stored, storeErr := fx.store.DesignApproaches(t.Context(), stageDesign)
			if storeErr != nil || len(stored) != 0 {
				t.Fatalf("stored approaches = %+v, %v, want none after a refused Approach", stored, storeErr)
			}
		})
	}
}

// TestStageApproachesRefusesChooseOfMissingApproach checks that Choose refuses an approach seq the design lacks as not
// found, and that a refused choice stores nothing and logs nothing.
func TestStageApproachesRefusesChooseOfMissingApproach(t *testing.T) {
	t.Parallel()
	fx := newStageResearchFixture(t)
	seedApproachConversation(t, fx)
	recordApproach(t, fx, approachRequestOf(approachKeep, false))

	_, err := chooseApproach(t, fx, ownerConversationChoice(2, approachDrop, "Drop the scout."))
	if code := connect.CodeOf(err); code != connect.CodeNotFound {
		t.Fatalf("Choose code = %v, want %v (err %v)", code, connect.CodeNotFound, err)
	}
	if stage := ledgerStage(t, fx); stage.GetChoice() != nil || len(stage.GetLog()) != 1 {
		t.Fatalf("GetStage choice = %v, log = %v, want no choice and only the approach's line", stage.GetChoice(),
			stage.GetLog())
	}
}
