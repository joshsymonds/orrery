package kinds

import (
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestCostEndsPassedWhateverTheGateExits: a cost run whose check ended ends passed with the check's stdout, whether the
// check exited 3 or 0, since the cost is measured and never judged.
func TestCostEndsPassedWhateverTheGateExits(t *testing.T) {
	t.Parallel()
	for _, exit := range []int{3, 0} {
		check := store.NodeCheck{ID: workID("check"), State: store.NodeEnded, Exit: exit, Stdout: "measured\n"}
		got := Cost{}.Decide(nodeWith(testRev, true), Rows{Checks: []store.NodeCheck{check}})
		want := Action{Kind: ActEnd, Outcome: outcomePassed, Output: "measured\n"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("exit %d: Decide() = %+v, want %+v", exit, got, want)
		}
	}
}

// TestCostStartsItsCheck: a cost run with a revision and no check asks to start the check part.
func TestCostStartsItsCheck(t *testing.T) {
	t.Parallel()
	got := Cost{}.Decide(nodeWith(testRev, false), Rows{})
	want := Action{Kind: ActStart, IDs: []string{workID("check")}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decide() = %+v, want %+v", got, want)
	}
}
