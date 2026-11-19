package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// kickRows reads every row a kick may change: the epic, its turns, its triggers and its decisions.
func kickRows(t *testing.T, s *Store) [][][]any {
	t.Helper()
	return append(controlRows(t, s), rowsOf(t, s.db.DB, "epic_decisions"))
}

// requireNoKickWrite fails unless the rows a kick may change equal before.
func requireNoKickWrite(t *testing.T, s *Store, before [][][]any) {
	t.Helper()
	if got := kickRows(t, s); !reflect.DeepEqual(got, before) {
		t.Errorf("rows after refused KickEpic = %v, want %v", got, before)
	}
}

func TestEpicKickStoresTheDecisionAndOnePendingTriggerCarryingTheText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))

	if err := s.KickEpic(ctx, claimEpic, "Try the sqlite plan", Maker{}, claimedAt()); err != nil {
		t.Fatalf("KickEpic: %v", err)
	}
	epic, err := s.Epic(ctx, claimEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	last := epic.Decisions[len(epic.Decisions)-1]
	if last.Seq != 3 || last.ID != "owner-kick:3" || last.Text != "Try the sqlite plan" || last.Question != 0 {
		t.Errorf("kick decision = %+v, want seq 3, id owner-kick:3, the text, and no question", last)
	}
	if got := epic.Changed.Unix(); got != claimedAt().Unix() {
		t.Errorf("changed after KickEpic = %d, want %d", got, claimedAt().Unix())
	}
	wantTriggers := [][]any{
		{claimEpic, createdTriggerID, createdTriggerCause, "pending", int64(0), int64(1700000000)},
		{claimEpic, "owner-kick:3", "owner kick: Try the sqlite plan", "pending", int64(0), claimedAt().Unix()},
	}
	if got := rowsOf(t, s.db.DB, "epic_triggers"); !reflect.DeepEqual(got, wantTriggers) {
		t.Errorf("epic_triggers after KickEpic = %v, want %v", got, wantTriggers)
	}
}

func TestEpicKickOfUnknownEpicIsErrNoEpicAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	before := kickRows(t, s)

	const missing = "orrery-1a2b3c4/no-such-epic"
	if err := s.KickEpic(ctx, missing, "Try again", Maker{}, claimedAt()); !errors.Is(err, ErrNoEpic) {
		t.Fatalf("KickEpic of an unknown epic err = %v, want ErrNoEpic", err)
	}
	requireNoKickWrite(t, s, before)
}

func TestEpicKickRefusesEveryEndedPhaseAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	endedPhases := []string{"met_at_base", "clean", "ended_with_gaps", "released", "done", "stopped"}
	for _, phase := range endedPhases {
		if _, err := s.fixtureExec(ctx, `UPDATE epics SET phase = ? WHERE id = ?`, phase, claimEpic); err != nil {
			t.Fatalf("set phase %s: %v", phase, err)
		}
		before := kickRows(t, s)
		requireRefusal(t, s.KickEpic(ctx, claimEpic, "Try again", Maker{}, claimedAt()),
			"epic "+claimEpic+" is "+phase+", an ended phase, and takes no kick")
		requireNoKickWrite(t, s, before)
	}
}

func TestEpicKickRefusesPausedEpicAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	if err := s.PauseEpic(ctx, claimEpic); err != nil {
		t.Fatalf("PauseEpic: %v", err)
	}
	before := kickRows(t, s)

	requireRefusal(t, s.KickEpic(ctx, claimEpic, "Try again", Maker{}, claimedAt()),
		"epic "+claimEpic+" is paused; resume it before kicking it")
	requireNoKickWrite(t, s, before)
}

func TestEpicKickRefusesParkedEpicNamingRetryAndWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	parkEpic(t, s, claimEpic, "credit ran out")
	before := kickRows(t, s)

	requireRefusal(t, s.KickEpic(ctx, claimEpic, "Try again", Maker{}, claimedAt()),
		"epic "+claimEpic+" is parked; orrery epic retry "+claimEpic+" ends the park before a kick")
	requireNoKickWrite(t, s, before)
}
