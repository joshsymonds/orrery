package store

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

// TestStepByPartListCaller stores the steps of two callers' sessions and reads one caller's listing. Caller A starts
// two running steps and twenty-five ended ones, some unlabelled and some under two epics. Each ended step started
// at its index in seconds and ended so that the step started first ended last, so the most recently ended steps are
// the first started. Caller B's steps end after all of A's, so a listing that lacked the caller narrowing would take
// them.
func TestStepByPartListCaller(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	start := engineNowAt()
	end := start.Add(1000 * time.Second)
	apply := func(at time.Time, ops ...EngineOp) {
		t.Helper()
		if err := s.ApplyEngine(ctx, ops, at); err != nil {
			t.Fatalf("ApplyEngine at %d: %v", at.Unix(), err)
		}
	}
	addStep := func(id, epic, session string, at time.Time) {
		step := stepAddOf(id)
		step.Epic = epic
		step.Caller = EpicCaller{Session: session}
		apply(at, step)
	}
	endStep := func(id string, at time.Time) {
		apply(at, StepEnd{ID: id, End: "done", Status: "DONE", Reason: "returned"})
	}

	apply(start,
		EngineAdd{ID: "caller-a", Dir: "/work/a", State: "idle"},
		EngineAdd{ID: "caller-b", Dir: "/work/b", State: "idle"},
	)
	// Of A's ended steps, index 0 is unlabelled, indexes 4, 9, ... are under F and the rest under E.
	for i := range 25 {
		id := fmt.Sprintf("end-%02d", i)
		epic := "E"
		switch i % 5 {
		case 0:
			epic = ""
		case 4:
			epic = "F"
		}
		addStep(id, epic, "caller-a", start.Add(time.Duration(i)*time.Second))
		endStep(id, end.Add(-time.Duration(i)*time.Second))
	}
	addStep("run-e", "E", "caller-a", start.Add(30*time.Second))
	addStep("run-u", "", "caller-a", start.Add(31*time.Second))
	addStep("b-run", "E", "caller-b", start.Add(40*time.Second))
	for k := range 3 {
		id := fmt.Sprintf("b-end-%d", k)
		addStep(id, "E", "caller-b", start.Add(time.Duration(41+k)*time.Second))
		endStep(id, end.Add(time.Duration(1+k)*time.Second))
	}

	allSteps := make([]string, 0, 22)
	for i := range 20 {
		allSteps = append(allSteps, fmt.Sprintf("end-%02d", i))
	}
	allSteps = append(allSteps, "run-e", "run-u")
	cases := []struct {
		name, epic string
		want       []string
	}{
		{
			name: "every step",
			want: allSteps,
		},
		{
			name: "one epic keeps every ended step of that epic, as it has fewer than 20",
			epic: "E",
			want: []string{
				"end-01", "end-02", "end-03", "end-06", "end-07", "end-08", "end-11", "end-12", "end-13",
				"end-16", "end-17", "end-18", "end-21", "end-22", "end-23", "run-e",
			},
		},
		{
			name: "an epic with no running step",
			epic: "F",
			want: []string{"end-04", "end-09", "end-14", "end-19", "end-24"},
		},
	}
	for _, tc := range cases {
		listings, err := s.CallerStepListings(ctx, "caller-a", tc.epic)
		if err != nil {
			t.Fatalf("%s: CallerStepListings: %v", tc.name, err)
		}
		got := stepListingIDs(listings)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: CallerStepListings = %v, want %v", tc.name, got, tc.want)
		}
		t.Logf("%s: CallerStepListings(caller-a) = %d steps %v", tc.name, len(got), got)
	}
}
