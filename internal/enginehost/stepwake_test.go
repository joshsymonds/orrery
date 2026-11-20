package enginehost

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

// stepWakeEpicA, stepWakeEpicB and stepWakeEpicC are the epics the flow wake tests label their steps with.
const (
	stepWakeEpicA = "orrery-0668c87/wake-a"
	stepWakeEpicB = "orrery-0668c87/wake-b"
	stepWakeEpicC = "orrery-0668c87/wake-c"
)

// stepWakeLog records the values a wake hook is called with, in call order. The hooks run on the step goroutines and the
// manager's pumps, so it is safe for concurrent use.
type stepWakeLog[T any] struct {
	mu    sync.Mutex
	calls []T
}

// record appends v to the calls the log holds.
func (l *stepWakeLog[T]) record(v T) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, v)
}

// all returns the calls the log holds, in order.
func (l *stepWakeLog[T]) all() []T {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls)
}

// TestStepEndFlowWakeNamesItsEpic covers R1: a step labeled with an epic wakes that epic once when it ends, and a step with
// no epic wakes none when it ends.
func TestStepEndFlowWakeNamesItsEpic(t *testing.T) {
	t.Parallel()
	f := newStepStartFixture(t)
	var wakes stepWakeLog[string]
	f.steps.epicWake = wakes.record
	repo := stepGitTestRepo(t)
	labeled := stepStartReadOnly(repo, "HEAD")
	labeled.Epic = stepWakeEpicB
	id, err := f.steps.Start(t.Context(), labeled)
	if err != nil {
		t.Fatalf("Start of the labeled step: %v", err)
	}
	if err = f.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop of the labeled step: %v", err)
	}
	if got := wakes.all(); !slices.Equal(got, []string{stepWakeEpicB}) {
		t.Fatalf("EpicWake calls after the labeled step's end = %q, want [%s] once", got, stepWakeEpicB)
	}
	id, err = f.steps.Start(t.Context(), stepStartReadOnly(repo, "HEAD"))
	if err != nil {
		t.Fatalf("Start of the unlabeled step: %v", err)
	}
	if err = f.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop of the unlabeled step: %v", err)
	}
	if got := wakes.all(); !slices.Equal(got, []string{stepWakeEpicB}) {
		t.Fatalf("EpicWake calls after the unlabeled step's end = %q, want still [%s] once", got, stepWakeEpicB)
	}
}

// TestStepCapFreeFlowWakeNamesRefusedEpic covers R1: with the step cap full, a start for epic C is refused and recorded.
// The next free of a step slot hands C on, and a free that refused no epic hands on none.
func TestStepCapFreeFlowWakeNamesRefusedEpic(t *testing.T) {
	t.Parallel()
	f := newRefusalFixture(t, 1)
	var frees stepWakeLog[[]string]
	f.manager.stepFreed = frees.record
	repo := stepGitTestRepo(t)
	running := stepStartReadOnly(repo, "HEAD")
	running.Prompt = "HANG"
	running.Epic = stepWakeEpicA
	idA, err := refusalStart(t, f.steps, running)
	if err != nil || idA == "" {
		t.Fatalf("first Start = %q, %v; want a running step", idA, err)
	}
	refused := stepStartReadOnly(repo, "HEAD")
	refused.Prompt = "HANG"
	refused.Epic = stepWakeEpicC
	if _, err = refusalStart(t, f.steps, refused); !errors.Is(err, ErrCapReached) {
		t.Fatalf("Start of epic C with the step cap full: err = %v, want ErrCapReached", err)
	}
	if err = f.steps.Stop(t.Context(), idA); err != nil {
		t.Fatalf("Stop of epic A's step: %v", err)
	}
	got := frees.all()
	if len(got) != 1 || !slices.Equal(got[0], []string{stepWakeEpicC}) {
		t.Fatalf("StepFreed calls after epic A's step ended = %q, want [[%s]]", got, stepWakeEpicC)
	}
	idC, err := refusalStart(t, f.steps, refused)
	if err != nil {
		t.Fatalf("Start of epic C after the free: %v", err)
	}
	if err = f.steps.Stop(t.Context(), idC); err != nil {
		t.Fatalf("Stop of epic C's step: %v", err)
	}
	got = frees.all()
	if len(got) != 2 || got[1] != nil {
		t.Fatalf("StepFreed calls after epic C's step ended = %q, want the first [[%s]] then nil", got, stepWakeEpicC)
	}
}
