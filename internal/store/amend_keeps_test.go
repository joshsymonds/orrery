package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// requireNoAmendmentKeepsR1 fails unless the amended epic stores no amendment and R1 still holds the item it was created
// with.
func requireNoAmendmentKeepsR1(t *testing.T, s *Store) {
	t.Helper()
	if amendments := closeRow(t, s, amendEpic).Amendments; len(amendments) != 0 {
		t.Errorf("amendments after the refusal = %+v, want none stored", amendments)
	}
	if item := r1Item(t, s); !reflect.DeepEqual(item, oldAmendItem()) {
		t.Errorf("R1 item after the refusal = %+v, want the item it was created with", item)
	}
}

// TestAmendPlannerKeepsConclusionAndKind checks that the planner and orreryd still may not change the conclusion or the
// kind of R1: an amend_evidence action the planner carries out that changes either is refused at once, and so is an
// amendment orreryd asks for that changes either, which is a RefusalError. Each stores no amendment and leaves R1 its item.
func TestAmendPlannerKeepsConclusionAndKind(t *testing.T) {
	t.Parallel()
	conclusion := newAmendItem()
	kind := captureAmendItem()
	kind.Kind, kind.Tests = evidence.KindCapture, nil

	t.Run("planner new conclusion is refused", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		createAmendEpic(t, s)
		action := storeActionsOf(t, s, amendAction(conclusion))[0]
		if _, err := s.CarryOutAction(context.Background(), action, amendAt()); err != nil {
			t.Fatalf("CarryOutAction: %v", err)
		}
		requireRefusedAmendAction(t, s, action,
			"the planner may not change the conclusion of R1; only the owner amends a conclusion")
		requireNoAmendmentKeepsR1(t, s)
	})

	t.Run("planner new kind is refused", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		createAmendEpic(t, s)
		action := storeActionsOf(t, s, amendAction(kind))[0]
		if _, err := s.CarryOutAction(context.Background(), action, amendAt()); err != nil {
			t.Fatalf("CarryOutAction: %v", err)
		}
		requireRefusedAmendAction(t, s, action,
			"the planner may not change the kind of R1; only the owner amends a kind")
		requireNoAmendmentKeepsR1(t, s)
	})

	t.Run("orreryd new conclusion is refused", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		createAmendEpic(t, s)
		requireRefusal(t, amendRefused(t, s, Maker{By: MakerOrreryd}, conclusion),
			"the orreryd may not change the conclusion of R1; only the owner amends a conclusion")
		requireNoAmendmentKeepsR1(t, s)
	})

	t.Run("orreryd new kind is refused", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		createAmendEpic(t, s)
		requireRefusal(t, amendRefused(t, s, Maker{By: MakerOrreryd}, kind),
			"the orreryd may not change the kind of R1; only the owner amends a kind")
		requireNoAmendmentKeepsR1(t, s)
	})
}
