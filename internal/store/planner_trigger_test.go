package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// createdTriggerRow is the one epic_triggers row a newly created epic from fullEpic must hold: id `created`, cause
// `epic created`, pending, turn 0, created at the epic's creation second.
func createdTriggerRow(id string) []any {
	return []any{id, "created", "epic created", "pending", int64(0), int64(1700000000)}
}

func TestCreateEpicAddsItsCreatedTrigger(t *testing.T) {
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	want := [][]any{createdTriggerRow(id)}
	if got := rowsOf(t, s.db, "epic_triggers"); !reflect.DeepEqual(got, want) {
		t.Errorf("epic_triggers after CreateEpic = %v, want %v", got, want)
	}
}

func TestSecondCreateEpicOfStoredIDLeavesItsTriggerUnchanged(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"
	createEpic(t, s, fullEpic(id))

	again := fullEpic(id)
	again.Created = epicCreated().Add(time.Hour)
	if err := s.CreateEpic(ctx, again); !errors.Is(err, ErrEpicExists) {
		t.Fatalf("second CreateEpic err = %v, want ErrEpicExists", err)
	}

	want := [][]any{createdTriggerRow(id)}
	if got := rowsOf(t, s.db, "epic_triggers"); !reflect.DeepEqual(got, want) {
		t.Errorf("epic_triggers after refused CreateEpic = %v, want %v", got, want)
	}
}

func TestFailedCreateEpicStoresNoTrigger(t *testing.T) {
	ctx := context.Background()
	s := openEpicStore(t)
	const id = "orrery-1a2b3c4/epic-store"

	broken := fullEpic(id)
	broken.Decisions[1].ID = broken.Decisions[0].ID
	if err := s.CreateEpic(ctx, broken); err == nil {
		t.Fatalf("CreateEpic with two seed decisions of one id: err = nil, want a write failure")
	}

	if _, err := s.Epic(ctx, id); !errors.Is(err, ErrNoEpic) {
		t.Errorf("Epic after failed CreateEpic err = %v, want ErrNoEpic", err)
	}
	if got := rowsOf(t, s.db, "epic_triggers"); len(got) != 0 {
		t.Errorf("epic_triggers after failed CreateEpic = %v, want none", got)
	}
}
