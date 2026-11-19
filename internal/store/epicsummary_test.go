package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// epicSummaryID is the synthetic epic the summary tests store.
const epicSummaryID = "orrery-1a2b3c4/epic-summary"

func TestCreatedEpicReadsAnEmptySummaryAndIsListedUnsummarized(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(epicSummaryID))

	got, err := s.Epic(ctx, epicSummaryID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if got.Summary != "" {
		t.Errorf("Summary of a created epic = %q, want empty", got.Summary)
	}
	unsummarized, err := s.UnsummarizedEpics(ctx)
	if err != nil {
		t.Fatalf("UnsummarizedEpics: %v", err)
	}
	if len(unsummarized) != 1 || !reflect.DeepEqual(unsummarized[0], got) {
		t.Errorf("UnsummarizedEpics = %+v, want only %+v", unsummarized, got)
	}
}

func TestSetEpicSummaryStoresOnceAndLeavesTheFirstAndTheChangedTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(epicSummaryID))

	if err := s.SetEpicSummary(ctx, epicSummaryID, "First summary"); err != nil {
		t.Fatalf("SetEpicSummary first: %v", err)
	}
	if err := s.SetEpicSummary(ctx, epicSummaryID, "Second summary"); err != nil {
		t.Fatalf("SetEpicSummary second: %v", err)
	}
	got, err := s.Epic(ctx, epicSummaryID)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if got.Summary != "First summary" {
		t.Errorf("Summary after two SetEpicSummary calls = %q, want the first", got.Summary)
	}
	if want := fullEpic(epicSummaryID).Changed; !got.Changed.Equal(want) {
		t.Errorf("Changed after SetEpicSummary = %v, want %v, unchanged", got.Changed, want)
	}
	unsummarized, err := s.UnsummarizedEpics(ctx)
	if err != nil {
		t.Fatalf("UnsummarizedEpics: %v", err)
	}
	if len(unsummarized) != 0 {
		t.Errorf("UnsummarizedEpics after SetEpicSummary = %+v, want none", unsummarized)
	}
}

func TestUnsummarizedEpicsListsOnlyEpicsWithoutASummaryInIDOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const later, earlier = "orrery-1a2b3c4/epic-b", "orrery-1a2b3c4/epic-a"
	createEpic(t, s, fullEpic(later))
	createEpic(t, s, fullEpic(earlier))
	createEpic(t, s, fullEpic("orrery-1a2b3c4/epic-c"))
	if err := s.SetEpicSummary(ctx, "orrery-1a2b3c4/epic-c", "Summary of c"); err != nil {
		t.Fatalf("SetEpicSummary: %v", err)
	}

	unsummarized, err := s.UnsummarizedEpics(ctx)
	if err != nil {
		t.Fatalf("UnsummarizedEpics: %v", err)
	}
	ids := make([]string, 0, len(unsummarized))
	for _, row := range unsummarized {
		ids = append(ids, row.ID)
	}
	if want := []string{earlier, later}; !reflect.DeepEqual(ids, want) {
		t.Errorf("UnsummarizedEpics ids = %v, want %v", ids, want)
	}
}

func TestEpicSummaryAndHistoryOfAnUnknownEpicAreErrNoEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)

	if err := s.SetEpicSummary(ctx, "orrery-1a2b3c4/missing", "Summary"); !errors.Is(err, ErrNoEpic) {
		t.Errorf("SetEpicSummary of an unknown epic err = %v, want ErrNoEpic", err)
	}
	if _, err := s.EpicHistory(ctx, "orrery-1a2b3c4/missing"); !errors.Is(err, ErrNoEpic) {
		t.Errorf("EpicHistory of an unknown epic err = %v, want ErrNoEpic", err)
	}
}

func TestEpicHistoryReadsTriggersByCreatedThenIDAndTellsBySeq(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(epicSummaryID))
	later := time.Unix(1700000200, 0)
	parkedAt := time.Unix(1700000300, 0)
	if err := s.fixtureQueries().InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: epicSummaryID, ID: "b-trigger", Cause: "second cause", State: "pending", Turn: 0, Created: later.Unix(),
	}); err != nil {
		t.Fatalf("InsertEpicTrigger b-trigger: %v", err)
	}
	if err := s.fixtureQueries().InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: epicSummaryID, ID: "a-trigger", Cause: "first cause", State: "pending", Turn: 0, Created: later.Unix(),
	}); err != nil {
		t.Fatalf("InsertEpicTrigger a-trigger: %v", err)
	}
	if err := s.ParkEpic(ctx, epicSummaryID, ParkClassBranch, "Parked for the history test", parkedAt); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}

	got, err := s.EpicHistory(ctx, epicSummaryID)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	want := EpicHistory{
		Triggers: []EpicTrigger{
			{ID: createdTriggerID, Cause: createdTriggerCause, State: "pending", Created: epicCreated()},
			{ID: "a-trigger", Cause: "first cause", State: "pending", Created: later},
			{ID: "b-trigger", Cause: "second cause", State: "pending", Created: later},
		},
		Tells: []EpicTell{{Seq: 1, Text: "Parked for the history test", At: parkedAt}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EpicHistory = %+v, want %+v", got, want)
	}
}
