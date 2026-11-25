package interpret

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestDesignHeldEpicRunsNoPlannerTurn asserts that a pass over a held epic starts no node run, passes the planner no
// epic, hands no task to the builder, and leaves the epic held. The epic has a plan workflow that would start a plan run
// were the pass to read it.
func TestDesignHeldEpicRunsNoPlannerTurn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	if err := s.CreateEpic(ctx, store.EpicRow{
		ID: runEpic, Repository: runRepo, Base: runBase, Title: "Run the workflow", Phase: store.PhaseHeld,
		Created: runClock(), Changed: runClock(), Workflow: planBuildPin(),
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	planner, builder := &fakePlanner{known: []string{runEpic}}, &fakeBuilder{}

	newRunner(s, planner, builder).Pass(ctx)

	if runs := runsOf(t, s); len(runs) != 0 {
		t.Errorf("node runs = %+v, want none for a held epic", runs)
	}
	if wantNamed := [][]string{nil}; !reflect.DeepEqual(planner.named, wantNamed) {
		t.Errorf("PassOnly named %v, want %v: no epic passed to the planner", planner.named, wantNamed)
	}
	if len(builder.passes) != 0 {
		t.Errorf("PassTasks calls = %+v, want none for a held epic", builder.passes)
	}
	if got := epicOf(t, s).Phase; got != store.PhaseHeld {
		t.Errorf("epic phase = %q, want %q unchanged", got, store.PhaseHeld)
	}
}
