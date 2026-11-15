package interpret

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// TestRunPassesAtStartThenOncePerTickValue: with Ticks set, Run passes once at start and then once per value sent, and
// Passed is called after each pass. Passed reports how many calls the planner has had, read on Run's own goroutine, so
// each report shows the pass before it has run. The test sends a value only after the pass before it has reported, and
// waits only for the report of each pass, so every wait is an event.
func TestRunPassesAtStartThenOncePerTickValue(t *testing.T) {
	t.Parallel()
	s := openRunStore(t)
	ticks := make(chan time.Time)
	passed := make(chan int)
	planner := &fakePlanner{}
	runner := New(Config{
		Store: s, Planner: planner, Builder: &fakeBuilder{}, Ticks: ticks, Now: runClock,
		Passed: func() { passed <- len(planner.calls) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()

	// The planner records recover, then one call per pass, so the report after pass k counts 1+k calls.
	if got := <-passed; got != 2 {
		t.Fatalf("calls at the start pass's report = %d, want 2", got)
	}
	for k := 2; k <= 3; k++ {
		ticks <- time.Time{}
		if got := <-passed; got != 1+k {
			t.Fatalf("calls at pass %d's report = %d, want %d", k, got, 1+k)
		}
	}
	cancel()
	<-done

	want := []string{"recover", "pass", "pass", "pass", "wait"}
	if !reflect.DeepEqual(planner.calls, want) {
		t.Errorf("planner calls = %v, want %v", planner.calls, want)
	}
}
