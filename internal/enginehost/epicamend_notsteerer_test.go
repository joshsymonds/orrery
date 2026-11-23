package enginehost

import (
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestAmendNotSteererRefusedBeforeAnyWrite has a conversation that does not steer the epic amend R1 with a new
// conclusion and kind. The call is refused FailedPrecondition, with the refusal naming session other-1 as not steering
// the epic and epic_steer, and the epic reads back with no amendment and with R1 still holding its item.
func TestAmendNotSteererRefusedBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	client := steerOnlyWorld(t)
	item := amendTestItem()
	item.Conclusion = "Another conclusion."
	item.Kind = "capture"
	item.Tests = nil

	_, err := client.AmendEvidence(t.Context(), steerOnlyRequest(sessionHeader(steerOnlyOther), &v1.AmendEvidenceRequest{
		Id: epicServiceID, Requirement: "R1", Item: item, Reason: "Synthetic reason.",
	}))
	wantConnectError(t, err, connect.CodeFailedPrecondition,
		"session "+steerOnlyOther+" does not steer epic "+epicServiceID+": session "+steerOnlySteerer+
			" steers it; epic_steer makes this conversation the steerer")

	epic := getEpicOK(t, client)
	if amendments := epic.GetAmendments(); len(amendments) != 0 {
		t.Errorf("amendments after the refused call = %v, want none", amendments)
	}
	reqs := epic.GetContract().GetRequirements()
	if len(reqs) == 0 || reqs[0].GetId() != "R1" {
		t.Fatalf("requirements after the refused call = %v, want R1 first", reqs)
	}
	if held := reqs[0].GetItem(); !proto.Equal(held, epicTestItem()) {
		t.Errorf("R1 item after the refused call = %v, want its original item %v", held, epicTestItem())
	}
}
