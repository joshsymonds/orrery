package store_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestBaseCostReturnsEndedCostCheckBaseCost: BaseCost returns the ended cost-node check of the epic with its kept
// output, and leaves out a check on another path and a cost-node check that exited EnvironmentExit.
func TestBaseCostReturnsEndedCostCheckBaseCost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openCheckStore(t)
	const cost, other, environment = "id-base-cost", "id-base-other", "id-base-environment"
	const stdout = "test-budget: cost internal/store 190.0\n"
	for _, added := range []struct {
		id, path string
		end      store.NodeCheckEnd
	}{
		{cost, "cost", store.NodeCheckEnd{Stdout: stdout}},
		{other, "gate-tests", store.NodeCheckEnd{Stdout: "test-budget: internal/store 212 cpu-s\n"}},
		{environment, "cost", store.NodeCheckEnd{Exit: store.EnvironmentExit, Stdout: "test-budget: cost x 1.0\n"}},
	} {
		check := runningCheck(added.id)
		check.Path = added.path
		if err := s.AddNodeCheck(ctx, check, checkTime(0)); err != nil {
			t.Fatalf("AddNodeCheck %s: %v", added.id, err)
		}
		if err := s.EndNodeCheck(ctx, added.id, added.end, checkTime(1)); err != nil {
			t.Fatalf("EndNodeCheck %s: %v", added.id, err)
		}
	}

	got, err := s.BaseCost(ctx, checkEpic)
	if err != nil {
		t.Fatalf("BaseCost: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, check := range got {
		ids = append(ids, check.ID)
	}
	if want := []string{cost}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("BaseCost checks = %v, want %v", ids, want)
	}
	if got[0].Stdout != stdout {
		t.Errorf("BaseCost stdout = %q, want %q", got[0].Stdout, stdout)
	}
}
