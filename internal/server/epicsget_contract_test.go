package server

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

func TestEpicsGetServesOnlyOpenQuestions(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour))
	stored.Turns = []*enginev1.EpicTurn{
		{Seq: 1, State: "succeeded", Ended: epicsAgo(3 * time.Hour)},
		{Seq: 2, State: "succeeded", Ended: epicsAgo(90 * time.Minute)},
	}
	stored.Questions = []*enginev1.EpicQuestion{
		{Id: "Q1", Turn: 1, Text: "which store?", State: "open"},
		{Id: "Q2", Turn: 1, Text: "which name?", State: "answered"},
		{Id: "Q3", Turn: 2, Text: "how often to retry?", State: "open"},
		{Id: "Q4", Turn: 7, Text: "asked by no turn?", State: "open"},
	}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/shared").GetQuestions()
	want := []*orreryv1.EpicQuestion{
		{Id: "Q1", Text: "which store?", Turn: 1, Asked: epicsAgo(3 * time.Hour)},
		{Id: "Q3", Text: "how often to retry?", Turn: 2, Asked: epicsAgo(90 * time.Minute)},
		{Id: "Q4", Text: "asked by no turn?", Turn: 7, Asked: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("questions = %v, want %d open questions", got, len(want))
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("question %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestEpicsGetServesPausedAndParked(t *testing.T) {
	t.Parallel()
	paused := storedEpicOf("repoa/paused", "accepted", epicsAgo(time.Hour))
	paused.Paused = true
	parked := storedEpicOf("repoa/parked", "accepted", epicsAgo(time.Hour))
	parked.Parked = "synthetic park reason"
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{paused, parked}}
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, store)

	if got := epicsGet(t, client, "repoa/paused"); !got.GetPaused() || got.GetParked() != "" {
		t.Errorf("paused epic = paused %v parked %q, want paused true and parked empty",
			got.GetPaused(), got.GetParked())
	}
	if got := epicsGet(t, client, "repoa/parked"); got.GetPaused() || got.GetParked() != "synthetic park reason" {
		t.Errorf("parked epic = paused %v parked %q, want paused false and parked %q",
			got.GetPaused(), got.GetParked(), "synthetic park reason")
	}
}

func TestEpicsGetServesContractSectionsInOrder(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf("repoa/shared", "accepted", epicsAgo(2*time.Hour))
	stored.Contract = &enginev1.EpicContract{
		Id:       "repoa/shared",
		Title:    "synthetic title",
		Approved: "synthetic approval",
		Intent:   "synthetic intent",
		Failures: []*enginev1.EpicFailure{
			{Id: "F1", Text: "synthetic failure one", Rating: "severe", Response: "synthetic response one"},
			{Id: "F2", Text: "synthetic failure two", Rating: "limited", Response: "synthetic response two"},
		},
		Care: "serious",
		Additions: []*enginev1.EpicAddition{
			{Location: "synthetic place one", What: "synthetic add one", Why: "synthetic why one", Cost: "cost one"},
			{Location: "synthetic place two", What: "synthetic add two", Why: "synthetic why two", Cost: "cost two"},
		},
		Premises: []*enginev1.EpicPremise{
			{Id: "P1", Text: "synthetic premise one", IfWrong: "synthetic if wrong one"},
			{Id: "P2", Text: "synthetic premise two", IfWrong: "synthetic if wrong two"},
		},
		Exclusions: []string{"synthetic exclusion one", "synthetic exclusion two"},
		Approach:   "synthetic approach",
		Alternatives: []*enginev1.EpicAlternative{
			{Alternative: "synthetic alt one", WhyNot: "why not one", Reconsider: "reconsider one"},
			{Alternative: "synthetic alt two", WhyNot: "why not two", Reconsider: "reconsider two"},
		},
		ReleaseSteps: []*enginev1.EpicReleaseStep{
			{Step: "synthetic step one", Target: "synthetic target one", Undo: "synthetic undo one"},
			{Step: "synthetic step two", Target: "synthetic target two", Undo: "synthetic undo two"},
		},
		ReleaseThen: "synthetic release then",
		Checks:      "synthetic checks",
		Base:        "synthetic base commit",
	}
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{stored}}
	client := epicStoreServe(t, t.TempDir(), &epicsFake{}, store)

	want := &orreryv1.EpicContract{
		Title:    "synthetic title",
		Approved: "synthetic approval",
		Intent:   "synthetic intent",
		Failures: []*orreryv1.EpicFailure{
			{Id: "F1", Text: "synthetic failure one", Rating: "severe", Response: "synthetic response one"},
			{Id: "F2", Text: "synthetic failure two", Rating: "limited", Response: "synthetic response two"},
		},
		Care: "serious",
		Additions: []*orreryv1.EpicAddition{
			{Location: "synthetic place one", What: "synthetic add one", Why: "synthetic why one", Cost: "cost one"},
			{Location: "synthetic place two", What: "synthetic add two", Why: "synthetic why two", Cost: "cost two"},
		},
		Premises: []*orreryv1.EpicPremise{
			{Id: "P1", Text: "synthetic premise one", IfWrong: "synthetic if wrong one"},
			{Id: "P2", Text: "synthetic premise two", IfWrong: "synthetic if wrong two"},
		},
		Exclusions: []string{"synthetic exclusion one", "synthetic exclusion two"},
		Approach:   "synthetic approach",
		Alternatives: []*orreryv1.EpicAlternative{
			{Alternative: "synthetic alt one", WhyNot: "why not one", Reconsider: "reconsider one"},
			{Alternative: "synthetic alt two", WhyNot: "why not two", Reconsider: "reconsider two"},
		},
		ReleaseSteps: []*orreryv1.EpicReleaseStep{
			{Step: "synthetic step one", Target: "synthetic target one", Undo: "synthetic undo one"},
			{Step: "synthetic step two", Target: "synthetic target two", Undo: "synthetic undo two"},
		},
		ReleaseThen: "synthetic release then",
		Checks:      "synthetic checks",
		Base:        "synthetic base commit",
		Accepted:    stored.GetCreated(),
	}
	got := epicsGet(t, client, "repoa/shared").GetContract()
	if !proto.Equal(got, want) {
		t.Errorf("contract = %v, want %v", got, want)
	}
}

func TestEpicsGetRecordOnlyEpicHasNoEpicFields(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeHead(t, root, "repoa", "record", `{"outcome":"shipped"}`, epicsNow())
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/other", "accepted", epicsAgo(time.Hour)),
	}}
	client := epicStoreServe(t, root, &epicsFake{}, store)

	got := epicsGet(t, client, "repoa/record")
	if len(got.GetQuestions()) != 0 || got.GetContract() != nil || got.GetPaused() || got.GetParked() != "" {
		t.Errorf("record epic = questions %v contract %v paused %v parked %q, want none",
			got.GetQuestions(), got.GetContract(), got.GetPaused(), got.GetParked())
	}
}
