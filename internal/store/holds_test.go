package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestHoldSurvivesReopeningTheStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	row := HoldRow{Member: "m1", Since: engineNowAt()}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.AddHold(ctx, row); err != nil {
		t.Fatalf("AddHold: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open after close: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	got, err := reopened.Holds(ctx)
	if err != nil {
		t.Fatalf("Holds after reopen: %v", err)
	}
	if want := []HoldRow{row}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Holds after reopen: got %+v, want %+v", got, want)
	}
}

func TestAddHoldOfHeldMemberKeepsFirstSince(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := engineNowAt()
	if err := s.AddHold(ctx, HoldRow{Member: "m1", Since: t0}); err != nil {
		t.Fatalf("AddHold first: %v", err)
	}
	if err := s.AddHold(ctx, HoldRow{Member: "m1", Since: t0.Add(time.Minute)}); err != nil {
		t.Fatalf("AddHold of held member: got %v, want nil", err)
	}

	got, err := s.Holds(ctx)
	if err != nil {
		t.Fatalf("Holds: %v", err)
	}
	if want := []HoldRow{{Member: "m1", Since: t0}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Holds after second AddHold: got %+v, want the first since %+v", got, want)
	}
}

func TestEndHoldFreesMemberAndUnknownMemberIsNoError(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	row := HoldRow{Member: "m1", Since: engineNowAt()}
	if err := s.AddHold(ctx, row); err != nil {
		t.Fatalf("AddHold: %v", err)
	}
	if held, err := s.Holds(ctx); err != nil || !reflect.DeepEqual(held, []HoldRow{row}) {
		t.Fatalf("Holds before EndHold: got %+v, %v, want %+v", held, err, []HoldRow{row})
	}
	if err := s.EndHold(ctx, "m1"); err != nil {
		t.Fatalf("EndHold: %v", err)
	}
	got, err := s.Holds(ctx)
	if err != nil {
		t.Fatalf("Holds after EndHold: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Holds after EndHold: got %+v, want none", got)
	}
	if err := s.EndHold(ctx, "m1"); err != nil {
		t.Fatalf("EndHold of a member not held: got %v, want nil", err)
	}
	if err := s.EndHold(ctx, "missing"); err != nil {
		t.Fatalf("EndHold of an unknown member: got %v, want nil", err)
	}
	if err := s.AddHold(ctx, HoldRow{Member: "m1", Since: engineNowAt()}); err != nil {
		t.Fatalf("AddHold after EndHold: %v", err)
	}
}

func TestHoldsOrderedByMember(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := engineNowAt()
	for _, member := range []string{"m3", "m1", "m2"} {
		if err := s.AddHold(ctx, HoldRow{Member: member, Since: t0}); err != nil {
			t.Fatalf("AddHold %s: %v", member, err)
		}
	}

	got, err := s.Holds(ctx)
	if err != nil {
		t.Fatalf("Holds: %v", err)
	}
	want := []HoldRow{
		{Member: "m1", Since: t0},
		{Member: "m2", Since: t0},
		{Member: "m3", Since: t0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Holds order: got %+v, want %+v", got, want)
	}
}
