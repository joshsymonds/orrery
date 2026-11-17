package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Epics the pending tests create. claimEpic, from the planner tests, asks an open question.
const (
	pendingEnded    = "orrery-1a2b3c4/epic-ended"
	pendingParked   = "orrery-1a2b3c4/epic-parked"
	pendingReleased = "orrery-1a2b3c4/epic-released"
	pendingOther    = "orrery-1a2b3c4/epic-other"
)

// pendingAt is the second the pending tests start at.
func pendingAt() time.Time {
	return time.Unix(1700004000, 0)
}

// openPendingStore opens a migrated store file and returns it with its path, so a Reader reads the same file.
func openPendingStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	s, err := Open(context.Background(), path, noSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s, path
}

// addPendingEngines adds an idle engine session for each id.
func addPendingEngines(t *testing.T, s *Store, ids ...string) {
	t.Helper()
	ops := make([]EngineOp, 0, len(ids))
	for _, id := range ids {
		ops = append(ops, EngineAdd{ID: id, Dir: "/work/engine", State: "idle"})
	}
	if err := s.ApplyEngine(context.Background(), ops, pendingAt()); err != nil {
		t.Fatalf("ApplyEngine add %v: %v", ids, err)
	}
}

// startPendingStep starts step id of epic under session, at pendingAt.
func startPendingStep(t *testing.T, s *Store, id, epic, session string) {
	t.Helper()
	add := StepAdd{ID: id, Agent: "implementer", Prompt: "do it", Epic: epic, Caller: EpicCaller{Session: session}}
	if err := s.ApplyEngine(context.Background(), []EngineOp{add}, pendingAt()); err != nil {
		t.Fatalf("ApplyEngine add step %s: %v", id, err)
	}
}

// endPendingStep gives step id the result, then ends it at at.
func endPendingStep(t *testing.T, s *Store, id, result string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := s.ApplyEngine(ctx, []EngineOp{StepReturn{ID: id, Result: result}}, at); err != nil {
		t.Fatalf("ApplyEngine return step %s: %v", id, err)
	}
	end := StepEnd{ID: id, End: "done", Status: "passed", Reason: "gate green"}
	if err := s.ApplyEngine(ctx, []EngineOp{end}, at); err != nil {
		t.Fatalf("ApplyEngine end step %s: %v", id, err)
	}
}

// recordPendingCreator records that session created epic, by its epic_create call.
func recordPendingCreator(t *testing.T, s *Store, epic, session string) {
	t.Helper()
	caller := EpicCaller{Session: session}
	if _, err := s.RecordEpicCall(context.Background(), epic, caller, epicCreateCall, pendingAt()); err != nil {
		t.Fatalf("RecordEpicCall %s by %s: %v", epic, session, err)
	}
}

// readPending returns the caller events still owed to session, read through a Reader of path.
func readPending(t *testing.T, path, session string) []PendingEvent {
	t.Helper()
	events, err := NewReader(path).PendingCallerEvents(context.Background(), session)
	if err != nil {
		t.Fatalf("PendingCallerEvents %s: %v", session, err)
	}
	return events
}

// pendingIDs returns the ids of events in order.
func pendingIDs(events []PendingEvent) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	return ids
}

func TestStepEventCardPendingListsOwedStepEndsAndEpicEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openPendingStore(t)
	addPendingEngines(t, s, "engine-1", "engine-2")

	// engine-1 creates claimEpic, which asks one question, pendingParked, which parks, and pendingReleased, which is
	// released. engine-2 creates pendingOther, which is released too.
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	recordPendingCreator(t, s, claimEpic, "engine-1")
	createEpic(t, s, fullEpic(pendingParked))
	if err := s.ParkEpic(ctx, pendingParked, ParkClassBranch, "credit ran out", pendingAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	recordPendingCreator(t, s, pendingParked, "engine-1")
	released := fullEpic(pendingReleased)
	released.Phase = "released"
	createEpic(t, s, released)
	recordPendingCreator(t, s, pendingReleased, "engine-1")
	ended := fullEpic(pendingEnded)
	ended.Phase = "done"
	createEpic(t, s, ended)
	recordPendingCreator(t, s, pendingEnded, "engine-1")
	other := fullEpic(pendingOther)
	other.Phase = "released"
	createEpic(t, s, other)
	recordPendingCreator(t, s, pendingOther, "engine-2")

	// engine-1 called step-a, which ends with two lines of result, and engine-2 called step-b.
	startPendingStep(t, s, "step-a", claimEpic, "engine-1")
	endPendingStep(t, s, "step-a", "First line\nSecond line", pendingAt().Add(time.Minute))
	startPendingStep(t, s, "step-b", "", "engine-2")
	endPendingStep(t, s, "step-b", "Other", pendingAt().Add(2*time.Minute))

	got := readPending(t, path, "engine-1")
	want := []PendingEvent{
		{
			ID: "step:step-a", Kind: CallerKindStepEnd, Step: "step-a", Agent: "implementer", End: "done",
			Status: "passed", Reason: "gate green", Summary: "First line", Epic: claimEpic,
		},
		{
			ID: "epic:orrery-1a2b3c4/epic-ended:done", Kind: CallerKindEnd, Epic: pendingEnded, Title: "Synthetic epic",
		},
		{
			ID:    "epic:orrery-1a2b3c4/epic-parked:Q1",
			Kind:  CallerKindPark,
			Epic:  pendingParked,
			Title: "Synthetic epic",
		},
		{
			ID: "epic:orrery-1a2b3c4/epic-released:released", Kind: CallerKindRelease, Epic: pendingReleased,
			Title: "Synthetic epic",
		},
		{
			ID: "epic:orrery-1a2b3c4/epic-store:Q1", Kind: CallerKindQuestion, Epic: claimEpic, Title: "Synthetic epic",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PendingCallerEvents(engine-1) =\n %+v\nwant\n %+v", got, want)
	}

	gotOther := readPending(t, path, "engine-2")
	wantOther := []PendingEvent{
		{ID: "step:step-b", Kind: CallerKindStepEnd, Step: "step-b", Agent: "implementer", End: "done",
			Status: "passed", Reason: "gate green", Summary: "Other"},
		{ID: "epic:orrery-1a2b3c4/epic-other:released", Kind: CallerKindRelease, Epic: pendingOther,
			Title: "Synthetic epic"},
	}
	if !reflect.DeepEqual(gotOther, wantOther) {
		t.Fatalf("PendingCallerEvents(engine-2) =\n %+v\nwant\n %+v", gotOther, wantOther)
	}
}

func TestStepEventCardPendingDropsReadAndMarkedEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openPendingStore(t)
	addPendingEngines(t, s, "engine-1", "engine-2")
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	recordPendingCreator(t, s, claimEpic, "engine-1")
	createEpic(t, s, fullEpic(pendingParked))
	if err := s.ParkEpic(ctx, pendingParked, ParkClassBranch, "credit ran out", pendingAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	recordPendingCreator(t, s, pendingParked, "engine-1")
	released := fullEpic(pendingReleased)
	released.Phase = "released"
	createEpic(t, s, released)
	recordPendingCreator(t, s, pendingReleased, "engine-1")
	ended := fullEpic(pendingEnded)
	ended.Phase = "done"
	createEpic(t, s, ended)
	recordPendingCreator(t, s, pendingEnded, "engine-1")

	startPendingStep(t, s, "step-a", "", "engine-1")
	endPendingStep(t, s, "step-a", "A", pendingAt().Add(time.Minute))
	startPendingStep(t, s, "step-b", "", "engine-1")
	endPendingStep(t, s, "step-b", "B", pendingAt().Add(2*time.Minute))

	const endID = "epic:orrery-1a2b3c4/epic-ended:done"
	const parkID = "epic:orrery-1a2b3c4/epic-parked:Q1"
	const releaseID = "epic:orrery-1a2b3c4/epic-released:released"
	const questionID = "epic:orrery-1a2b3c4/epic-store:Q1"
	all := []string{"step:step-a", "step:step-b", endID, parkID, releaseID, questionID}
	if got := pendingIDs(readPending(t, path, "engine-1")); !reflect.DeepEqual(got, all) {
		t.Fatalf("PendingCallerEvents before any read or mark = %v, want %v", got, all)
	}

	// A step read through MarkStepRead drops its step end; a mark of the step's event drops the other.
	if marked, err := s.MarkStepRead(ctx, "step-b", EpicCaller{Session: "engine-1"}); err != nil || !marked {
		t.Fatalf("MarkStepRead step-b = %v, %v, want true, nil", marked, err)
	}
	if err := s.MarkCallerEvent(ctx, "step:step-a", "engine-1", pendingAt()); err != nil {
		t.Fatalf("MarkCallerEvent step-a: %v", err)
	}
	// A mark of the event under another session drops nothing of engine-1's.
	if err := s.MarkCallerEvent(ctx, questionID, "engine-2", pendingAt()); err != nil {
		t.Fatalf("MarkCallerEvent question under engine-2: %v", err)
	}
	epicIDs := []string{endID, parkID, releaseID, questionID}
	if got := pendingIDs(readPending(t, path, "engine-1")); !reflect.DeepEqual(got, epicIDs) {
		t.Fatalf("PendingCallerEvents after the step reads and a mark of another session = %v, want %v", got, epicIDs)
	}

	// Marks of the end and the park, through MarkCallerEvent, drop them.
	for _, id := range []string{endID, parkID} {
		if err := s.MarkCallerEvent(ctx, id, "engine-1", pendingAt()); err != nil {
			t.Fatalf("MarkCallerEvent %s: %v", id, err)
		}
	}
	if got := pendingIDs(readPending(t, path, "engine-1")); !reflect.DeepEqual(got, []string{releaseID, questionID}) {
		t.Fatalf("PendingCallerEvents after the end and park are marked = %v, want the release and the question", got)
	}

	if err := s.MarkCallerEvent(ctx, EpicPhaseEvent(pendingReleased, "released"), "engine-1", pendingAt()); err != nil {
		t.Fatalf("MarkCallerEvent release: %v", err)
	}
	if err := s.MarkCallerEvent(ctx, EpicQuestionEvent(claimEpic, 1), "engine-1", pendingAt()); err != nil {
		t.Fatalf("MarkCallerEvent question: %v", err)
	}
	if got := readPending(t, path, "engine-1"); len(got) != 0 {
		t.Fatalf("PendingCallerEvents after every event is marked or read = %v, want none", pendingIDs(got))
	}
}
