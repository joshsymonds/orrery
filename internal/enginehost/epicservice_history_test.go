package enginehost

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// getEpicHistoryOK returns the stored epic with epicServiceID through client, asking for its history when history is set.
// It fails the test on error.
func getEpicHistoryOK(t *testing.T, client enginev1connect.EpicsClient, history bool) *v1.StoredEpic {
	t.Helper()
	got, err := client.GetEpic(t.Context(), connect.NewRequest(&v1.GetEpicRequest{Id: epicServiceID, History: history}))
	if err != nil {
		t.Fatalf("GetEpic %s history=%t: %v", epicServiceID, history, err)
	}
	return got.Msg.GetEpic()
}

func TestEpicGetAnswersSummaryAndHistory(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	created := createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	if created.GetSummary() != "" {
		t.Fatalf("created summary = %q, want empty until written", created.GetSummary())
	}
	if err := st.SetEpicSummary(t.Context(), epicServiceID, "Synthetic summary."); err != nil {
		t.Fatalf("SetEpicSummary: %v", err)
	}
	if err := st.ParkEpic(
		t.Context(),
		epicServiceID,
		store.ParkClassBranch,
		"Synthetic first tell.",
		time.Unix(1700000800, 0),
	); err != nil {
		t.Fatalf("ParkEpic first: %v", err)
	}
	if err := st.ParkEpic(
		t.Context(),
		epicServiceID,
		store.ParkClassBranch,
		"Synthetic second tell.",
		time.Unix(1700000900, 0),
	); err != nil {
		t.Fatalf("ParkEpic second: %v", err)
	}

	got := getEpicHistoryOK(t, client, false)
	if got.GetSummary() != "Synthetic summary." {
		t.Fatalf("summary = %q, want %q", got.GetSummary(), "Synthetic summary.")
	}
	if len(got.GetTriggers()) != 0 {
		t.Fatalf("triggers without history = %v, want none", got.GetTriggers())
	}
	if len(got.GetTells()) != 0 {
		t.Fatalf("tells without history = %v, want none", got.GetTells())
	}

	got = getEpicHistoryOK(t, client, true)
	wantTriggers := []*v1.EpicTrigger{
		{Id: "created", Cause: "epic created", State: "pending", Turn: 0, Created: created.GetCreated()},
	}
	if len(got.GetTriggers()) != len(wantTriggers) {
		t.Fatalf("triggers = %v, want %d", got.GetTriggers(), len(wantTriggers))
	}
	for i, want := range wantTriggers {
		if !proto.Equal(got.GetTriggers()[i], want) {
			t.Fatalf("trigger %d = %v, want %v", i, got.GetTriggers()[i], want)
		}
	}
	wantTells := []*v1.EpicTell{
		{Seq: 1, Text: "Synthetic first tell.", At: 1700000800, Told: false},
		{Seq: 2, Text: "Synthetic second tell.", At: 1700000900, Told: false},
	}
	if len(got.GetTells()) != len(wantTells) {
		t.Fatalf("tells = %v, want %d", got.GetTells(), len(wantTells))
	}
	for i, want := range wantTells {
		if !proto.Equal(got.GetTells()[i], want) {
			t.Fatalf("tell %d = %v, want %v", i, got.GetTells()[i], want)
		}
	}
}
