package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// insertStaleDueRow inserts the due row of claimEpic for dueResetA without pausing the epic. It stands for a row left
// due after its epic was resumed or stopped, the state the store held before the owner's controls closed due rows.
func insertStaleDueRow(t *testing.T, s *Store) {
	t.Helper()
	err := sqlcgen.New(s.db).InsertDueResume(context.Background(), sqlcgen.InsertDueResumeParams{
		ID: dueEpicA, Kind: dueKindEpic, Target: claimEpic, Resets: dueResetA,
	})
	if err != nil {
		t.Fatalf("InsertDueResume %s: %v", dueEpicA, err)
	}
}

// limitedWithTwoDueRowsOf creates claimEpic and leaves its epic due row for dueResetA behind, then frees a turn as a
// usage limit that resets at dueResetB. The epic is limit-paused and both of its due rows are due.
func limitedWithTwoDueRowsOf(t *testing.T, s *Store) {
	t.Helper()
	createEpic(t, s, fullEpic(claimEpic))
	turn := claimTurnOf(t, s)
	insertStaleDueRow(t, s)
	freeLimitOf(t, s, turn, time.Unix(dueResetB, 0))
}

func TestPauseEpicRecordsTheOwnerSource(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))

	if err := s.PauseEpic(context.Background(), claimEpic); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	if got := pauseSourceOf(t, s); got != "owner" {
		t.Errorf("pause source after PauseEpic = %q, want owner", got)
	}
}

func TestPauseEpicOverALimitPauseIsAcceptedAndClosesItsDueRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	freeLimitOf(t, s, claimTurnOf(t, s), time.Unix(dueResetA, 0))

	if err := s.PauseEpic(ctx, claimEpic); err != nil {
		t.Fatalf("PauseEpic over a limit pause: %v", err)
	}
	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after PauseEpic over a limit pause = %d, want 1", paused)
	}
	if got := pauseSourceOf(t, s); got != "owner" {
		t.Errorf("pause source after PauseEpic over a limit pause = %q, want owner", got)
	}
	if got := dueStateOf(t, s, dueEpicA); got != "done" {
		t.Errorf("state of the due row after PauseEpic = %q, want done", got)
	}

	resumed, err := s.ResumeDueEpic(ctx, dueEpicA)
	if err != nil {
		t.Fatalf("ResumeDueEpic: %v", err)
	}
	if resumed {
		t.Errorf("ResumeDueEpic of the owner-paused epic reported a resume, want none")
	}
	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after the due-row resume = %d, want 1", paused)
	}
	if got := pauseSourceOf(t, s); got != "owner" {
		t.Errorf("pause source after the due-row resume = %q, want owner", got)
	}
}

func TestPauseEpicRefusesASecondOwnerPauseAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	if err := s.PauseEpic(ctx, claimEpic); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	insertStaleDueRow(t, s)

	before := controlRows(t, s)
	requireRefusal(t, s.PauseEpic(ctx, claimEpic), "epic "+claimEpic+" is already paused")
	if got := controlRows(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("rows after refused PauseEpic = %v, want %v", got, before)
	}
	if got := pauseSourceOf(t, s); got != "owner" {
		t.Errorf("pause source after refused PauseEpic = %q, want owner", got)
	}
	if got := dueStateOf(t, s, dueEpicA); got != "due" {
		t.Errorf("state of the due row after refused PauseEpic = %q, want due", got)
	}
}

func TestStopEpicRecordsTheOwnerSourceAndClosesTheDueRows(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	claimTurnOf(t, s)
	insertStaleDueRow(t, s)

	if _, err := s.StopEpic(context.Background(), claimEpic); err != nil {
		t.Fatalf("StopEpic: %v", err)
	}
	if got := pauseSourceOf(t, s); got != "owner" {
		t.Errorf("pause source after StopEpic = %q, want owner", got)
	}
	if got := dueStateOf(t, s, dueEpicA); got != "done" {
		t.Errorf("state of the due row after StopEpic = %q, want done", got)
	}
}

func TestResumeEpicClosesTheDueRowsOfTheEpic(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	limitedWithTwoDueRowsOf(t, s)

	if err := s.ResumeEpic(context.Background(), claimEpic); err != nil {
		t.Fatalf("ResumeEpic: %v", err)
	}
	if paused, _ := epicFlags(t, s); paused != 0 {
		t.Errorf("paused after ResumeEpic = %d, want 0", paused)
	}
	if got := dueIDs(t, s); len(got) != 0 {
		t.Errorf("due rows after ResumeEpic = %v, want none", got)
	}
	for _, id := range []string{dueEpicA, dueEpicB} {
		if got := dueStateOf(t, s, id); got != "done" {
			t.Errorf("state of %s after ResumeEpic = %q, want done", id, got)
		}
	}
}

func TestResumeDueEpicResumesALimitPausedEpicAndMarksItsRowDone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	freeLimitOf(t, s, claimTurnOf(t, s), time.Unix(dueResetA, 0))

	resumed, err := s.ResumeDueEpic(ctx, dueEpicA)
	if err != nil {
		t.Fatalf("ResumeDueEpic: %v", err)
	}
	if !resumed {
		t.Errorf("ResumeDueEpic of a limit-paused epic reported no resume, want one")
	}
	if paused, parked := epicFlags(t, s); paused != 0 || parked != "" {
		t.Errorf("paused, parked after ResumeDueEpic = %d, %q; want 0, empty", paused, parked)
	}
	if got := pauseSourceOf(t, s); got != "" {
		t.Errorf("pause source after ResumeDueEpic = %q, want empty", got)
	}
	if got := dueStateOf(t, s, dueEpicA); got != "done" {
		t.Errorf("state of the due row after ResumeDueEpic = %q, want done", got)
	}
}

func TestResumeDueEpicClosesTheEpicsOtherDueRowsWhenItResumes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	limitedWithTwoDueRowsOf(t, s)

	resumed, err := s.ResumeDueEpic(ctx, dueEpicA)
	if err != nil {
		t.Fatalf("ResumeDueEpic: %v", err)
	}
	if !resumed {
		t.Fatalf("ResumeDueEpic reported no resume, want one")
	}
	if got := dueIDs(t, s); len(got) != 0 {
		t.Errorf("due rows after ResumeDueEpic = %v, want none", got)
	}
	if got := dueStateOf(t, s, dueEpicB); got != "done" {
		t.Errorf("state of %s after ResumeDueEpic = %q, want done", dueEpicB, got)
	}
}

func TestResumeDueEpicResumesNothingForAnEpicNotPaused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	insertStaleDueRow(t, s)
	epicsBefore := rowsOf(t, s.db.DB, "epics")

	resumed, err := s.ResumeDueEpic(ctx, dueEpicA)
	if err != nil {
		t.Fatalf("ResumeDueEpic: %v", err)
	}
	if resumed {
		t.Errorf("ResumeDueEpic of an epic not paused reported a resume, want none")
	}
	if got := rowsOf(t, s.db.DB, "epics"); !reflect.DeepEqual(got, epicsBefore) {
		t.Errorf("epics after ResumeDueEpic of an epic not paused = %v, want %v", got, epicsBefore)
	}
	if got := dueStateOf(t, s, dueEpicA); got != "done" {
		t.Errorf("state of the due row after ResumeDueEpic = %q, want done", got)
	}
}

func TestResumeDueEpicResumesNothingForAnOwnerPausedEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	if err := s.PauseEpic(ctx, claimEpic); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	insertStaleDueRow(t, s)

	resumed, err := s.ResumeDueEpic(ctx, dueEpicA)
	if err != nil {
		t.Fatalf("ResumeDueEpic: %v", err)
	}
	if resumed {
		t.Errorf("ResumeDueEpic of an owner-paused epic reported a resume, want none")
	}
	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after ResumeDueEpic of an owner-paused epic = %d, want 1", paused)
	}
	if got := pauseSourceOf(t, s); got != "owner" {
		t.Errorf("pause source after ResumeDueEpic = %q, want owner", got)
	}
	if got := dueStateOf(t, s, dueEpicA); got != "done" {
		t.Errorf("state of the due row after ResumeDueEpic = %q, want done", got)
	}
}

func TestResumeDueEpicResumesNothingForARowAlreadyDone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	freeLimitOf(t, s, claimTurnOf(t, s), time.Unix(dueResetA, 0))
	if err := s.CloseDueResume(ctx, dueEpicA); err != nil {
		t.Fatalf("CloseDueResume: %v", err)
	}

	resumed, err := s.ResumeDueEpic(ctx, dueEpicA)
	if err != nil {
		t.Fatalf("ResumeDueEpic: %v", err)
	}
	if resumed {
		t.Errorf("ResumeDueEpic of a done row reported a resume, want none")
	}
	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after ResumeDueEpic of a done row = %d, want 1", paused)
	}
	if got := pauseSourceOf(t, s); got != "limit" {
		t.Errorf("pause source after ResumeDueEpic of a done row = %q, want limit", got)
	}
	if got := dueStateOf(t, s, dueEpicA); got != "done" {
		t.Errorf("state of the due row after ResumeDueEpic = %q, want done", got)
	}
}

func TestResumeDueEpicOfAnUnknownRowIsErrNoDueResume(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))

	_, err := s.ResumeDueEpic(context.Background(), dueEpicA)
	if !errors.Is(err, ErrNoDueResume) {
		t.Fatalf("ResumeDueEpic of an unknown row = %v, want ErrNoDueResume", err)
	}
}

func TestResumeDueEpicRefusesAStepRowAndWritesNothing(t *testing.T) {
	t.Parallel()
	s := openDueStore(t)
	applyDue(t, s, stepLimitAt("s1", dueResetA))

	_, err := s.ResumeDueEpic(context.Background(), dueStepS1A)
	if err == nil {
		t.Fatalf("ResumeDueEpic of a step row = nil error, want an error")
	}
	if got := dueStateOf(t, s, dueStepS1A); got != "due" {
		t.Errorf("state of the step row after refused ResumeDueEpic = %q, want due", got)
	}
}

func TestCloseDueResumeClosesOneRowAndResumesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	limitedWithTwoDueRowsOf(t, s)

	if err := s.CloseDueResume(ctx, dueEpicA); err != nil {
		t.Fatalf("CloseDueResume: %v", err)
	}
	if got := dueStateOf(t, s, dueEpicA); got != "done" {
		t.Errorf("state of %s after CloseDueResume = %q, want done", dueEpicA, got)
	}
	if got := dueIDs(t, s); !reflect.DeepEqual(got, []string{dueEpicB}) {
		t.Errorf("due rows after closing %s = %v, want [%s]", dueEpicA, got, dueEpicB)
	}
	if paused, _ := epicFlags(t, s); paused != 1 {
		t.Errorf("paused after CloseDueResume = %d, want 1", paused)
	}
	if got := pauseSourceOf(t, s); got != "limit" {
		t.Errorf("pause source after CloseDueResume = %q, want limit", got)
	}
}
