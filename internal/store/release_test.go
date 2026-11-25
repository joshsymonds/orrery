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

// releaseEpic and releaseOther are epics in releaseRepo. releaseFree is an epic in releaseOtherRepo.
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

// TestStartReleaseMovesEachCleanEpicAtOnce checks that each clean epic moves to releasing in its own write, whichever
// other epic of its repository already releases, and that a repeat of a start changes nothing.
func TestStartReleaseMovesEachCleanEpicAtOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t,
		releaseEpicIn(releaseEpic, releaseRepo, store.PhaseClean),
		releaseEpicIn(releaseOther, releaseRepo, store.PhaseClean),
	)

	if err := s.StartRelease(ctx, releaseEpic, releaseRunAt()); err != nil {
		t.Fatalf("StartRelease of %s: %v", releaseEpic, err)
	}
	requirePhase(t, s, releaseEpic, store.PhaseReleasing)

	if err := s.StartRelease(ctx, releaseOther, releaseRunAt()); err != nil {
		t.Fatalf("StartRelease of %s: %v", releaseOther, err)
	}
	requirePhase(t, s, releaseOther, store.PhaseReleasing)

	if err := s.StartRelease(ctx, releaseEpic, releaseRunAt()); err != nil {
		t.Fatalf("repeat StartRelease of %s: %v", releaseEpic, err)
	}
	requirePhase(t, s, releaseEpic, store.PhaseReleasing)
}

// TestStartReleaseLeavesOtherPhasesAndRefusesAnUnknownEpic checks that StartRelease leaves an epic in any phase but
// clean in its phase, including a releasing one, and that an id no epic has is an error matching ErrNoEpic.
func TestStartReleaseLeavesOtherPhasesAndRefusesAnUnknownEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t,
		releaseEpicIn(releaseEpic, releaseRepo, "accepted"),
		releaseEpicIn(releaseOther, releaseRepo, store.PhaseStopped),
		releaseEpicIn(releaseFree, releaseOtherRepo, store.PhaseReleasing),
	)

	for _, epic := range []string{releaseEpic, releaseOther, releaseFree} {
		if err := s.StartRelease(ctx, epic, releaseRunAt()); err != nil {
			t.Fatalf("StartRelease of %s: %v", epic, err)
		}
	}
	requirePhase(t, s, releaseEpic, "accepted")
	requirePhase(t, s, releaseOther, store.PhaseStopped)
	requirePhase(t, s, releaseFree, store.PhaseReleasing)

	err := s.StartRelease(ctx, "orrery-8b9c0d1/no-such-epic", releaseRunAt())
	if !errors.Is(err, store.ErrNoEpic) {
		t.Errorf("StartRelease of an unknown epic: err = %v, want ErrNoEpic", err)
	}
}

// TestStopReleaseParksRunWithOneTellAndStops checks that StopRelease parks the running release run at its path and
// attempt with outcome failed and the reason as its output, parks the epic with the reason as the release_step class and
// adds no tell, and moves the epic from releasing to stopped. A repeat changes nothing and adds no tell.
func TestStopReleaseParksRunWithOneTellAndStops(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, raw := openReleaseStore(t, releaseEpicIn(releaseEpic, releaseRepo, store.PhaseClean))
	if err := s.StartRelease(ctx, releaseEpic, releaseRunAt()); err != nil {
		t.Fatalf("StartRelease of %s: %v", releaseEpic, err)
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
	var class string
	if err := raw.QueryRow(`SELECT park_class FROM epics WHERE id = ?`, releaseEpic).Scan(&class); err != nil {
		t.Fatalf("read park_class of epic %s: %v", releaseEpic, err)
	}
	if want := string(store.ParkClassReleaseStep); class != want {
		t.Errorf("park class of epic %s = %q, want %q", releaseEpic, class, want)
	}
	if got := tellsOf(t, raw, releaseEpic); len(got) != 0 {
		t.Errorf("tells of epic %s = %q, want none: a release_step park is the planner's", releaseEpic, got)
	}
	if got := parkTriggerCauses(t, raw, releaseEpic); !reflect.DeepEqual(got, [][]any{{reason}}) {
		t.Errorf("park triggers of epic %s = %v, want one with cause %q", releaseEpic, got, reason)
	}
	requirePhase(t, s, releaseEpic, store.PhaseStopped)
}

// TestEpicFlowReadsReleaseRunsAndReleaseSteps checks that EpicFlow returns the release runs of its epic ordered by
// attempt, part and position, and for its release node run the steps stored under that run's attempt for each release
// step and for the judge, and no step of another attempt.
func TestEpicFlowReadsReleaseRunsAndReleaseSteps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	row := releaseEpicIn(releaseEpic, releaseRepo, store.PhaseClean)
	row.ReleaseSteps = []store.EpicReleaseStep{
		{Step: "push tag", Target: "origin", Undo: "delete tag"},
		{Step: "publish", Target: "registry", Undo: "yank"},
	}
	s, _ := openReleaseStore(t, row)
	if err := s.StartRelease(ctx, releaseEpic, releaseRunAt()); err != nil {
		t.Fatalf("StartRelease of %s: %v", releaseEpic, err)
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

	steps := addReleaseSteps(t, s, releaseEpic)
	flow, err := s.EpicFlow(ctx, releaseEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if !reflect.DeepEqual(flow.Steps, steps) {
		t.Errorf("EpicFlow steps = %+v, want the release steps %+v", flow.Steps, steps)
	}
}

// TestReleaseProofsReadsEachStepFromItsStoredRuns checks that ReleaseProofs gives each release step of an epic in
// contract order the state its stored release runs give it: passed with the evidence and attempt of its passed run,
// failed with the reason and evidence of its highest-attempt run, running while the running release node run has its
// work step with no end, and waiting with no run. A judge run decides no step.
func TestReleaseProofsReadsEachStepFromItsStoredRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	row := releaseEpicIn(releaseEpic, releaseRepo, store.PhaseReleasing)
	row.ReleaseSteps = []store.EpicReleaseStep{
		{Step: "push tag", Target: "origin", Undo: "delete tag"},
		{Step: "publish", Target: "registry", Undo: "yank"},
		{Step: "announce", Target: "mailing list", Undo: "retract"},
		{Step: "close issue", Target: "tracker", Undo: "reopen"},
	}
	s, _ := openReleaseStore(t, row)

	failedAt := releaseRunAt()
	laterAt := time.Unix(1700002060, 0)
	runs := []store.ReleaseRun{
		{Part: store.ReleasePartStep, Position: 1, Attempt: 1, Step: "push tag", Evidence: "run-11",
			Reason: "push rejected", At: failedAt},
		{Part: store.ReleasePartStep, Position: 1, Attempt: 2, Step: "push tag", Evidence: "run-12", Passed: true,
			At: laterAt},
		{Part: store.ReleasePartStep, Position: 2, Attempt: 1, Step: "publish", Evidence: "run-21",
			Reason: "registry refused", At: failedAt},
		{Part: store.ReleasePartStep, Position: 2, Attempt: 2, Step: "publish", Evidence: "run-22",
			Reason: "registry refused again", At: laterAt},
		{Part: store.ReleasePartJudge, Position: 4, Attempt: 1, Evidence: "run-judge",
			Reason: "the release is not whole", At: failedAt},
	}
	for _, record := range runs {
		if err := s.RecordRelease(ctx, releaseEpic, record); err != nil {
			t.Fatalf("RecordRelease %s %d at attempt %d: %v", record.Part, record.Position, record.Attempt, err)
		}
	}

	node := store.NodeRun{Path: "release", ID: "id-release", Node: "release", Kind: "release", Attempt: 2}
	if err := s.StartNodes(ctx, releaseEpic, []store.NodeRun{node}, nil, releaseRunAt()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	id := store.NodeWorkID("id-release", 2, "release 3")
	if err := s.ApplyEngine(ctx, []store.EngineOp{checkStepAdd(id)}, checkTime(0)); err != nil {
		t.Fatalf("ApplyEngine adding step %s: %v", id, err)
	}

	flow, err := s.EpicFlow(ctx, releaseEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	want := []store.ReleaseProof{
		{Position: 1, Step: "push tag", Target: "origin", Undo: "delete tag", State: "passed", Evidence: "run-12",
			Attempt: 2, At: laterAt},
		{Position: 2, Step: "publish", Target: "registry", Undo: "yank", State: "failed", Evidence: "run-22",
			Reason: "registry refused again", Attempt: 2, At: laterAt},
		{Position: 3, Step: "announce", Target: "mailing list", Undo: "retract", State: "running"},
		{Position: 4, Step: "close issue", Target: "tracker", Undo: "reopen", State: "waiting"},
	}
	if got := store.ReleaseProofs(flow); !reflect.DeepEqual(got, want) {
		t.Errorf("ReleaseProofs of epic %s = %+v, want %+v", releaseEpic, got, want)
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
