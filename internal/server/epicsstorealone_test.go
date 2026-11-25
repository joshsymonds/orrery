package server

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

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
