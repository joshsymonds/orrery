package planner

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestOrchestratorParkTurnOfAPlannerParkNeedsAnActionOrQuestion checks the turn check of an epic that holds a park. A
// return with neither an action nor a question is refused with a line that names the actions field and the park reason
// when the park is a planner class, and the same return with one retry_epic action passes. An owner park does not hold
// the return to the rule, so the return with neither passes.
func TestOrchestratorParkTurnOfAPlannerParkNeedsAnActionOrQuestion(t *testing.T) {
	t.Parallel()
	const reason = "workflow node check failed: boom"
	bare := `{"status":"done","decisions":[],"tasks":[],"outline":"o","questions":[],"outcome":"wait"}`
	withRetry := `{"status":"done","decisions":[],"tasks":[],"outline":"o","questions":[],` +
		`"actions":[{"kind":"retry_epic","args":"",` +
		`"reason":"the park is settled"}],"outcome":"wait"}`
	nodePark := store.EpicRow{Parked: reason, ParkClass: store.ParkClassNode}
	ownerPark := store.EpicRow{Parked: "branch is gone", ParkClass: store.ParkClassBranch}

	want := []string{
		"actions: the epic is parked (" + reason + "); return an action for the park or a catastrophe question",
	}
	bareCheck := returnCheck(context.Background(), nodePark, store.Review{}, testResolver{}, nil, false)
	if got := bareCheck(json.RawMessage(bare)); !reflect.DeepEqual(got, want) {
		t.Errorf("ReturnCheck of a return with no action or question on a planner park = %q, want %q", got, want)
	}
	if got := returnCheck(
		context.Background(),
		nodePark,
		store.Review{},
		testResolver{},
		nil,
		false,
	)(
		json.RawMessage(withRetry),
	); len(
		got,
	) != 0 {
		t.Errorf("ReturnCheck of a return with a retry_epic action on a planner park = %q, want none", got)
	}
	if got := returnCheck(
		context.Background(),
		ownerPark,
		store.Review{},
		testResolver{},
		nil,
		false,
	)(
		json.RawMessage(bare),
	); len(
		got,
	) != 0 {
		t.Errorf("ReturnCheck of a return with no action or question on an owner park = %q, want none", got)
	}
}
