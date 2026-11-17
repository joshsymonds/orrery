package server

import (
	"testing"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

func TestEpicKickForwardsTheIDAndTheTextInOneCall(t *testing.T) {
	t.Parallel()
	fake := &epicCommandsFake{}
	client := epicCommandServe(t, fake)

	_, err := client.Kick(t.Context(), connect.NewRequest(&orreryv1.EpicsKickRequest{
		Id: syntheticEpicID, Text: "Try the sqlite plan first.",
	}))
	if err != nil {
		t.Fatalf("Kick: %v", err)
	}
	assertForwarded(t, fake.recorded(), &enginev1.KickEpicRequest{
		Id: syntheticEpicID, Text: "Try the sqlite plan first.",
	})
}
