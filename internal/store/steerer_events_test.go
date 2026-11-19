package store

import (
	"context"
	"reflect"
	"testing"
)

// TestSteererEventsPendingNamesEventsOnlyForTheSteerer: engine-2 creates each epic and engine-1 steers it. The pending
// list of engine-1 names the question, the park, the release and the end, and the pending list of engine-2 names none.
func TestSteererEventsPendingNamesEventsOnlyForTheSteerer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openPendingStore(t)
	addPendingEngines(t, s, "engine-1", "engine-2")

	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	steerPendingEpic(t, s, claimEpic, "engine-1")
	recordPendingCreator(t, s, claimEpic, "engine-2")
	parked := fullEpic(pendingParked)
	parked.Steerer = "engine-1"
	createEpic(t, s, parked)
	if err := s.ParkEpic(ctx, pendingParked, ParkClassBranch, "credit ran out", pendingAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	recordPendingCreator(t, s, pendingParked, "engine-2")
	released := fullEpic(pendingReleased)
	released.Phase = "released"
	released.Steerer = "engine-1"
	createEpic(t, s, released)
	recordPendingCreator(t, s, pendingReleased, "engine-2")
	ended := fullEpic(pendingEnded)
	ended.Phase = "done"
	ended.Steerer = "engine-1"
	createEpic(t, s, ended)
	recordPendingCreator(t, s, pendingEnded, "engine-2")

	want := []string{
		"epic:orrery-1a2b3c4/epic-ended:done",
		"epic:orrery-1a2b3c4/epic-parked:Q1",
		"epic:orrery-1a2b3c4/epic-released:released",
		"epic:orrery-1a2b3c4/epic-store:Q1",
	}
	if got := pendingIDs(readPending(t, path, "engine-1")); !reflect.DeepEqual(got, want) {
		t.Fatalf("PendingCallerEvents(steerer) = %v, want %v", got, want)
	}
	if got := readPending(t, path, "engine-2"); len(got) != 0 {
		t.Fatalf("PendingCallerEvents(creator) = %v, want none", pendingIDs(got))
	}
}
