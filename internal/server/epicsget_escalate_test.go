package server

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

func TestEscalateAPI_GetCarriesEscalation(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour))
	stored.Turns = []*enginev1.EpicTurn{{Seq: 1, State: "succeeded", Ended: epicsAgo(3 * time.Hour)}}
	stored.Questions = []*enginev1.EpicQuestion{{
		Id: "Q1", Turn: 1, Text: "synthetic question", State: "open",
		Escalated: epicsAgo(time.Hour), EscalationReason: "synthetic reason",
		EscalationBy: "conversation", EscalationSession: "synthetic session",
	}}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/shared").GetQuestions()
	want := &orreryv1.EpicQuestion{
		Id: "Q1", Text: "synthetic question", Turn: 1, Asked: epicsAgo(3 * time.Hour),
		Escalated: epicsAgo(time.Hour), EscalationReason: "synthetic reason",
		EscalationBy: "conversation", EscalationSession: "synthetic session",
	}
	if len(got) != 1 {
		t.Fatalf("questions = %v, want 1 open question", got)
	}
	if !proto.Equal(got[0], want) {
		t.Errorf("question = %v, want %v", got[0], want)
	}
}

func TestEscalateAPI_OwnerEscalation(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour))
	stored.Turns = []*enginev1.EpicTurn{{Seq: 1, State: "succeeded", Ended: epicsAgo(3 * time.Hour)}}
	stored.Questions = []*enginev1.EpicQuestion{{
		Id: "Q1", Turn: 1, Text: "synthetic question", State: "open",
		Escalated: epicsAgo(time.Hour), EscalationReason: "synthetic owner reason", EscalationBy: "owner",
	}}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/shared").GetQuestions()
	want := &orreryv1.EpicQuestion{
		Id: "Q1", Text: "synthetic question", Turn: 1, Asked: epicsAgo(3 * time.Hour),
		Escalated: epicsAgo(time.Hour), EscalationReason: "synthetic owner reason", EscalationBy: "owner",
	}
	if len(got) != 1 {
		t.Fatalf("questions = %v, want 1 open question", got)
	}
	if !proto.Equal(got[0], want) {
		t.Errorf("question = %v, want %v", got[0], want)
	}
}

func TestEscalateAPI_UnescalatedQuestionCarriesNone(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour))
	stored.Turns = []*enginev1.EpicTurn{{Seq: 1, State: "succeeded", Ended: epicsAgo(3 * time.Hour)}}
	stored.Questions = []*enginev1.EpicQuestion{{
		Id: "Q1", Turn: 1, Text: "synthetic question", State: "open",
	}}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/shared").GetQuestions()
	if len(got) != 1 {
		t.Fatalf("questions = %v, want 1 open question", got)
	}
	if q := got[0]; q.GetEscalated() != 0 || q.GetEscalationReason() != "" || q.GetEscalationBy() != "" ||
		q.GetEscalationSession() != "" {
		t.Errorf("question escalation = escalated %d reason %q by %q session %q, want none",
			q.GetEscalated(), q.GetEscalationReason(), q.GetEscalationBy(), q.GetEscalationSession())
	}
}
