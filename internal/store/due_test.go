package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// dueResetA is the earlier known reset of the due tests and dueResetB a later one, as unix seconds.
const (
	dueResetA int64 = 1_700_003_600
	dueResetB int64 = 1_700_007_200
)

// The ids of the due rows the tests expect. Each is the lowercase sha256 of the namespace, kind, target and reset
// joined with NUL, computed outside the package so the tests do not repeat the derivation they check.
const (
	dueStepS1A = "a871c9616a431ad87493113e632ebf69edfd845012f6fdf868f02f3d545944c2"
	dueStepS1B = "c6140e4389c001be725377d4a78e54c2343968b8a12322214048f22518eaf926"
	dueStepS2A = "1cc74325c6ffbd12c23f3ac9bd2f64b2a13238c4f2711be3054bc883ab4a266d"
)

// stepLimitAt is the StepLoop that limits step id by a usage limit that resets at the unix second resets.
func stepLimitAt(id string, resets int64) StepLoop {
	return StepLoop{ID: id, Limited: true, LimitKind: "usage", LimitResets: time.Unix(resets, 0)}
}

// openDueStore opens a migrated store that holds the steps s1 and s2.
func openDueStore(t *testing.T) *Store {
	t.Helper()
	s := openSyncStore(t)
	applyDue(t, s, stepAddOf("s1"), stepAddOf("s2"))
	return s
}

// applyDue applies ops to s in one write at the engine's test time, failing the test on error.
func applyDue(t *testing.T, s *Store, ops ...EngineOp) {
	t.Helper()
	if err := s.ApplyEngine(context.Background(), ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

// dueIDs returns the ids of the due rows of s in listing order, failing the test on error.
func dueIDs(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.DueResumes(context.Background())
	if err != nil {
		t.Fatalf("DueResumes: %v", err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

// dueStateOf returns the state of the due row id, failing the test when there is no such row.
func dueStateOf(t *testing.T, s *Store, id string) string {
	t.Helper()
	row, err := s.fixtureQueries().GetDueResume(context.Background(), id)
	if err != nil {
		t.Fatalf("GetDueResume %s: %v", id, err)
	}
	return row.State
}

func TestLimitedStepLoopStoresItsDueRowInTheSameWrite(t *testing.T) {
	t.Parallel()
	s := openDueStore(t)
	applyDue(t, s, stepLimitAt("s1", dueResetA))

	if got := dueIDs(t, s); !reflect.DeepEqual(got, []string{dueStepS1A}) {
		t.Fatalf("due rows after the limit = %v, want [%s]", got, dueStepS1A)
	}
	row, err := s.fixtureQueries().GetDueResume(context.Background(), dueStepS1A)
	if err != nil {
		t.Fatalf("GetDueResume: %v", err)
	}
	want := sqlcgen.DueResume{ID: dueStepS1A, Kind: "step", Target: "s1", Resets: dueResetA, State: "due"}
	if row != want {
		t.Fatalf("GetDueResume = %+v, want %+v", row, want)
	}
}

func TestDueRowRollsBackWithAFailedLimitWrite(t *testing.T) {
	t.Parallel()
	s := openDueStore(t)
	err := s.ApplyEngine(context.Background(), []EngineOp{
		stepLimitAt("s1", dueResetA),
		stepLimitAt("missing", dueResetA),
	}, engineNowAt())
	if !errors.Is(err, ErrNoStep) {
		t.Fatalf("ApplyEngine with a missing step = %v, want ErrNoStep", err)
	}
	if got := dueIDs(t, s); len(got) != 0 {
		t.Fatalf("due rows after the failed write = %v, want none", got)
	}
}

func TestLimitedStepLoopWithSameResetAddsNoSecondDueRow(t *testing.T) {
	t.Parallel()
	s := openDueStore(t)
	applyDue(t, s, stepLimitAt("s1", dueResetA))
	applyDue(t, s, stepLimitAt("s1", dueResetA))

	if got := dueIDs(t, s); !reflect.DeepEqual(got, []string{dueStepS1A}) {
		t.Fatalf("due rows after the same limit twice = %v, want [%s]", got, dueStepS1A)
	}
}

func TestLimitedStepLoopWithEarlierOrEqualResetAddsNoDueRow(t *testing.T) {
	t.Parallel()
	s := openDueStore(t)
	applyDue(t, s, stepLimitAt("s1", dueResetB))
	applyDue(t, s, stepLimitAt("s1", dueResetA))
	if got := dueIDs(t, s); !reflect.DeepEqual(got, []string{dueStepS1B}) {
		t.Fatalf("due rows after an earlier reset = %v, want [%s]", got, dueStepS1B)
	}

	applyDue(t, s, stepLimitAt("s1", dueResetB))
	if got := dueIDs(t, s); !reflect.DeepEqual(got, []string{dueStepS1B}) {
		t.Fatalf("due rows after an equal reset = %v, want [%s]", got, dueStepS1B)
	}
}

func TestLimitedStepLoopWithLaterResetAddsSecondDueRow(t *testing.T) {
	t.Parallel()
	s := openDueStore(t)
	applyDue(t, s, stepLimitAt("s1", dueResetA))
	applyDue(t, s, stepLimitAt("s1", dueResetB))

	if got := dueIDs(t, s); !reflect.DeepEqual(got, []string{dueStepS1A, dueStepS1B}) {
		t.Fatalf("due rows after a later reset = %v, want [%s %s]", got, dueStepS1A, dueStepS1B)
	}
}

func TestStepLoopWithUnknownResetOrNoLimitAddsNoDueRow(t *testing.T) {
	t.Parallel()
	s := openDueStore(t)
	applyDue(t, s, StepLoop{ID: "s1", Limited: true, LimitKind: "usage"})
	applyDue(t, s, StepLoop{ID: "s1", LimitResets: time.Unix(dueResetA, 0)})

	if got := dueIDs(t, s); len(got) != 0 {
		t.Fatalf("due rows for an unknown reset or no limit = %v, want none", got)
	}
}

// TestClosedDueRowStaysClosedForItsReset is the once-per-reset rule: a limit that repeats a reset whose row was
// closed adds no row, so the step is not resumed again before its real reset.
func TestClosedDueRowStaysClosedForItsReset(t *testing.T) {
	t.Parallel()
	s := openDueStore(t)
	applyDue(t, s, stepLimitAt("s1", dueResetA))
	if err := s.CloseDueResume(context.Background(), dueStepS1A); err != nil {
		t.Fatalf("CloseDueResume: %v", err)
	}
	applyDue(t, s, stepLimitAt("s1", dueResetA))

	if got := dueIDs(t, s); len(got) != 0 {
		t.Fatalf("due rows after closing and repeating the reset = %v, want none", got)
	}
	if state := dueStateOf(t, s, dueStepS1A); state != "done" {
		t.Fatalf("state of the closed row = %q, want done", state)
	}
}

func TestDueResumesListOnlyDueRowsOldestResetFirst(t *testing.T) {
	t.Parallel()
	s := openDueStore(t)
	applyDue(t, s, stepLimitAt("s1", dueResetB))
	applyDue(t, s, stepLimitAt("s2", dueResetA))

	if got := dueIDs(t, s); !reflect.DeepEqual(got, []string{dueStepS2A, dueStepS1B}) {
		t.Fatalf("due rows = %v, want the oldest reset first [%s %s]", got, dueStepS2A, dueStepS1B)
	}

	if err := s.CloseDueResume(context.Background(), dueStepS2A); err != nil {
		t.Fatalf("CloseDueResume: %v", err)
	}
	if got := dueIDs(t, s); !reflect.DeepEqual(got, []string{dueStepS1B}) {
		t.Fatalf("due rows after closing %s = %v, want [%s]", dueStepS2A, got, dueStepS1B)
	}
	if state := dueStateOf(t, s, dueStepS2A); state != "done" {
		t.Fatalf("state of %s after close = %q, want done", dueStepS2A, state)
	}
}

func TestStepEndClosesOnlyThatStepsDueRows(t *testing.T) {
	t.Parallel()
	s := openDueStore(t)
	applyDue(t, s, stepLimitAt("s1", dueResetA))
	applyDue(t, s, stepLimitAt("s1", dueResetB))
	applyDue(t, s, stepLimitAt("s2", dueResetA))
	applyDue(t, s, StepEnd{ID: "s1", End: "done", Status: "passed", Reason: "gate green"})

	if got := dueIDs(t, s); !reflect.DeepEqual(got, []string{dueStepS2A}) {
		t.Fatalf("due rows after s1 ends = %v, want [%s]", got, dueStepS2A)
	}
	for _, id := range []string{dueStepS1A, dueStepS1B} {
		if state := dueStateOf(t, s, id); state != "done" {
			t.Fatalf("state of s1's row %s = %q, want done", id, state)
		}
	}
}

// TestStepDueClosedByEngineOpMarksOnlyThatStepsRowsDone is the step-close engine op: it closes every due row of the
// step without naming their ids, and leaves another step's rows due.
func TestStepDueClosedByEngineOpMarksOnlyThatStepsRowsDone(t *testing.T) {
	t.Parallel()
	s := openDueStore(t)
	applyDue(t, s, stepLimitAt("s1", dueResetA))
	applyDue(t, s, stepLimitAt("s1", dueResetB))
	applyDue(t, s, stepLimitAt("s2", dueResetA))
	applyDue(t, s, StepDueResumesClosed{ID: "s1"})

	if got := dueIDs(t, s); !reflect.DeepEqual(got, []string{dueStepS2A}) {
		t.Fatalf("due rows after closing s1's rows = %v, want [%s]", got, dueStepS2A)
	}
	for _, id := range []string{dueStepS1A, dueStepS1B} {
		if state := dueStateOf(t, s, id); state != "done" {
			t.Fatalf("state of s1's row %s = %q, want done", id, state)
		}
	}
}

// TestLimitAfterStepEndAddsNoDueRow covers a limit that reaches the store after the step has ended: StepEnd already
// closed the step's rows, so a row stored now would stay due for a step that is finished.
func TestLimitAfterStepEndAddsNoDueRow(t *testing.T) {
	t.Parallel()
	s := openDueStore(t)
	applyDue(t, s, StepEnd{ID: "s1", End: "done", Status: "passed", Reason: "gate green"})
	applyDue(t, s, stepLimitAt("s1", dueResetA))

	if got := dueIDs(t, s); len(got) != 0 {
		t.Fatalf("due rows for a limit after the step ended = %v, want none", got)
	}
}

func TestEpicPauseSourceIsSetAndClearedByResume(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	seedEpic(t, fixtureDB{s: s})
	q := s.fixtureQueries()

	if err := q.PauseEpic(ctx, seededEpic); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	source := sqlcgen.SetEpicPauseSourceParams{PauseSource: "limit", ID: seededEpic}
	if err := q.SetEpicPauseSource(ctx, source); err != nil {
		t.Fatalf("SetEpicPauseSource: %v", err)
	}
	epic, err := q.GetEpic(ctx, seededEpic)
	if err != nil {
		t.Fatalf("GetEpic: %v", err)
	}
	if epic.PauseSource != "limit" || epic.Paused != 1 {
		t.Fatalf("epic after set = paused %d source %q, want paused 1 source limit", epic.Paused, epic.PauseSource)
	}

	if err := q.ResumeEpic(ctx, seededEpic); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	epic, err = q.GetEpic(ctx, seededEpic)
	if err != nil {
		t.Fatalf("GetEpic after resume: %v", err)
	}
	if epic.PauseSource != "" || epic.Paused != 0 {
		t.Fatalf("epic after resume = paused %d source %q, want paused 0 source empty", epic.Paused, epic.PauseSource)
	}
}

func TestEpicPauseSourceRejectsAnUnknownSource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	seedEpic(t, fixtureDB{s: s})

	err := s.fixtureQueries().SetEpicPauseSource(ctx, sqlcgen.SetEpicPauseSourceParams{
		PauseSource: "weekly", ID: seededEpic,
	})
	if err == nil {
		t.Fatalf("SetEpicPauseSource with source weekly: nil error, want the CHECK to reject it")
	}
}
