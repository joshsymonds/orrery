package store

import (
	"context"
	"testing"
	"time"
)

// listingFields are the fields EpicListings and Epic and EpicFlow read for an epic's steering and inbox: its steerer,
// whether it is paused and by what source, its park reason and how many of its questions are open.
type listingFields struct {
	Steerer       string
	Paused        bool
	PauseSource   string
	Parked        string
	OpenQuestions int64
}

// listingFieldsOf returns the listing fields of row.
func listingFieldsOf(row EpicRow) listingFields {
	return listingFields{
		Steerer: row.Steerer, Paused: row.Paused, PauseSource: row.PauseSource, Parked: row.Parked,
		OpenQuestions: row.OpenQuestions,
	}
}

// checkListingFields checks that EpicListings, Epic and EpicFlow each read the listing fields of claimEpic as want.
func checkListingFields(t *testing.T, s *Store, want listingFields) {
	t.Helper()
	ctx := context.Background()
	listed, err := s.EpicListings(ctx)
	if err != nil {
		t.Fatalf("EpicListings: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("EpicListings lists %d epics, want 1", len(listed))
	}
	if got := listingFieldsOf(listed[0]); got != want {
		t.Errorf("EpicListings fields = %+v, want %+v", got, want)
	}
	epic, err := s.Epic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if got := listingFieldsOf(epic); got != want {
		t.Errorf("Epic fields = %+v, want %+v", got, want)
	}
	flow, err := s.EpicFlow(ctx, claimEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if got := listingFieldsOf(flow.Epic); got != want {
		t.Errorf("EpicFlow fields = %+v, want %+v", got, want)
	}
}

func TestEpicListingFieldsSteeredEpicPausedByItsOwnerReadsOwnerSource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	row := fullEpic(claimEpic)
	row.Steerer = "steer-session-1"
	createEpic(t, s, row)
	if err := s.PauseEpic(ctx, claimEpic); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	checkListingFields(t, s, listingFields{Steerer: "steer-session-1", Paused: true, PauseSource: pauseOwner})
}

func TestEpicListingFieldsEpicPausedByAUsageLimitReadsLimitSource(t *testing.T) {
	t.Parallel()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	freeLimitOf(t, s, claimTurnOf(t, s), time.Unix(dueResetA, 0))
	checkListingFields(t, s, listingFields{Paused: true, PauseSource: pauseLimit})
}

func TestEpicListingFieldsUnpausedEpicReadsNoPauseAndTheParkReason(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	checkListingFields(t, s, listingFields{})

	if err := s.ParkEpic(ctx, claimEpic, ParkClassBranch, "credit ran out", completedAt()); err != nil {
		t.Fatalf("ParkEpic: %v", err)
	}
	// An owner-held park opens its park question, which is the one open question the park leaves.
	checkListingFields(t, s, listingFields{Parked: "credit ran out", OpenQuestions: 1})
}

func TestEpicListingFieldsOpenQuestionsCountsOnlyOpenQuestions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	questionedEpic(t, s,
		shapedQuestion("first", "First?"), shapedQuestion("second", "Second?"), shapedQuestion("third", "Third?"))
	checkListingFields(t, s, listingFields{OpenQuestions: 3})

	if _, err := s.AnswerQuestion(
		ctx, claimEpic, "D3", "Answer two", 2, ChoiceOther, Maker{}, claimedAt()); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	checkListingFields(t, s, listingFields{OpenQuestions: 2})
}
