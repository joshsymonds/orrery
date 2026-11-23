package server

import (
	"context"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// TestEpicsStoreAloneList answers exactly the epics the store holds, sorted by id, each with source orreryd: List reads
// no epic from anywhere else.
func TestEpicsStoreAloneList(t *testing.T) {
	t.Parallel()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/beta", "accepted", epicsAgo(time.Hour)),
		storedEpicOf("repob/alpha", "released", epicsAgo(2*time.Hour)),
	}}
	client := epicStoreServe(t, &epicsFake{}, store)

	got := epicsList(t, client)
	if ids, want := epicIDs(got), []string{"repoa/beta", "repob/alpha"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for _, epic := range got {
		if epic.GetSource() != epicSourceOrreryd {
			t.Errorf("%s source = %q, want %q", epic.GetId(), epic.GetSource(), epicSourceOrreryd)
		}
	}
}

// TestEpicsStoreAloneGetNotFound answers not_found for each id the store does not hold, though the store holds an epic.
func TestEpicsStoreAloneGetNotFound(t *testing.T) {
	t.Parallel()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/beta", "accepted", epicsAgo(time.Hour)),
	}}
	client := epicStoreServe(t, &epicsFake{}, store)

	for _, id := range []string{"repoa/missing", "repoa", ""} {
		_, err := client.Get(context.Background(), connect.NewRequest(&orreryv1.EpicsGetRequest{Id: id}))
		requireConnectError(t, err, connect.CodeNotFound, "epic not found")
	}
}

// TestEpicsStoreAloneHistoryNotFound answers not_found for each id the store does not hold, though a labeled step names
// one of them.
func TestEpicsStoreAloneHistoryNotFound(t *testing.T) {
	t.Parallel()
	store := &epicStoreFake{stored: []*enginev1.StoredEpic{
		storedEpicOf("repoa/beta", "accepted", epicsAgo(time.Hour)),
	}}
	steps := &epicsFake{steps: []*enginev1.Step{
		{Id: "missing-run", Epic: "repoa/missing", Started: epicsAgo(2 * time.Hour)},
	}}
	client := stepDataServe(t, steps, store, nil)

	for _, id := range []string{"repoa/missing", "repoa", ""} {
		_, err := client.History(context.Background(), connect.NewRequest(&orreryv1.EpicsHistoryRequest{Id: id}))
		requireConnectError(t, err, connect.CodeNotFound, "epic not found")
	}
}
