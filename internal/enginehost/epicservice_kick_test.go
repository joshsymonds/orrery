package enginehost

import (
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

// kickText is the owner's text the kick tests send.
const kickText = "Try the sqlite plan first"

func TestEpicKickReturnsTheEpicWithTheDecisionAndItsPendingTrigger(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	before := getEpicOK(t, client)
	seq := int64(len(before.GetDecisions()) + 1)
	id := fmt.Sprintf("owner-kick:%d", seq)

	kicked, err := client.KickEpic(ctx, connect.NewRequest(&v1.KickEpicRequest{Id: epicServiceID, Text: kickText}))
	if err != nil {
		t.Fatalf("KickEpic: %v", err)
	}
	epic := kicked.Msg.GetEpic()
	decisions := epic.GetDecisions()
	if last := decisions[len(decisions)-1]; last.GetSeq() != seq || last.GetId() != id ||
		last.GetText() != kickText {
		t.Fatalf("last decision = %v, want seq %d, id %s and the kick text", last, seq, id)
	}
	if got := getEpicOK(t, client); !proto.Equal(got, epic) {
		t.Fatalf("GetEpic = %v, want the kicked epic %v", got, epic)
	}
	read, err := client.GetEpic(ctx, connect.NewRequest(&v1.GetEpicRequest{Id: epicServiceID, History: true}))
	if err != nil {
		t.Fatalf("GetEpic with history: %v", err)
	}
	var kick *v1.EpicTrigger
	for _, trigger := range read.Msg.GetEpic().GetTriggers() {
		if trigger.GetId() == id {
			kick = trigger
		}
	}
	if kick == nil {
		t.Fatalf("triggers = %v, want a trigger %s", read.Msg.GetEpic().GetTriggers(), id)
	}
	if kick.GetState() != "pending" || kick.GetCause() != "owner kick: "+kickText {
		t.Fatalf("kick trigger = %v, want pending with the kick text as its cause", kick)
	}
}

func TestEpicKickRefusesEmptyIDAndBlankTextBeforeTheStore(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	want := getEpicOK(t, client)

	_, err := client.KickEpic(ctx, connect.NewRequest(&v1.KickEpicRequest{Text: kickText}))
	wantConnectError(t, err, connect.CodeInvalidArgument, "id: empty")
	_, err = client.KickEpic(ctx, connect.NewRequest(&v1.KickEpicRequest{Id: epicServiceID, Text: " \t "}))
	wantConnectError(t, err, connect.CodeInvalidArgument, "text: empty")
	if got := getEpicOK(t, client); !proto.Equal(got, want) {
		t.Fatalf("GetEpic after the refused kicks = %v, want unchanged %v", got, want)
	}
}

func TestEpicKickMapsEachStoreRefusalToItsCode(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	kick := func(id string) error {
		_, err := client.KickEpic(ctx, connect.NewRequest(&v1.KickEpicRequest{Id: id, Text: kickText}))
		return err
	}

	const missing = "synthetic-repo-0a1b2c3/no-such-epic"
	wantConnectError(t, kick(missing), connect.CodeNotFound, "id: no epic "+missing)

	if _, err := client.PauseEpic(ctx, connect.NewRequest(&v1.PauseEpicRequest{Id: epicServiceID})); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	wantConnectError(t, kick(epicServiceID), connect.CodeFailedPrecondition,
		"epic "+epicServiceID+" is paused; resume it before kicking it")

	if _, err := client.ResumeEpic(ctx, connect.NewRequest(&v1.ResumeEpicRequest{Id: epicServiceID})); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	turn, err := st.ClaimTurn(ctx, epicServiceID, time.Unix(turnStarted, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	free := store.TurnFree{Park: "needs a human"}
	if err = st.FreeTurn(ctx, epicServiceID, turn.Seq, free, time.Unix(turnFreed, 0)); err != nil {
		t.Fatalf("FreeTurn: %v", err)
	}
	wantConnectError(t, kick(epicServiceID), connect.CodeFailedPrecondition,
		"epic "+epicServiceID+" is parked; orrery epic retry "+epicServiceID+" ends the park before a kick")
}
