package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

const steerMoveEpic = "orrery-1a2b3c4/epic-moved"

// steerMoveAt returns the unix second the move tests move an epic at.
func steerMoveAt() time.Time {
	return time.Unix(1700005000, 0)
}

// steerMoveStore opens a store with the sessions engine-1 and engine-2, and one epic that engine-1 steers.
func steerMoveStore(t *testing.T) *Store {
	t.Helper()
	s, _ := openPendingStore(t)
	addPendingEngines(t, s, "engine-1", "engine-2")
	row := fullEpic(steerMoveEpic)
	row.Steerer = "engine-1"
	createEpic(t, s, row)
	return s
}

// steerMoveHistory returns the history of the moved epic, and fails the test when the read errors.
func steerMoveHistory(t *testing.T, s *Store) EpicHistory {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), steerMoveEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	return history
}

// requireSteerer fails the test unless the moved epic is steered by session want.
func requireSteerer(t *testing.T, s *Store, want string) {
	t.Helper()
	steerer, ok, err := s.EpicSteerer(context.Background(), steerMoveEpic)
	if err != nil {
		t.Fatalf("EpicSteerer: %v", err)
	}
	if !ok || steerer.ID != want {
		t.Fatalf("EpicSteerer = %q (found %t), want %q", steerer.ID, ok, want)
	}
}

func TestSteerMoveByOwnerMovesSteererAndRecordsOneAction(t *testing.T) {
	t.Parallel()
	s := steerMoveStore(t)

	action, err := s.MoveEpic(context.Background(), steerMoveEpic, "engine-2", Maker{By: MakerOwner}, steerMoveAt())
	if err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}

	requireSteerer(t, s, "engine-2")
	want := []EpicAction{{
		Seq: 1, Kind: ActionMove, Maker: Maker{By: MakerOwner}, From: "engine-1", To: "engine-2", At: steerMoveAt(),
	}}
	if !reflect.DeepEqual(action, want[0]) {
		t.Fatalf("MoveEpic action = %+v, want %+v", action, want[0])
	}
	if got := steerMoveHistory(t, s).Actions; !reflect.DeepEqual(got, want) {
		t.Fatalf("EpicHistory.Actions = %+v, want %+v", got, want)
	}
}

func TestSteerMoveByConversationRecordsThatMaker(t *testing.T) {
	t.Parallel()
	s := steerMoveStore(t)
	maker := Maker{By: MakerConversation, Session: "engine-1"}

	if _, err := s.MoveEpic(context.Background(), steerMoveEpic, "engine-2", maker, steerMoveAt()); err != nil {
		t.Fatalf("MoveEpic: %v", err)
	}

	actions := steerMoveHistory(t, s).Actions
	if len(actions) != 1 || actions[0].Maker != maker || actions[0].From != "engine-1" || actions[0].To != "engine-2" {
		t.Fatalf("EpicHistory.Actions = %+v, want one move from engine-1 to engine-2 made by %+v", actions, maker)
	}
}

func TestSteerMoveToTheSteererIsRefusedAndWritesNothing(t *testing.T) {
	t.Parallel()
	s := steerMoveStore(t)

	_, err := s.MoveEpic(context.Background(), steerMoveEpic, "engine-1", Maker{By: MakerOwner}, steerMoveAt())
	var refusal RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("MoveEpic to the steerer err = %v, want a RefusalError", err)
	}
	requireSteerer(t, s, "engine-1")
	if got := steerMoveHistory(t, s).Actions; len(got) != 0 {
		t.Fatalf("EpicHistory.Actions = %+v, want none after a refused move", got)
	}
}

func TestSteerMoveOfUnknownEpicIsErrNoEpic(t *testing.T) {
	t.Parallel()
	s := steerMoveStore(t)

	_, err := s.MoveEpic(context.Background(), "orrery-1a2b3c4/epic-missing", "engine-2",
		Maker{By: MakerOwner}, steerMoveAt())
	if !errors.Is(err, ErrNoEpic) {
		t.Fatalf("MoveEpic of unknown epic err = %v, want ErrNoEpic", err)
	}
}
