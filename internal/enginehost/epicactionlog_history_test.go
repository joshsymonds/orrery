package enginehost

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/store"
)

func TestEpicActionLogHistoryReturnsMovesInSeqOrder(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	if _, err := st.MoveEpic(
		t.Context(),
		epicServiceID,
		"conv-one",
		store.Maker{By: store.MakerOwner},
		time.Unix(1700001000, 0),
	); err != nil {
		t.Fatalf("MoveEpic owner: %v", err)
	}
	if _, err := st.MoveEpic(
		t.Context(),
		epicServiceID,
		"conv-two",
		store.Maker{By: store.MakerConversation, Session: "conv-one"},
		time.Unix(1700001100, 0),
	); err != nil {
		t.Fatalf("MoveEpic conversation: %v", err)
	}

	got := getEpicHistoryOK(t, client, true).GetActions()
	want := []*v1.EpicAction{
		{Seq: 1, Kind: store.ActionMove, Time: 1700001000, By: store.MakerOwner, From: fixedSteerID, To: "conv-one"},
		{
			Seq: 2, Kind: store.ActionMove, Time: 1700001100, By: store.MakerConversation, Session: "conv-one",
			From: "conv-one", To: "conv-two",
		},
	}
	if len(got) != len(want) {
		t.Fatalf("actions = %v, want %d", got, len(want))
	}
	for i, w := range want {
		if !proto.Equal(got[i], w) {
			t.Fatalf("action %d = %v, want %v", i, got[i], w)
		}
	}

	if without := getEpicHistoryOK(t, client, false).GetActions(); len(without) != 0 {
		t.Fatalf("actions without history = %v, want none", without)
	}
}
