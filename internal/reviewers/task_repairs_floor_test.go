package reviewers

import (
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/gates"
)

// TestTaskRepairsFloorMustAsksWhetherTheTasksOwnTestsFail: the floor's must-assertion, as the reviewer is gated on it,
// is the one asking whether the task's own tests would fail if the behavior they cover were removed. No Requirement's
// capture is among the floor's musts, since the musts are exactly that one assertion.
func TestTaskRepairsFloorMustAsksWhetherTheTasksOwnTestsFail(t *testing.T) {
	t.Parallel()
	rs := mergedDefaults(t)
	i := slices.IndexFunc(rs, func(r Reviewer) bool { return r.Name == "floor" })
	if i < 0 {
		t.Fatalf("no floor reviewer in %v", names(rs))
	}
	var musts []string
	for _, a := range rs[i].Gate().Assertions {
		if a.Kind == gates.KindMust {
			musts = append(musts, a.Text)
		}
	}
	want := []string{"The task's own tests would fail if the behavior they cover were removed."}
	if !slices.Equal(musts, want) {
		t.Errorf("floor's must-assertions = %q, want %q", musts, want)
	}
}
