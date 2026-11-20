package store_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// outsideHoldDeadline is the instant the outside holds in these tests are freed: 30 minutes after releaseRunAt.
func outsideHoldDeadline() time.Time {
	return releaseRunAt().Add(30 * time.Minute)
}

// holdOutsideSlot holds the release slot of releaseRepo for gambit:e1 until outsideHoldDeadline, and fails unless
// gambit:e1 holds it.
func holdOutsideSlot(t *testing.T, s *store.Store) {
	t.Helper()
	holder, held, err := s.HoldReleaseSlot(context.Background(), releaseRepo, "gambit:e1", outsideHoldDeadline())
	if err != nil || !held {
		t.Fatalf("HoldReleaseSlot of gambit:e1 = %+v, held %v, %v; want held", holder, held, err)
	}
}

// TestSlotOutsideHoldBlocksCleanEpic checks that a clean epic of a repository whose slot an outside label holds gets
// that holder back from TakeReleaseSlot, stays clean, and sees the holder as its slot.
func TestSlotOutsideHoldBlocksCleanEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t, releaseEpicIn(releaseEpic, releaseRepo, store.PhaseClean))
	addReleaseSteps(t, s, releaseEpic)
	holdOutsideSlot(t, s)

	holder, err := s.TakeReleaseSlot(ctx, releaseEpic, releaseRunAt())
	if err != nil {
		t.Fatalf("TakeReleaseSlot of %s: %v", releaseEpic, err)
	}
	if holder != "outside:gambit:e1" {
		t.Errorf("TakeReleaseSlot of %s = %q, want %q", releaseEpic, holder, "outside:gambit:e1")
	}
	requirePhase(t, s, releaseEpic, store.PhaseClean)
	requireSlot(t, s, releaseEpic, "outside:gambit:e1")
}

// TestSlotOutsideHoldGiveFreesForEpic checks that only the label holding an outside hold can give it back, that the
// give frees the slot for a clean epic, and that the epic takes it and moves to releasing.
func TestSlotOutsideHoldGiveFreesForEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t, releaseEpicIn(releaseEpic, releaseRepo, store.PhaseClean))
	addReleaseSteps(t, s, releaseEpic)
	holdOutsideSlot(t, s)

	given, err := s.GiveReleaseSlot(ctx, releaseRepo, "gambit:e2")
	if err != nil {
		t.Fatalf("GiveReleaseSlot by gambit:e2: %v", err)
	}
	if given {
		t.Errorf("GiveReleaseSlot by gambit:e2 = true, want false: another label holds the slot")
	}
	requireSlot(t, s, releaseEpic, "outside:gambit:e1")

	given, err = s.GiveReleaseSlot(ctx, releaseRepo, "gambit:e1")
	if err != nil {
		t.Fatalf("GiveReleaseSlot by gambit:e1: %v", err)
	}
	if !given {
		t.Fatalf("GiveReleaseSlot by gambit:e1 = false, want true")
	}

	holder, err := s.TakeReleaseSlot(ctx, releaseEpic, releaseRunAt())
	if err != nil {
		t.Fatalf("TakeReleaseSlot of %s: %v", releaseEpic, err)
	}
	if holder != releaseEpic {
		t.Errorf("TakeReleaseSlot of %s = %q, want %q", releaseEpic, holder, releaseEpic)
	}
	requirePhase(t, s, releaseEpic, store.PhaseReleasing)
}

// TestSlotOutsideHoldRefusedWhileEpicHolds checks that an outside hold is refused on a slot an epic holds, and that the
// refusal names the epic.
func TestSlotOutsideHoldRefusedWhileEpicHolds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t, releaseEpicIn(releaseEpic, releaseRepo, store.PhaseClean))
	if _, err := s.TakeReleaseSlot(ctx, releaseEpic, releaseRunAt()); err != nil {
		t.Fatalf("TakeReleaseSlot of %s: %v", releaseEpic, err)
	}

	holder, held, err := s.HoldReleaseSlot(ctx, releaseRepo, "gambit:e1", outsideHoldDeadline())
	if err != nil {
		t.Fatalf("HoldReleaseSlot by gambit:e1: %v", err)
	}
	if held {
		t.Errorf("HoldReleaseSlot by gambit:e1 held = true, want false: %s holds the slot", releaseEpic)
	}
	if holder.Kind != store.ReleaseSlotEpic || holder.Epic != releaseEpic {
		t.Errorf("HoldReleaseSlot by gambit:e1 holder = %+v, want epic %s", holder, releaseEpic)
	}
}

// TestSlotOutsideHoldRenewedBySameLabel checks that the label holding an outside hold renews it with its deadline
// replaced, that another label is refused the renewed slot, and that an empty repository or label is an error.
func TestSlotOutsideHoldRenewedBySameLabel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t)
	holdOutsideSlot(t, s)

	renewed := outsideHoldDeadline().Add(time.Hour)
	holder, held, err := s.HoldReleaseSlot(ctx, releaseRepo, "gambit:e1", renewed)
	if err != nil || !held || !holder.Deadline.Equal(renewed) {
		t.Fatalf("HoldReleaseSlot renewed by gambit:e1 = %+v, held %v, %v; want held until %v",
			holder, held, err, renewed)
	}
	next, ok, err := s.NextReleaseSlotDeadline(ctx)
	if err != nil || !ok || !next.Equal(renewed) {
		t.Errorf("NextReleaseSlotDeadline after the renewal = %v, %v, %v; want %v", next, ok, err, renewed)
	}

	holder, held, err = s.HoldReleaseSlot(ctx, releaseRepo, "gambit:e2", renewed.Add(time.Hour))
	if err != nil || held || holder.Label != "gambit:e1" {
		t.Errorf("HoldReleaseSlot by gambit:e2 = %+v, held %v, %v; want gambit:e1 to keep the slot", holder, held, err)
	}

	if _, _, err = s.HoldReleaseSlot(ctx, "", "gambit:e1", renewed); err == nil {
		t.Errorf("HoldReleaseSlot with an empty repository: err = nil, want an error")
	}
	if _, _, err = s.HoldReleaseSlot(ctx, releaseRepo, "", renewed); err == nil {
		t.Errorf("HoldReleaseSlot with an empty label: err = nil, want an error")
	}
}

// TestSlotOutsideHoldFreedAtDeadline checks that an outside hold is freed by FreeExpiredReleaseSlots once its deadline
// is reached and not a second before, that NextReleaseSlotDeadline reports the deadline until then and none after, and
// that a clean epic then takes the freed slot.
func TestSlotOutsideHoldFreedAtDeadline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openReleaseStore(t, releaseEpicIn(releaseEpic, releaseRepo, store.PhaseClean))
	addReleaseSteps(t, s, releaseEpic)
	holdOutsideSlot(t, s)

	next, ok, err := s.NextReleaseSlotDeadline(ctx)
	if err != nil {
		t.Fatalf("NextReleaseSlotDeadline: %v", err)
	}
	if !ok || !next.Equal(outsideHoldDeadline()) {
		t.Fatalf("NextReleaseSlotDeadline = %v, %v; want %v, true", next, ok, outsideHoldDeadline())
	}

	freed, err := s.FreeExpiredReleaseSlots(ctx, outsideHoldDeadline().Add(-time.Second))
	if err != nil {
		t.Fatalf("FreeExpiredReleaseSlots one second before the deadline: %v", err)
	}
	if len(freed) != 0 {
		t.Errorf("FreeExpiredReleaseSlots one second before the deadline = %v, want none", freed)
	}
	requireSlot(t, s, releaseEpic, "outside:gambit:e1")

	freed, err = s.FreeExpiredReleaseSlots(ctx, outsideHoldDeadline())
	if err != nil {
		t.Fatalf("FreeExpiredReleaseSlots at the deadline: %v", err)
	}
	if !reflect.DeepEqual(freed, []string{releaseRepo}) {
		t.Errorf("FreeExpiredReleaseSlots at the deadline = %v, want [%s]", freed, releaseRepo)
	}
	if _, ok, err = s.NextReleaseSlotDeadline(ctx); err != nil || ok {
		t.Errorf("NextReleaseSlotDeadline after the free = %v, %v; want none", ok, err)
	}

	holder, err := s.TakeReleaseSlot(ctx, releaseEpic, releaseRunAt())
	if err != nil {
		t.Fatalf("TakeReleaseSlot of %s: %v", releaseEpic, err)
	}
	if holder != releaseEpic {
		t.Errorf("TakeReleaseSlot of %s after the free = %q, want %q", releaseEpic, holder, releaseEpic)
	}
	requirePhase(t, s, releaseEpic, store.PhaseReleasing)
}
