package enginehost

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

// TestEpicItemKindAndTestsRoundTrip creates an epic whose R1 names a test item with two test files and reads it back
// through GetEpic: the item comes back with its kind and its tests.
func TestEpicItemKindAndTestsRoundTrip(t *testing.T) {
	t.Parallel()
	st := epicTestStore(t)
	client := newEpicsClient(t, startEpicServer(t, st))
	req := fullEpicRequest(stepGitTestRepo(t), epicServiceID)
	item := epicTestItem()
	item.Kind = "test"
	item.Tests = []string{"internal/synthetic/first_test.go", "internal/synthetic/second_test.go"}
	req.GetContract().GetRequirements()[0].Item = item
	createEpicOK(t, client, req)

	requirements := getEpicOK(t, client).GetContract().GetRequirements()
	if len(requirements) == 0 || requirements[0].GetId() != "R1" {
		t.Fatalf("requirements = %v, want R1 first", requirements)
	}
	if got := requirements[0].GetItem(); !proto.Equal(got, item) {
		t.Fatalf("R1 item = %v, want %v with its kind and tests", got, item)
	}
}
