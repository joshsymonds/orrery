package store_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// loopEpic and loopOther are the epics the loop write tests write to. Both are created, so ParkLoop can park them.
const (
	loopEpic  = "orrery-5c6d7e8/epic-loops"
	loopOther = "orrery-5c6d7e8/epic-loops-other"
)

// loopParkedAt is the second the loop write tests park a loop at.
func loopParkedAt() time.Time {
	return time.Unix(1700001300, 0)
}

// loopEpicRow is the accepted epic with the given id that the loop write tests create.
func loopEpicRow(id string) store.EpicRow {
	created := time.Unix(1700000000, 0)
	return store.EpicRow{
		ID: id, Repository: "/work/synthetic-repo", Base: "main", Title: "Synthetic epic",
		Approved: "Approved sentence", Intent: "Store the loop writes", Care: "serious",
		Approach: "One round per call", ReleaseThen: "Then line", Checks: "just check", Phase: "accepted",
		Created: created, Changed: created,
	}
}

// openLoopStore opens a migrated store with loopEpic and loopOther created, and returns it with a raw handle to the
// same file for the epic and tell rows the store API does not return whole.
func openLoopStore(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	s, raw := openNodeStore(t)
	for _, id := range []string{loopEpic, loopOther} {
		if err := s.CreateEpic(context.Background(), loopEpicRow(id)); err != nil {
			t.Fatalf("CreateEpic %s: %v", id, err)
		}
	}
	return s, raw
}

// requireNodeRuns fails unless the node runs of epic are want, ordered by path.
func requireNodeRuns(t *testing.T, s *store.Store, epic string, want []store.NodeRun) {
	t.Helper()
	got, err := s.NodeRuns(context.Background(), epic)
	if err != nil {
		t.Fatalf("NodeRuns of epic %s: %v", epic, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NodeRuns of epic %s = %+v, want %+v", epic, got, want)
	}
}

// parkedOf returns the park reason stored for epic, empty while it is not parked.
func parkedOf(t *testing.T, raw *sql.DB, epic string) string {
	t.Helper()
	var parked string
	if err := raw.QueryRow(`SELECT parked FROM epics WHERE id = ?`, epic).Scan(&parked); err != nil {
		t.Fatalf("read parked of epic %s: %v", epic, err)
	}
	return parked
}

// tellsOf returns the text of every tell of epic in seq order.
func tellsOf(t *testing.T, raw *sql.DB, epic string) []string {
	t.Helper()
	rows, err := raw.Query(`SELECT text FROM epic_tells WHERE epic = ? ORDER BY seq`, epic)
	if err != nil {
		t.Fatalf("read tells of epic %s: %v", epic, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatalf("scan tell of epic %s: %v", epic, err)
		}
		out = append(out, text)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read tells of epic %s: %v", epic, err)
	}
	return out
}

// TestNextRoundMovesARunningLoopOneRoundPerCall checks that NextRound moves a running loop from its round to the next,
// that a repeat with the same from changes nothing, and that the next call moves the loop on from its new round.
func TestNextRoundMovesARunningLoopOneRoundPerCall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openLoopStore(t)

	loop := store.NodeRun{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 1}
	if err := s.StartNodes(ctx, loopEpic, []store.NodeRun{loop}, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}

	if err := s.NextRound(ctx, loopEpic, "fix", 1); err != nil {
		t.Fatalf("NextRound from 1: %v", err)
	}
	if err := s.NextRound(ctx, loopEpic, "fix", 1); err != nil {
		t.Fatalf("repeat NextRound from 1: %v", err)
	}
	want := []store.NodeRun{
		{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 2,
			State: store.NodeRunning, Started: nodeStartedAt()},
	}
	requireNodeRuns(t, s, loopEpic, want)

	if err := s.NextRound(ctx, loopEpic, "fix", 2); err != nil {
		t.Fatalf("NextRound from 2: %v", err)
	}
	want[0].LoopRound = 3
	requireNodeRuns(t, s, loopEpic, want)
}

// TestNextRoundChangesNothingOnEndedParkedOrNonLoopRuns checks that NextRound leaves an ended loop, a parked loop and a
// running run that is not a loop as they are, whatever from it is given.
func TestNextRoundChangesNothingOnEndedParkedOrNonLoopRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openLoopStore(t)

	runs := []store.NodeRun{
		{Path: "done", ID: "id-done", Node: "done", Kind: "loop", LoopRound: 1},
		{Path: "held", ID: "id-held", Node: "held", Kind: "loop", LoopRound: 2},
		{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan"},
	}
	if err := s.StartNodes(ctx, loopEpic, runs, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.EndNodes(
		ctx, loopEpic, []store.NodeEnd{{Path: "done", Outcome: "complete"}}, nodeEndedAt(),
	); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}
	if _, err := raw.Exec(
		`UPDATE epic_node_runs SET state = 'parked' WHERE epic = ? AND path = 'held'`, loopEpic,
	); err != nil {
		t.Fatalf("park held loop: %v", err)
	}

	for _, call := range []struct {
		path string
		from int64
	}{{"done", 1}, {"held", 2}, {"plan", 0}} {
		if err := s.NextRound(ctx, loopEpic, call.path, call.from); err != nil {
			t.Fatalf("NextRound of %s from %d: %v", call.path, call.from, err)
		}
	}

	want := []store.NodeRun{
		{Path: "done", ID: "id-done", Node: "done", Kind: "loop", LoopRound: 1,
			State: store.NodeEnded, Outcome: "complete", Started: nodeStartedAt(), Ended: nodeEndedAt()},
		{Path: "held", ID: "id-held", Node: "held", Kind: "loop", LoopRound: 2,
			State: store.NodeParked, Started: nodeStartedAt()},
		{Path: "plan", ID: "id-plan", Node: "plan", Kind: "plan",
			State: store.NodeRunning, Started: nodeStartedAt()},
	}
	requireNodeRuns(t, s, loopEpic, want)
}

// TestParkLoopParksTheLoopAndTheEpicWithOneTell checks that ParkLoop parks the running loop at the round asked for,
// parks its epic with the reason, and adds the reason as one tell, and that the other epic is untouched.
func TestParkLoopParksTheLoopAndTheEpicWithOneTell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openLoopStore(t)

	loop := store.NodeRun{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 2}
	if err := s.StartNodes(ctx, loopEpic, []store.NodeRun{loop}, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}

	const reason = "loop fix spent its 2 rounds without review.complete"
	if err := s.ParkLoop(ctx, loopEpic, "fix", 2, reason, loopParkedAt()); err != nil {
		t.Fatalf("ParkLoop: %v", err)
	}

	requireNodeRuns(t, s, loopEpic, []store.NodeRun{
		{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 2,
			State: store.NodeParked, Started: nodeStartedAt()},
	})
	if parked := parkedOf(t, raw, loopEpic); parked != reason {
		t.Errorf("parked of epic %s after ParkLoop = %q, want %q", loopEpic, parked, reason)
	}
	if got := tellsOf(t, raw, loopEpic); !reflect.DeepEqual(got, []string{reason}) {
		t.Errorf("tells of epic %s after ParkLoop = %q, want one tell %q", loopEpic, got, reason)
	}
	if parked := parkedOf(t, raw, loopOther); parked != "" {
		t.Errorf("parked of epic %s after ParkLoop of %s = %q, want empty", loopOther, loopEpic, parked)
	}
}

// TestParkLoopAtAnotherRoundOrRepeatChangesNothing checks that ParkLoop at a round other than the loop's changes
// neither the loop nor the epic, and that a second ParkLoop of a loop already parked adds no tell and keeps the first
// reason.
func TestParkLoopAtAnotherRoundOrRepeatChangesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openLoopStore(t)

	loop := store.NodeRun{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 2}
	if err := s.StartNodes(ctx, loopEpic, []store.NodeRun{loop}, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}

	if err := s.ParkLoop(ctx, loopEpic, "fix", 1, "wrong round", loopParkedAt()); err != nil {
		t.Fatalf("ParkLoop at round 1: %v", err)
	}
	running := []store.NodeRun{
		{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 2,
			State: store.NodeRunning, Started: nodeStartedAt()},
	}
	requireNodeRuns(t, s, loopEpic, running)
	if parked := parkedOf(t, raw, loopEpic); parked != "" {
		t.Errorf("parked of epic %s after ParkLoop at another round = %q, want empty", loopEpic, parked)
	}
	if got := tellsOf(t, raw, loopEpic); len(got) != 0 {
		t.Errorf("tells of epic %s after ParkLoop at another round = %q, want none", loopEpic, got)
	}

	const first = "first reason"
	if err := s.ParkLoop(ctx, loopEpic, "fix", 2, first, loopParkedAt()); err != nil {
		t.Fatalf("ParkLoop at round 2: %v", err)
	}
	if err := s.ParkLoop(ctx, loopEpic, "fix", 2, "second reason", nodeEndedAt()); err != nil {
		t.Fatalf("repeat ParkLoop at round 2: %v", err)
	}
	requireNodeRuns(t, s, loopEpic, []store.NodeRun{
		{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 2,
			State: store.NodeParked, Started: nodeStartedAt()},
	})
	if parked := parkedOf(t, raw, loopEpic); parked != first {
		t.Errorf("parked of epic %s after a repeat = %q, want %q", loopEpic, parked, first)
	}
	if got := tellsOf(t, raw, loopEpic); !reflect.DeepEqual(got, []string{first}) {
		t.Errorf("tells of epic %s after a repeat = %q, want one tell %q", loopEpic, got, first)
	}
}

// TestRetryEpicGrantsEachParkedLoopOneRound checks that RetryEpic of an epic parked by ParkLoop turns each parked loop
// of that epic running with one granted round, leaves its running loops and another epic's parked loop as they were,
// and that a NextRound then moves the granted loop on from its parked round.
func TestRetryEpicGrantsEachParkedLoopOneRound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openLoopStore(t)

	runs := []store.NodeRun{
		{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 2},
		{Path: "review", ID: "id-review", Node: "review", Kind: "loop", LoopRound: 1},
	}
	if err := s.StartNodes(ctx, loopEpic, runs, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes of epic %s: %v", loopEpic, err)
	}
	otherRuns := []store.NodeRun{{Path: "fix", ID: "id-other-fix", Node: "fix", Kind: "loop", LoopRound: 4}}
	if err := s.StartNodes(ctx, loopOther, otherRuns, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes of epic %s: %v", loopOther, err)
	}
	if err := s.ParkLoop(ctx, loopEpic, "fix", 2, "loop fix spent its 2 rounds", loopParkedAt()); err != nil {
		t.Fatalf("ParkLoop of epic %s: %v", loopEpic, err)
	}
	if err := s.ParkLoop(ctx, loopOther, "fix", 4, "loop fix spent its 4 rounds", loopParkedAt()); err != nil {
		t.Fatalf("ParkLoop of epic %s: %v", loopOther, err)
	}

	if err := s.RetryEpic(ctx, loopEpic); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	if parked := parkedOf(t, raw, loopEpic); parked != "" {
		t.Errorf("parked of epic %s after RetryEpic = %q, want empty", loopEpic, parked)
	}
	want := []store.NodeRun{
		{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 2, Granted: 1,
			State: store.NodeRunning, Started: nodeStartedAt()},
		{Path: "review", ID: "id-review", Node: "review", Kind: "loop", LoopRound: 1,
			State: store.NodeRunning, Started: nodeStartedAt()},
	}
	requireNodeRuns(t, s, loopEpic, want)
	requireNodeRuns(t, s, loopOther, []store.NodeRun{
		{Path: "fix", ID: "id-other-fix", Node: "fix", Kind: "loop", LoopRound: 4,
			State: store.NodeParked, Started: nodeStartedAt()},
	})

	if err := s.NextRound(ctx, loopEpic, "fix", 2); err != nil {
		t.Fatalf("NextRound after RetryEpic: %v", err)
	}
	want[0].LoopRound = 3
	requireNodeRuns(t, s, loopEpic, want)
}

// TestRetryEpicWithoutParkedLoopGrantsNothing checks that RetryEpic of an epic parked with no loop parked clears the
// park and leaves its running loop with no granted round.
func TestRetryEpicWithoutParkedLoopGrantsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openLoopStore(t)

	loop := store.NodeRun{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 1}
	if err := s.StartNodes(ctx, loopEpic, []store.NodeRun{loop}, nil, nodeStartedAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.ParkEpic(ctx, loopEpic, "credit ran out", loopParkedAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}

	if err := s.RetryEpic(ctx, loopEpic); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	if parked := parkedOf(t, raw, loopEpic); parked != "" {
		t.Errorf("parked of epic %s after RetryEpic = %q, want empty", loopEpic, parked)
	}
	requireNodeRuns(t, s, loopEpic, []store.NodeRun{
		{Path: "fix", ID: "id-fix", Node: "fix", Kind: "loop", LoopRound: 1,
			State: store.NodeRunning, Started: nodeStartedAt()},
	})
}
