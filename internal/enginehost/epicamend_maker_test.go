package enginehost

import (
	"maps"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// amenderAfterCall makes a call that amends R1 of epicServiceID, as the caller whose headers are header, with amendTestItem,
// which keeps R1's conclusion and kind, so the amendment applies in the call. It returns the amender of the one amendment
// GetEpic reads back.
func amenderAfterCall(t *testing.T, client enginev1connect.EpicsClient, header http.Header) string {
	t.Helper()
	req := connect.NewRequest(&v1.AmendEvidenceRequest{
		Id: epicServiceID, Requirement: "R1", Item: amendTestItem(), Reason: "the test moved",
	})
	maps.Copy(req.Header(), header)
	if _, err := client.AmendEvidence(t.Context(), req); err != nil {
		t.Fatalf("AmendEvidence: %v", err)
	}
	amendments := getEpicOK(t, client).GetAmendments()
	if len(amendments) != 1 {
		t.Fatalf("amendments after the call = %d, want the one it stored", len(amendments))
	}
	return amendments[0].GetAmender()
}

// TestAmenderMakerOwnerCall checks that a call with no caller headers, which is the owner's, stores the owner as the
// amender of its amendment.
func TestAmenderMakerOwnerCall(t *testing.T) {
	t.Parallel()
	st, _ := openPlannerStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	createEpicOK(t, st, client, fullEpicRequest(stepGitTestRepo(t), epicServiceID))
	if got := amenderAfterCall(t, client, nil); got != store.AmenderOwner {
		t.Errorf("amender of a call with no caller headers = %q, want %q", got, store.AmenderOwner)
	}
}

// TestAmenderMakerSteererCall checks that a call from the conversation that steers the epic stores that conversation as
// the amender of its amendment.
func TestAmenderMakerSteererCall(t *testing.T) {
	t.Parallel()
	client := steerOnlyWorld(t)
	if got := amenderAfterCall(t, client, sessionHeader(steerOnlySteerer)); got != store.AmenderConversation {
		t.Errorf("amender of the steering conversation's call = %q, want %q", got, store.AmenderConversation)
	}
}

// TestAmenderMakerStepOutsideEpic checks that a call from a step session not labeled with the epic stores orreryd as the
// amender of its amendment.
func TestAmenderMakerStepOutsideEpic(t *testing.T) {
	t.Parallel()
	client := steerOnlyWorld(t)
	if got := amenderAfterCall(t, client, sessionHeader(steerOnlyStep)); got != store.AmenderOrreryd {
		t.Errorf("amender of a step outside the epic's call = %q, want %q", got, store.AmenderOrreryd)
	}
}
