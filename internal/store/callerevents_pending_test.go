package store

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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

// steerPendingEpic sets the steerer of epic to session by a direct update, for an epic that a helper created without
// one. The update is a store write, so the epic's revision moves as any write of its row does.
func steerPendingEpic(t *testing.T, s *Store, epic, session string) {
	t.Helper()
	query := `UPDATE epics SET steerer = ? WHERE id = ?`
	if _, err := s.fixtureExec(context.Background(), query, session, epic); err != nil {
		t.Fatalf("steer epic %s by %s: %v", epic, session, err)
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
	steerPendingEpic(t, s, claimEpic, "engine-1")
	recordPendingCreator(t, s, claimEpic, "engine-1")
	parked := fullEpic(pendingParked)
	parked.Steerer = "engine-1"
	createEpic(t, s, parked)
	if err := s.ParkEpic(ctx, pendingParked, ParkClassBranch, "credit ran out", pendingAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	recordPendingCreator(t, s, pendingParked, "engine-1")
	released := fullEpic(pendingReleased)
	released.Phase = "released"
	released.Steerer = "engine-1"
	createEpic(t, s, released)
	recordPendingCreator(t, s, pendingReleased, "engine-1")
	ended := fullEpic(pendingEnded)
	ended.Phase = "done"
	ended.Steerer = "engine-1"
	createEpic(t, s, ended)
	recordPendingCreator(t, s, pendingEnded, "engine-1")
	other := fullEpic(pendingOther)
	other.Phase = "released"
	other.Steerer = "engine-2"
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
			ID:     "step:step-a",
			Kind:   CallerKindStepEnd,
			Step:   "step-a",
			Agent:  "implementer",
			End:    "done",
			Status: "passed",
			Brief:  "Return: First line...\nNot shown: return (22 bytes)\nReason: gate green\nMore: step_result id step-a",
			Epic:   claimEpic,
			Facts:  StepFacts{Seconds: 60},
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
			Status: "passed", Brief: "Return: Other\nNot shown: none\nReason: gate green\nMore: step_result id step-b",
			Facts: StepFacts{Seconds: 120}},
		{ID: "epic:orrery-1a2b3c4/epic-other:released", Kind: CallerKindRelease, Epic: pendingOther,
			Title: "Synthetic epic"},
	}
	if !reflect.DeepEqual(gotOther, wantOther) {
		t.Fatalf("PendingCallerEvents(engine-2) =\n %+v\nwant\n %+v", gotOther, wantOther)
	}
}

// TestEndComesBackPendingDropsMarkedEvents: a step's end, and each epic event, stays listed for its caller until its
// event is marked under the caller's row id, and is listed no more after.
func TestEndComesBackPendingDropsMarkedEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openPendingStore(t)
	addPendingEngines(t, s, "engine-1", "engine-2")
	questionedEpic(t, s, shapedQuestion("keep-old-tasks", "Keep the old tasks?"))
	steerPendingEpic(t, s, claimEpic, "engine-1")
	recordPendingCreator(t, s, claimEpic, "engine-1")
	parked := fullEpic(pendingParked)
	parked.Steerer = "engine-1"
	createEpic(t, s, parked)
	if err := s.ParkEpic(ctx, pendingParked, ParkClassBranch, "credit ran out", pendingAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	recordPendingCreator(t, s, pendingParked, "engine-1")
	released := fullEpic(pendingReleased)
	released.Phase = "released"
	released.Steerer = "engine-1"
	createEpic(t, s, released)
	recordPendingCreator(t, s, pendingReleased, "engine-1")
	ended := fullEpic(pendingEnded)
	ended.Phase = "done"
	ended.Steerer = "engine-1"
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

	// A mark of the step's event drops its step end, and a mark of the event under another session drops nothing of
	// engine-1's. step-b has no mark, so its step end stays listed.
	if err := s.MarkCallerEvent(ctx, "step:step-a", "engine-1", pendingAt()); err != nil {
		t.Fatalf("MarkCallerEvent step-a: %v", err)
	}
	if err := s.MarkCallerEvent(ctx, questionID, "engine-2", pendingAt()); err != nil {
		t.Fatalf("MarkCallerEvent question under engine-2: %v", err)
	}
	unmarked := []string{"step:step-b", endID, parkID, releaseID, questionID}
	if got := pendingIDs(readPending(t, path, "engine-1")); !reflect.DeepEqual(got, unmarked) {
		t.Fatalf("PendingCallerEvents after a mark of step-a and of another session = %v, want %v", got, unmarked)
	}

	// Marks of step-b's end, the end and the park, through MarkCallerEvent, drop them.
	for _, id := range []string{"step:step-b", endID, parkID} {
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

// TestStepEventBriefPendingCarriesBrief ends a step under engine-1 with two lines of result and a reason, and reads its
// pending step end: its Brief is the brief's lines, the last of them the More line that names step_result.
func TestStepEventBriefPendingCarriesBrief(t *testing.T) {
	t.Parallel()
	s, path := openPendingStore(t)
	addPendingEngines(t, s, "engine-1")
	startPendingStep(t, s, "step-brief", "", "engine-1")
	endPendingStep(t, s, "step-brief", "First line\nSecond line", pendingAt().Add(time.Minute))

	want := []string{
		"Return: First line...",
		"Not shown: return (22 bytes)",
		"Reason: gate green",
		"More: step_result id step-brief",
	}
	got := readPending(t, path, "engine-1")
	if len(got) != 1 {
		t.Fatalf("PendingCallerEvents(engine-1) = %v, want the one step end", pendingIDs(got))
	}
	if lines := strings.Split(got[0].Brief, "\n"); !reflect.DeepEqual(lines, want) {
		t.Fatalf("Brief lines = %q, want %q", lines, want)
	}
}

// TestStepEventBriefPendingIsBounded ends a step under engine-1 with a 20 KB one-line result and a 16 KB reason of many
// lines, and reads its pending step end. Its Brief is the lines StepEndParts gives the step, at most StepEndBlockCap
// bytes, and it holds none of the result's last 100 bytes.
func TestStepEventBriefPendingIsBounded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openPendingStore(t)
	addPendingEngines(t, s, "engine-1")
	startPendingStep(t, s, "step-big", "", "engine-1")
	result := strings.Repeat("a", 20*1024-100) + strings.Repeat("z", 100)
	reason := strings.Repeat("a reason line\n", 16*1024/len("a reason line\n"))
	at := pendingAt().Add(time.Minute)
	if err := s.ApplyEngine(ctx, []EngineOp{StepReturn{ID: "step-big", Result: result}}, at); err != nil {
		t.Fatalf("ApplyEngine return step-big: %v", err)
	}
	end := StepEnd{ID: "step-big", End: "done", Status: "passed", Reason: reason}
	if err := s.ApplyEngine(ctx, []EngineOp{end}, at); err != nil {
		t.Fatalf("ApplyEngine end step-big: %v", err)
	}

	callers, err := s.CallerSteps(ctx)
	if err != nil {
		t.Fatalf("CallerSteps: %v", err)
	}
	i := slices.IndexFunc(callers, func(c CallerStep) bool { return c.Step.ID == "step-big" })
	if i < 0 {
		t.Fatalf("CallerSteps = %v, want step-big among them", callers)
	}
	_, lines := StepEndParts(callers[i].Step)
	want := strings.Join(lines, "\n")

	got := readPending(t, path, "engine-1")
	if len(got) != 1 {
		t.Fatalf("PendingCallerEvents(engine-1) = %v, want the one step end", pendingIDs(got))
	}
	brief := got[0].Brief
	if brief != want {
		t.Fatalf("Brief = %q, want the StepEndParts lines %q", brief, want)
	}
	if len(brief) > StepEndBlockCap {
		t.Fatalf("Brief is %d bytes, over the %d-byte cap", len(brief), StepEndBlockCap)
	}
	if !strings.HasSuffix(brief, "\nMore: step_result id step-big") {
		t.Fatalf("Brief = %q, which does not end with its More line", brief)
	}
	if tail := result[len(result)-100:]; strings.Contains(brief, tail) {
		t.Fatalf("Brief holds the result's last 100 bytes %q", tail)
	}
}
