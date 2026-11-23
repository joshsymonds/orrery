package store

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"
)

// heldQuestionText is the text of the held question the tests ask of claimEpic.
const heldQuestionText = "Held after a predecessor that will not ship."

// heldQuestionOpen is the open held question AskHeldEpic opens on claimEpic, as the store reads it back: start and
// close, and no recommendation.
func heldQuestionOpen() EpicQuestion {
	return EpicQuestion{
		Seq: 1, Turn: 0, State: QuestionOpen, Label: HeldLabel, Text: heldQuestionText,
		Choices: []EpicChoice{
			{ID: "start", Does: "Start the epic now from origin main as it is then"},
			{ID: "close", Does: "Close the epic abandoned"},
		},
	}
}

// createHeldEpic stores claimEpic in phase held, steered by engine-1, as a held epic that a predecessor holds.
func createHeldEpic(t *testing.T, s *Store) {
	t.Helper()
	held := fullEpic(claimEpic)
	held.Phase = PhaseHeld
	held.Steerer = "engine-1"
	createEpic(t, s, held)
	recordPendingCreator(t, s, claimEpic, "engine-1")
}

// TestDesignHeldEpicAsksOnce checks that AskHeldEpic opens one held question on a held epic, which offers start and
// close and recommends nothing, and that the steering session is owed the question's event. A second ask writes nothing
// and leaves the first question as it is.
func TestDesignHeldEpicAsksOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := openPendingStore(t)
	addPendingEngines(t, s, "engine-1")
	createHeldEpic(t, s)

	if err := s.AskHeldEpic(ctx, claimEpic, heldQuestionText, claimedAt()); err != nil {
		t.Fatalf("AskHeldEpic: %v", err)
	}
	before := storedEpicRow(t, s)
	if err := s.AskHeldEpic(ctx, claimEpic, "Asked again.", claimedAt().Add(time.Minute)); err != nil {
		t.Fatalf("second AskHeldEpic: %v", err)
	}
	if after := storedEpicRow(t, s); !reflect.DeepEqual(after, before) {
		t.Errorf("row after the second ask = %+v, want %+v", after, before)
	}
	if want := []EpicQuestion{heldQuestionOpen()}; !reflect.DeepEqual(before.Questions, want) {
		t.Errorf("questions after the ask = %+v, want %+v", before.Questions, want)
	}
	want := []PendingEvent{
		{ID: EpicQuestionEvent(claimEpic, 1), Kind: CallerKindQuestion, Epic: claimEpic, Title: "Synthetic epic"},
	}
	if got := readPending(t, path, "engine-1"); !reflect.DeepEqual(got, want) {
		t.Errorf("pending events of the steering session = %+v, want %+v", got, want)
	}
}

// TestDesignHeldEpicAnswerClose checks that answering close to the held question closes the epic abandoned in the same
// write, leaves the question answered with close, and stores the owner's decision against it.
func TestDesignHeldEpicAnswerClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	held := fullEpic(claimEpic)
	held.Phase = PhaseHeld
	createEpic(t, s, held)
	if err := s.AskHeldEpic(ctx, claimEpic, heldQuestionText, claimedAt()); err != nil {
		t.Fatalf("AskHeldEpic: %v", err)
	}

	if _, err := s.AnswerQuestion(
		ctx, claimEpic, "owner-close", "", 1, "close", Maker{By: MakerOwner}, claimedAt().Add(time.Minute),
	); err != nil {
		t.Fatalf("AnswerQuestion close: %v", err)
	}
	row := storedEpicRow(t, s)
	if row.Phase != PhaseClosedAbandoned {
		t.Errorf("phase after close = %q, want %q", row.Phase, PhaseClosedAbandoned)
	}
	answered := heldQuestionOpen()
	answered.State, answered.Choice = QuestionAnswered, "close"
	if want := []EpicQuestion{answered}; !reflect.DeepEqual(row.Questions, want) {
		t.Errorf("questions after close = %+v, want %+v", row.Questions, want)
	}
	if !slices.ContainsFunc(row.Decisions, func(d EpicDecision) bool {
		return d.ID == "owner-close" && d.Question == 1
	}) {
		t.Errorf("decisions after close = %+v, want owner-close answering question 1", row.Decisions)
	}
}
