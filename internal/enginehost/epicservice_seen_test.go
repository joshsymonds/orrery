package enginehost

import (
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// storedSeen returns the seen mark the store holds for epicServiceID, in unix seconds. It fails the test when the mark is
// zero, because the tests call it only after SeeEpic.
func storedSeen(t *testing.T, st *store.Store) int64 {
	t.Helper()
	row, err := st.Epic(t.Context(), epicServiceID)
	if err != nil {
		t.Fatalf("Epic %s: %v", epicServiceID, err)
	}
	if row.Seen.IsZero() {
		t.Fatalf("store Seen of %s is zero, want the mark SeeEpic stored", epicServiceID)
	}
	return row.Seen.Unix()
}

// listedSummaryOK returns the summary of epicServiceID in the ListEpics reply client receives. It fails the test on error
// or when the list holds no summary of that id.
func listedSummaryOK(t *testing.T, client enginev1connect.EpicsClient) *v1.EpicSummary {
	t.Helper()
	listed, err := client.ListEpics(t.Context(), connect.NewRequest(&v1.ListEpicsRequest{}))
	if err != nil {
		t.Fatalf("ListEpics: %v", err)
	}
	for _, summary := range listed.Msg.GetEpics() {
		if summary.GetId() == epicServiceID {
			return summary
		}
	}
	t.Fatalf("ListEpics holds no summary of %s", epicServiceID)
	return nil
}

func TestEpicSeenIsZeroUntilSeeEpic(t *testing.T) {
	t.Parallel()
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	if got := listedSummaryOK(t, client).GetSeen(); got != 0 {
		t.Fatalf("ListEpics seen = %d before SeeEpic, want 0", got)
	}
	if got := getEpicOK(t, client).GetSeen(); got != 0 {
		t.Fatalf("GetEpic seen = %d before SeeEpic, want 0", got)
	}
}

func TestEpicSeenStoredBySeeEpicIsCarriedAcrossClients(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := epicTestStore(t)
	path := startEpicServer(t, st)
	owner := newEpicsClient(t, path)
	other := newEpicsClient(t, path)
	createEpicOK(t, owner, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	if _, err := owner.SeeEpic(ctx, connect.NewRequest(&v1.SeeEpicRequest{Id: epicServiceID})); err != nil {
		t.Fatalf("SeeEpic: %v", err)
	}
	want := storedSeen(t, st)
	if got := listedSummaryOK(t, other).GetSeen(); got != want {
		t.Fatalf("ListEpics seen = %d from another client, want the stored mark %d", got, want)
	}
	if got := getEpicOK(t, other).GetSeen(); got != want {
		t.Fatalf("GetEpic seen = %d from another client, want the stored mark %d", got, want)
	}
}

func TestEpicSeenSeeEpicRefusesEmptyAndUnknownID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	client := newEpicsClient(t, startEpicServer(t, epicTestStore(t)))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))

	_, err := client.SeeEpic(ctx, connect.NewRequest(&v1.SeeEpicRequest{}))
	wantConnectError(t, err, connect.CodeInvalidArgument, "id: empty")
	_, err = client.SeeEpic(ctx, connect.NewRequest(&v1.SeeEpicRequest{Id: "synthetic-repo-9999999/missing"}))
	wantConnectError(t, err, connect.CodeNotFound, "id: no epic synthetic-repo-9999999/missing")

	if got := getEpicOK(t, client).GetSeen(); got != 0 {
		t.Fatalf("GetEpic seen = %d after refused SeeEpic calls, want 0", got)
	}
}
