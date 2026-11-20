package interpret

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestCloseEpicPassStartsNothingOnAClosedEpic closes a new epic on the built-in workflow before any pass, which would
// otherwise start its red capture and its plan run, and checks that passes after the close start no node run, give the
// epic no planner turn and hand no build to the builder.
func TestCloseEpicPassStartsNothingOnAClosedEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openRunStore(t)
	createRunEpic(t, s, builtinPin(t))
	if _, err := s.CloseEpic(
		ctx,
		runEpic,
		"abandoned",
		"",
		store.CloserOwner,
		store.Maker{By: store.MakerOwner},
		runClock(),
	); err != nil {
		t.Fatalf("CloseEpic: %v", err)
	}
	planner, builder := &fakePlanner{known: []string{runEpic}}, &fakeBuilder{}
	runner := newRunner(s, planner, builder)
	runner.Pass(ctx)
	runner.Pass(ctx)

	if got := runsOf(t, s); len(got) != 0 {
		t.Errorf("node runs after passes on a closed epic = %+v, want none", got)
	}
	if want := [][]string{nil, nil}; !reflect.DeepEqual(planner.named, want) {
		t.Errorf("PassOnly named %v, want %v: the closed epic is given no planner turn", planner.named, want)
	}
	if len(builder.passes) != 0 {
		t.Errorf("PassTasks calls = %+v, want none for a closed epic", builder.passes)
	}
}
