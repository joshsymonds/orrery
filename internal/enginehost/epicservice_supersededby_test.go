package enginehost

import (
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestGetEpicSupersededByServesTheReplacementNamedByASupersededClose closes an epic as superseded by an invented epic.
// GetEpic answers that epic as the stored superseded_by.
func TestGetEpicSupersededByServesTheReplacementNamedByASupersededClose(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startControlServer(t, st, closeSteps()))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	const successor = "synthetic-repo-0a1b2c3/epic-successor"
	if _, err := client.CloseEpic(t.Context(), connect.NewRequest(&v1.CloseEpicRequest{
		Id: epicServiceID, Kind: "superseded", By: successor,
	})); err != nil {
		t.Fatalf("CloseEpic: %v", err)
	}
	if got := getEpicOK(t, client).GetSupersededBy(); got != successor {
		t.Fatalf("superseded by = %q, want %q", got, successor)
	}
}

// TestGetEpicSupersededByIsEmptyForAnEpicThatIsNotClosed reads an accepted epic that no close has touched. Its
// superseded_by is empty.
func TestGetEpicSupersededByIsEmptyForAnEpicThatIsNotClosed(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startControlServer(t, st, closeSteps()))
	createEpicOK(t, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	if got := getEpicOK(t, client).GetSupersededBy(); got != "" {
		t.Fatalf("superseded by of an open epic = %q, want empty", got)
	}
}

// TestGetEpicSupersededByIsEmptyForAShippedOrAbandonedCloseWithoutBy closes two epics as shipped and as abandoned, each
// naming no epic. GetEpic answers an empty superseded_by for each.
func TestGetEpicSupersededByIsEmptyForAShippedOrAbandonedCloseWithoutBy(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startControlServer(t, st, closeSteps()))
	repo := stepGitTestRepo(t)
	for _, kind := range []string{"shipped", "abandoned"} {
		id := "synthetic-repo-0a1b2c3/epic-" + kind
		createEpicOK(t, client, fullEpicRequest(repo, id))
		if _, err := client.CloseEpic(t.Context(), connect.NewRequest(&v1.CloseEpicRequest{
			Id: id, Kind: kind,
		})); err != nil {
			t.Fatalf("CloseEpic %s: %v", kind, err)
		}
		got, err := client.GetEpic(t.Context(), connect.NewRequest(&v1.GetEpicRequest{Id: id}))
		if err != nil {
			t.Fatalf("GetEpic %s: %v", id, err)
		}
		if sup := got.Msg.GetEpic().GetSupersededBy(); sup != "" {
			t.Fatalf("superseded by of %s closed as %s = %q, want empty", id, kind, sup)
		}
	}
}
