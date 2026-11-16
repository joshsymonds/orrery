package store_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// releaseEpic and releaseOther are epics in releaseRepo, so they contend for its one slot. releaseFree is an epic in
// releaseOtherRepo, whose slot the other two must not touch.
const (
	releaseEpic      = "orrery-8b9c0d1/epic-release"
	releaseOther     = "orrery-8b9c0d1/epic-release-other"
	releaseFree      = "orrery-8b9c0d1/epic-release-free"
	releaseRepo      = "/work/synthetic-release-repo"
	releaseOtherRepo = "/work/synthetic-release-other"
)

// releaseRunAt is the second the release tests record and start their runs at.
func releaseRunAt() time.Time {
	return time.Unix(1700002000, 0)
}

// releaseEpicIn returns a synthetic epic with id in phase and repository.
func releaseEpicIn(id, repository, phase string) store.EpicRow {
	row := loopEpicRow(id)
	row.Repository = repository
	row.Phase = phase
	return row
}

// openReleaseStore opens a migrated store with each of epics created, and returns it with a raw handle to the same
// file.
func openReleaseStore(t *testing.T, epics ...store.EpicRow) (*store.Store, *sql.DB) {
	t.Helper()
	s, raw := openNodeStore(t)
	for _, row := range epics {
		if err := s.CreateEpic(context.Background(), row); err != nil {
			t.Fatalf("CreateEpic %s: %v", row.ID, err)
		}
	}
	return s, raw
}

// requirePhase fails unless epic is in phase.
func requirePhase(t *testing.T, s *store.Store, epic, phase string) {
	t.Helper()
	row, err := s.Epic(context.Background(), epic)
	if err != nil {
		t.Fatalf("Epic %s: %v", epic, err)
	}
	if row.Phase != phase {
		t.Errorf("phase of epic %s = %q, want %q", epic, row.Phase, phase)
	}
}

// requireSlot fails unless the release slot of epic's repository is held by want, empty when it is free.
func requireSlot(t *testing.T, s *store.Store, epic, want string) {
	t.Helper()
	flow, err := s.EpicFlow(context.Background(), epic)
	if err != nil {
		t.Fatalf("EpicFlow %s: %v", epic, err)
	}
	if flow.Slot != want {
		t.Errorf("slot seen by epic %s = %q, want %q", epic, flow.Slot, want)
	}
}

// requireReleaseRuns fails unless the release runs EpicFlow returns for epic are want.
func requireReleaseRuns(t *testing.T, s *store.Store, epic string, want []store.ReleaseRun) {
	t.Helper()
	flow, err := s.EpicFlow(context.Background(), epic)
	if err != nil {
		t.Fatalf("EpicFlow %s: %v", epic, err)
	}
	if !reflect.DeepEqual(flow.Releases, want) {
		t.Errorf("release runs of epic %s = %+v, want %+v", epic, flow.Releases, want)
	}
}

// TestRecordReleaseStoresARunOnce checks that RecordRelease stores a run under its epic, part, position and attempt,
// that a repeat of that key with other fields changes nothing, and that the same part at another attempt is stored.
func TestRecordReleaseStoresARunOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t, releaseEpicIn(releaseEpic, releaseRepo, store.PhaseReleasing))

	first := store.ReleaseRun{
		Part: store.ReleasePartStep, Position: 1, Attempt: 1, Step: "push tag", Start: "aaa111", Head: "bbb222",
		End: "ccc333", Commits: []string{"bbb222"}, Evidence: "run-1", Passed: true, At: releaseRunAt(),
	}
	if err := s.RecordRelease(ctx, releaseEpic, first); err != nil {
		t.Fatalf("RecordRelease: %v", err)
	}
	repeat := first
	repeat.Passed = false
	repeat.Reason = "changed"
	repeat.Head = "ddd444"
	if err := s.RecordRelease(ctx, releaseEpic, repeat); err != nil {
		t.Fatalf("repeat RecordRelease: %v", err)
	}
	other := first
	other.Attempt = 2
	other.Passed = false
	other.Reason = "retried"
	if err := s.RecordRelease(ctx, releaseEpic, other); err != nil {
		t.Fatalf("RecordRelease at attempt 2: %v", err)
	}

	requireReleaseRuns(t, s, releaseEpic, []store.ReleaseRun{first, other})
}

// TestTakeReleaseSlotHoldsForCleanAndReleasingEpics checks that a clean epic takes its repository's free slot and moves
// to releasing in the same write, that another clean epic of the repository is handed the holder and stays clean, that
// the holder's repeat changes nothing, and that an epic of another repository takes that repository's slot.
func TestTakeReleaseSlotHoldsForCleanAndReleasingEpics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t,
		releaseEpicIn(releaseEpic, releaseRepo, store.PhaseClean),
		releaseEpicIn(releaseOther, releaseRepo, store.PhaseClean),
		releaseEpicIn(releaseFree, releaseOtherRepo, store.PhaseClean),
	)

	holder, err := s.TakeReleaseSlot(ctx, releaseEpic)
	if err != nil {
		t.Fatalf("TakeReleaseSlot of %s: %v", releaseEpic, err)
	}
	if holder != releaseEpic {
		t.Errorf("TakeReleaseSlot of %s = %q, want %q", releaseEpic, holder, releaseEpic)
	}
	requirePhase(t, s, releaseEpic, store.PhaseReleasing)

	holder, err = s.TakeReleaseSlot(ctx, releaseOther)
	if err != nil {
		t.Fatalf("TakeReleaseSlot of %s: %v", releaseOther, err)
	}
	if holder != releaseEpic {
		t.Errorf("TakeReleaseSlot of %s = %q, want the holder %q", releaseOther, holder, releaseEpic)
	}
	requirePhase(t, s, releaseOther, store.PhaseClean)

	holder, err = s.TakeReleaseSlot(ctx, releaseEpic)
	if err != nil {
		t.Fatalf("repeat TakeReleaseSlot of %s: %v", releaseEpic, err)
	}
	if holder != releaseEpic {
		t.Errorf("repeat TakeReleaseSlot of %s = %q, want %q", releaseEpic, holder, releaseEpic)
	}
	requirePhase(t, s, releaseEpic, store.PhaseReleasing)

	holder, err = s.TakeReleaseSlot(ctx, releaseFree)
	if err != nil {
		t.Fatalf("TakeReleaseSlot of %s: %v", releaseFree, err)
	}
	if holder != releaseFree {
		t.Errorf("TakeReleaseSlot of %s = %q, want %q", releaseFree, holder, releaseFree)
	}
	requireSlot(t, s, releaseOther, releaseEpic)
}

// TestTakeReleaseSlotStoppedRetakesFreeSlotWithoutPhaseChange checks that a stopped epic is handed the holder while
// another epic holds the repository's slot, and takes the slot once it is free, with its phase unchanged.
func TestTakeReleaseSlotStoppedRetakesFreeSlotWithoutPhaseChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t,
		releaseEpicIn(releaseEpic, releaseRepo, store.PhaseClean),
		releaseEpicIn(releaseOther, releaseRepo, store.PhaseStopped),
	)
	if _, err := s.TakeReleaseSlot(ctx, releaseEpic); err != nil {
		t.Fatalf("TakeReleaseSlot of %s: %v", releaseEpic, err)
	}

	holder, err := s.TakeReleaseSlot(ctx, releaseOther)
	if err != nil {
		t.Fatalf("TakeReleaseSlot of stopped %s: %v", releaseOther, err)
	}
	if holder != releaseEpic {
		t.Errorf("TakeReleaseSlot of stopped %s = %q, want the holder %q", releaseOther, holder, releaseEpic)
	}

	if err := s.FreeReleaseSlot(ctx, releaseEpic); err != nil {
		t.Fatalf("FreeReleaseSlot of %s: %v", releaseEpic, err)
	}
	holder, err = s.TakeReleaseSlot(ctx, releaseOther)
	if err != nil {
		t.Fatalf("TakeReleaseSlot of stopped %s after the free: %v", releaseOther, err)
	}
	if holder != releaseOther {
		t.Errorf("TakeReleaseSlot of stopped %s = %q, want %q", releaseOther, holder, releaseOther)
	}
	requirePhase(t, s, releaseOther, store.PhaseStopped)
	requireSlot(t, s, releaseEpic, releaseOther)
}

// TestTakeReleaseSlotTakesNothingElse checks that an epic in another phase takes no slot, and that an id no epic has
// is an error matching ErrNoEpic.
func TestTakeReleaseSlotTakesNothingElse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t, releaseEpicIn(releaseEpic, releaseRepo, "accepted"))

	holder, err := s.TakeReleaseSlot(ctx, releaseEpic)
	if err != nil {
		t.Fatalf("TakeReleaseSlot of accepted %s: %v", releaseEpic, err)
	}
	if holder != "" {
		t.Errorf("TakeReleaseSlot of accepted %s = %q, want no holder", releaseEpic, holder)
	}
	requirePhase(t, s, releaseEpic, "accepted")
	requireSlot(t, s, releaseEpic, "")

	if _, err := s.TakeReleaseSlot(ctx, "orrery-8b9c0d1/no-such-epic"); !errors.Is(err, store.ErrNoEpic) {
		t.Errorf("TakeReleaseSlot of an unknown epic: err = %v, want ErrNoEpic", err)
	}
}

// TestFreeReleaseSlotRemovesOnlyTheSlotItHolds checks that FreeReleaseSlot by an epic that holds no slot leaves the
// slot its holder took as it is, and that the holder's FreeReleaseSlot frees it.
func TestFreeReleaseSlotRemovesOnlyTheSlotItHolds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t,
		releaseEpicIn(releaseEpic, releaseRepo, store.PhaseClean),
		releaseEpicIn(releaseOther, releaseRepo, store.PhaseClean),
	)
	if _, err := s.TakeReleaseSlot(ctx, releaseEpic); err != nil {
		t.Fatalf("TakeReleaseSlot of %s: %v", releaseEpic, err)
	}

	if err := s.FreeReleaseSlot(ctx, releaseOther); err != nil {
		t.Fatalf("FreeReleaseSlot of %s: %v", releaseOther, err)
	}
	requireSlot(t, s, releaseEpic, releaseEpic)

	if err := s.FreeReleaseSlot(ctx, releaseEpic); err != nil {
		t.Fatalf("FreeReleaseSlot of %s: %v", releaseEpic, err)
	}
	requireSlot(t, s, releaseEpic, "")
}

// TestStopReleaseParksRunWithOneTellAndStops checks that StopRelease parks the running release run at its path and
// attempt with outcome failed and the reason as its output, parks the epic with the reason and adds it as one tell,
// and moves the epic from releasing to stopped. A repeat changes nothing and adds no tell.
func TestStopReleaseParksRunWithOneTellAndStops(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openReleaseStore(t, releaseEpicIn(releaseEpic, releaseRepo, store.PhaseClean))
	if _, err := s.TakeReleaseSlot(ctx, releaseEpic); err != nil {
		t.Fatalf("TakeReleaseSlot of %s: %v", releaseEpic, err)
	}
	run := store.NodeRun{Path: "release", ID: "id-release", Node: "release", Kind: "release", Attempt: 1}
	if err := s.StartNodes(ctx, releaseEpic, []store.NodeRun{run}, nil, releaseRunAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}

	const reason = "push rejected"
	if err := s.StopRelease(ctx, releaseEpic, "release", 1, reason, releaseRunAt()); err != nil {
		t.Fatalf("StopRelease: %v", err)
	}
	if err := s.StopRelease(ctx, releaseEpic, "release", 1, reason, releaseRunAt()); err != nil {
		t.Fatalf("repeat StopRelease: %v", err)
	}

	parked := run
	parked.State = store.NodeParked
	parked.Outcome = "failed"
	parked.Output = reason
	parked.Started = releaseRunAt()
	requireNodeRuns(t, s, releaseEpic, []store.NodeRun{parked})
	if got := parkedOf(t, raw, releaseEpic); got != reason {
		t.Errorf("parked reason of epic %s = %q, want %q", releaseEpic, got, reason)
	}
	if want := []string{reason}; !reflect.DeepEqual(tellsOf(t, raw, releaseEpic), want) {
		t.Errorf("tells of epic %s = %q, want %q", releaseEpic, tellsOf(t, raw, releaseEpic), want)
	}
	requirePhase(t, s, releaseEpic, store.PhaseStopped)
}

// TestEpicFlowReadsReleaseRunsSlotAndReleaseSteps checks that EpicFlow returns the release runs of its epic ordered by
// attempt, part and position, the holder of its repository's slot, and for its release node run the steps stored under
// that run's attempt for each release step and for the judge, and no step of another attempt.
func TestEpicFlowReadsReleaseRunsSlotAndReleaseSteps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	row := releaseEpicIn(releaseEpic, releaseRepo, store.PhaseClean)
	row.ReleaseSteps = []store.EpicReleaseStep{
		{Step: "push tag", Target: "origin", Undo: "delete tag"},
		{Step: "publish", Target: "registry", Undo: "yank"},
	}
	s, _ := openReleaseStore(t, row)
	if _, err := s.TakeReleaseSlot(ctx, releaseEpic); err != nil {
		t.Fatalf("TakeReleaseSlot of %s: %v", releaseEpic, err)
	}

	runs := []store.ReleaseRun{
		{Part: store.ReleasePartStep, Position: 2, Attempt: 1, Step: "publish", Evidence: "run-2", Passed: true,
			At: releaseRunAt()},
		{Part: store.ReleasePartStep, Position: 1, Attempt: 2, Step: "push tag", Evidence: "run-3", Passed: true,
			At: releaseRunAt()},
		{Part: store.ReleasePartCandidate, Position: 1, Attempt: 1, Head: "bbb222", Commits: []string{"bbb222"},
			Evidence: "run-0", Passed: true, At: releaseRunAt()},
		{Part: store.ReleasePartJudge, Position: 1, Attempt: 1, Evidence: "run-1", Passed: false,
			Reason: "the release is not whole", At: releaseRunAt()},
		{Part: store.ReleasePartStep, Position: 1, Attempt: 1, Step: "push tag", Evidence: "run-1", Passed: true,
			At: releaseRunAt()},
	}
	for _, run := range runs {
		if err := s.RecordRelease(ctx, releaseEpic, run); err != nil {
			t.Fatalf("RecordRelease %s %d at attempt %d: %v", run.Part, run.Position, run.Attempt, err)
		}
	}
	want := []store.ReleaseRun{runs[2], runs[3], runs[4], runs[0], runs[1]}
	requireReleaseRuns(t, s, releaseEpic, want)
	requireSlot(t, s, releaseEpic, releaseEpic)

	steps := addReleaseSteps(t, s, releaseEpic)
	flow, err := s.EpicFlow(ctx, releaseEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if !reflect.DeepEqual(flow.Steps, steps) {
		t.Errorf("EpicFlow steps = %+v, want the release steps %+v", flow.Steps, steps)
	}
}

// addReleaseSteps starts a release node run of epic at attempt 1, stores a step under each of its parts `release 1`,
// `release 2` and `judge`, and stores one more under `release 1` at attempt 2, which EpicFlow must not read. It returns
// the three steps of attempt 1 in that order.
func addReleaseSteps(t *testing.T, s *store.Store, epic string) []store.StepRow {
	t.Helper()
	ctx := context.Background()
	run := store.NodeRun{Path: "release", ID: "id-release", Node: "release", Kind: "release", Attempt: 1}
	if err := s.StartNodes(ctx, epic, []store.NodeRun{run}, nil, releaseRunAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	addStep := func(id string) {
		t.Helper()
		if err := s.ApplyEngine(ctx, []store.EngineOp{checkStepAdd(id)}, checkTime(0)); err != nil {
			t.Fatalf("ApplyEngine adding step %s: %v", id, err)
		}
	}
	want := make([]store.StepRow, 0, 3)
	for _, part := range []string{"release 1", "release 2", "judge"} {
		id := store.NodeWorkID("id-release", 1, part)
		addStep(id)
		step, err := s.Step(ctx, id)
		if err != nil {
			t.Fatalf("Step %s: %v", id, err)
		}
		want = append(want, step)
	}
	addStep(store.NodeWorkID("id-release", 2, "release 1"))
	return want
}
