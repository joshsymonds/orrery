package store_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// callerEpic is the epic the caller event tests make calls of.
const callerEpic = "orrery-7b8c9d0/epic-callers"

// callerStart is the second the caller event tests start at, and callerLater is a minute after it.
func callerStart() time.Time {
	return time.Unix(1700003000, 0)
}

func callerLater() time.Time {
	return time.Unix(callerStart().Unix()+60, 0)
}

// addCallerEngines adds one idle engine row for each id.
func addCallerEngines(t *testing.T, s *store.Store, ids ...string) {
	t.Helper()
	ops := make([]store.EngineOp, 0, len(ids))
	for _, id := range ids {
		ops = append(ops, store.EngineAdd{ID: id, Dir: "/work/engine", State: "idle"})
	}
	if err := s.ApplyEngine(context.Background(), ops, callerStart()); err != nil {
		t.Fatalf("ApplyEngine add %v: %v", ids, err)
	}
}

// recordCaller records call of callerEpic by the engine session id, at at.
func recordCaller(t *testing.T, s *store.Store, id, call string, at time.Time) {
	t.Helper()
	caller := store.EpicCaller{Session: id}
	if _, err := s.RecordEpicCall(context.Background(), callerEpic, caller, call, at); err != nil {
		t.Fatalf("RecordEpicCall %s by %s: %v", call, id, err)
	}
}

// startCallerStep starts the step id under the session caller, at callerStart.
func startCallerStep(t *testing.T, s *store.Store, id string, caller store.EpicCaller) {
	t.Helper()
	add := store.StepAdd{ID: id, Agent: "implementer", Prompt: "do it", Caller: caller}
	if err := s.ApplyEngine(context.Background(), []store.EngineOp{add}, callerStart()); err != nil {
		t.Fatalf("ApplyEngine add step %s: %v", id, err)
	}
}

// endCallerStep ends the step id at at.
func endCallerStep(t *testing.T, s *store.Store, id string, at time.Time) {
	t.Helper()
	end := store.StepEnd{ID: id, End: "done", Status: "passed", Reason: "gate green"}
	if err := s.ApplyEngine(context.Background(), []store.EngineOp{end}, at); err != nil {
		t.Fatalf("ApplyEngine end step %s: %v", id, err)
	}
}

// callerStepIDs returns the step ids of steps in order.
func callerStepIDs(steps []store.CallerStep) []string {
	ids := make([]string, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.Step.ID)
	}
	return ids
}

func listCallerSteps(t *testing.T, s *store.Store) []store.CallerStep {
	t.Helper()
	steps, err := s.CallerSteps(context.Background())
	if err != nil {
		t.Fatalf("CallerSteps: %v", err)
	}
	return steps
}

func TestCallerEventStoreEpicCreateMarksCreator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	addCallerEngines(t, s, "engine-1", "engine-2")

	recordCaller(t, s, "engine-2", "epic_get", callerStart())
	if _, ok, err := s.EpicCreator(ctx, callerEpic); err != nil || ok {
		t.Fatalf("EpicCreator before any epic_create = ok %v, err %v, want no creator", ok, err)
	}

	recordCaller(t, s, "engine-1", "epic_create", callerLater())
	creator, ok, err := s.EpicCreator(ctx, callerEpic)
	if err != nil {
		t.Fatalf("EpicCreator: %v", err)
	}
	want := store.CallerSession{ID: "engine-1", Kind: "engine", State: "idle"}
	if !ok || creator != want {
		t.Fatalf("EpicCreator after epic_create = %+v, %v, want %+v, true", creator, ok, want)
	}
}

func TestCallerEventStoreLaterCallKeepsCreator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	addCallerEngines(t, s, "engine-1")

	recordCaller(t, s, "engine-1", "epic_create", callerStart())
	recordCaller(t, s, "engine-1", "epic_get", callerLater())

	creator, ok, err := s.EpicCreator(ctx, callerEpic)
	if err != nil {
		t.Fatalf("EpicCreator: %v", err)
	}
	if !ok || creator.ID != "engine-1" {
		t.Fatalf("EpicCreator after a later epic_get = %+v, %v, want engine-1 still the creator", creator, ok)
	}
}

func TestCallerEventStoreEpicCreatorReturnsPaneRowUntilItIsGone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	pane := store.Pane{ID: "orrery-1", Pane: "%1", Fields: store.Fields{State: "idle"}}
	if err := s.SyncPane(ctx, "-L orrery", pane, callerStart()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	caller := store.EpicCaller{Server: "-L orrery", Pane: "%1"}
	if _, err := s.RecordEpicCall(ctx, callerEpic, caller, "epic_create", callerStart()); err != nil {
		t.Fatalf("RecordEpicCall: %v", err)
	}

	creator, ok, err := s.EpicCreator(ctx, callerEpic)
	if err != nil {
		t.Fatalf("EpicCreator: %v", err)
	}
	want := store.CallerSession{ID: "orrery-1", Server: "-L orrery", Pane: "%1", State: "idle"}
	if !ok || creator != want {
		t.Fatalf("EpicCreator = %+v, %v, want %+v, true", creator, ok, want)
	}

	// The pane leaves the server, so its sessions row is removed and the creator is gone with it.
	if err := s.SyncServer(ctx, "-L orrery", nil, callerLater()); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if creator, ok, err := s.EpicCreator(ctx, callerEpic); err != nil || ok {
		t.Fatalf("EpicCreator after the pane is gone = %+v, %v, %v, want no creator", creator, ok, err)
	}
}

func TestCallerEventStoreCallerStepsListsUnreadStepWithItsCaller(t *testing.T) {
	t.Parallel()
	s, _ := openEpicCallStore(t)
	addCallerEngines(t, s, "engine-1")
	caller := store.EpicCaller{Session: "engine-1"}
	// step-a ends after step-b, so the listing orders by ended time before id.
	startCallerStep(t, s, "step-a", caller)
	startCallerStep(t, s, "step-b", caller)
	endCallerStep(t, s, "step-b", callerStart().Add(time.Second))
	endCallerStep(t, s, "step-a", callerLater())
	// A step with no caller and a step still running are not listed.
	startCallerStep(t, s, "step-none", store.EpicCaller{})
	endCallerStep(t, s, "step-none", callerLater())
	startCallerStep(t, s, "step-running", caller)

	got := listCallerSteps(t, s)
	if ids := callerStepIDs(got); !reflect.DeepEqual(ids, []string{"step-b", "step-a"}) {
		t.Fatalf("CallerSteps = %v, want [step-b step-a]", ids)
	}
	wantCaller := store.CallerSession{ID: "engine-1", Kind: "engine", State: "idle"}
	for _, step := range got {
		if step.Caller != wantCaller {
			t.Errorf("caller of %s = %+v, want %+v", step.Step.ID, step.Caller, wantCaller)
		}
	}
}

func TestCallerEventStoreCallerStepsDropsReadAndMarkedSteps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	addCallerEngines(t, s, "engine-1", "engine-2")
	caller := store.EpicCaller{Session: "engine-1"}
	for _, id := range []string{"step-marked", "step-kept"} {
		startCallerStep(t, s, id, caller)
		endCallerStep(t, s, id, callerLater())
	}

	if err := s.MarkCallerEvent(ctx, "step:step-marked", "engine-1", callerLater()); err != nil {
		t.Fatalf("MarkCallerEvent: %v", err)
	}
	// A delivery to another session leaves the caller's own step listed.
	if err := s.MarkCallerEvent(ctx, "step:step-kept", "engine-2", callerLater()); err != nil {
		t.Fatalf("MarkCallerEvent to another session: %v", err)
	}

	if got := callerStepIDs(listCallerSteps(t, s)); !reflect.DeepEqual(got, []string{"step-kept"}) {
		t.Fatalf("CallerSteps = %v, want [step-kept]", got)
	}
}

func TestCallerEventStoreMarkCallerEventRepeatsWithoutError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	const event, session = "step:step-1", "engine-1"

	if marked, err := s.CallerEventMarked(ctx, event, session); err != nil || marked {
		t.Fatalf("CallerEventMarked before any mark = %v, %v, want false", marked, err)
	}
	for i := range 2 {
		if err := s.MarkCallerEvent(ctx, event, session, callerStart()); err != nil {
			t.Fatalf("MarkCallerEvent %d: %v", i+1, err)
		}
	}
	if marked, err := s.CallerEventMarked(ctx, event, session); err != nil || !marked {
		t.Fatalf("CallerEventMarked after the marks = %v, %v, want true", marked, err)
	}
	if marked, err := s.CallerEventMarked(ctx, event, "engine-2"); err != nil || marked {
		t.Fatalf("CallerEventMarked for another session = %v, %v, want false", marked, err)
	}
}
