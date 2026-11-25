package server

import (
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

func TestEpicsHistoryDecisionByCarriesBySession(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf("repoa/shared", "accepted", historyAt(3600))
	stored.Decisions = []*enginev1.EpicDecision{
		{Seq: 1, Id: "d1", Text: "synthetic decision one", Time: historyAt(100), Question: "Q1",
			By: "conversation", Session: "synthetic-session"},
		{Seq: 2, Id: "d2", Text: "synthetic decision two", Time: historyAt(200), By: "owner"},
		{Seq: 3, Id: "d3", Text: "synthetic decision three", Time: historyAt(300), By: "orreryd"},
		{Seq: 4, Id: "d4", Text: "synthetic decision four", Time: historyAt(400)},
	}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := stepDataServe(t, &epicsFake{}, store, &evidenceRunsFake{})

	got := epicsHistory(t, client, "repoa/shared", "", 0)
	var decisions []*orreryv1.EpicDecision
	for _, entry := range got.GetEntries() {
		if entry.GetDecision() != nil {
			decisions = append(decisions, entry.GetDecision())
		}
	}
	want := []*orreryv1.EpicDecision{
		{Seq: 1, Id: "d1", Text: "synthetic decision one", Time: historyAt(100), Question: "Q1",
			By: "conversation", Session: "synthetic-session"},
		{Seq: 2, Id: "d2", Text: "synthetic decision two", Time: historyAt(200), By: "owner"},
		{Seq: 3, Id: "d3", Text: "synthetic decision three", Time: historyAt(300), By: "orreryd"},
		{Seq: 4, Id: "d4", Text: "synthetic decision four", Time: historyAt(400)},
	}
	if len(decisions) != len(want) {
		t.Fatalf("decision entries = %d, want %d", len(decisions), len(want))
	}
	for i := range want {
		if !proto.Equal(decisions[i], want[i]) {
			t.Errorf("decision entry %d = %v, want %v", i, decisions[i], want[i])
		}
	}
}

func TestEpicsAnswerDecisionByCarriesEngineReply(t *testing.T) {
	t.Parallel()
	fake := &epicCommandsFake{decision: &enginev1.EpicDecision{
		Seq: 4, Id: "A1", Text: "Ship it.", Time: 1_700_000_000, Question: "Q1",
		By: "conversation", Session: "synthetic-session",
	}}
	client := epicCommandServe(t, fake)
	reply, err := client.Answer(t.Context(), connect.NewRequest(&orreryv1.EpicsAnswerRequest{
		EpicId: syntheticEpicID, Id: "A1", Text: "Ship it.", Question: "Q1",
	}))
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	want := &orreryv1.EpicDecision{
		Seq: 4, Id: "A1", Text: "Ship it.", Time: 1_700_000_000, Question: "Q1",
		By: "conversation", Session: "synthetic-session",
	}
	if !proto.Equal(reply.Msg.GetDecision(), want) {
		t.Fatalf("Answer decision = %v, want %v", reply.Msg.GetDecision(), want)
	}
}
