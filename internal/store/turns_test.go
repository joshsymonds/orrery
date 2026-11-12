package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// turnPane is a pane snapshot for session id in tmux pane "%" + id, carrying only the fields a test sets.
func turnPane(id string, fields Fields) Pane {
	return Pane{ID: id, Pane: "%" + id, Fields: fields}
}

func turnTime(sec int64) time.Time {
	return time.Unix(1_700_000_000+sec, 0)
}

// lastTurnEndsOf reads the turn ends of the store at path, failing the test on error.
func lastTurnEndsOf(t *testing.T, path string) map[string]int64 {
	t.Helper()
	ends, err := NewReader(path).LastTurnEnds(context.Background())
	if err != nil {
		t.Fatalf("LastTurnEnds: %v", err)
	}
	return ends
}

func TestLastTurnEndsAnswersTheNewestDoneAfterAFlip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)

	// The first done, then working, then done again. The label change last leaves state at done, so its event
	// does not set state and must not move the answer off the second done.
	steps := []struct {
		sec  int64
		pane Pane
	}{
		{0, turnPane("orrery-1", Fields{State: "done"})},
		{10, turnPane("orrery-1", Fields{State: "working"})},
		{20, turnPane("orrery-1", Fields{State: "done"})},
		{30, turnPane("orrery-1", Fields{State: "done", Label: "renamed"})},
	}
	for _, step := range steps {
		if err := s.SyncPane(ctx, "-L a", step.pane, turnTime(step.sec)); err != nil {
			t.Fatalf("SyncPane at %d: %v", step.sec, err)
		}
	}

	want := map[string]int64{"orrery-1": turnTime(20).Unix()}
	if got := lastTurnEndsOf(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("turn ends: got %v, want %v", got, want)
	}
}

func TestLastTurnEndsOmitsSessionsThatNeverWentDone(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)

	if err := s.SyncPane(ctx, "-L a", turnPane("orrery-1", Fields{State: "working"}), turnTime(0)); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	if err := s.SyncPane(
		ctx,
		"-L a",
		turnPane("orrery-1", Fields{State: "idle", Label: "x"}),
		turnTime(5),
	); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}

	if got := lastTurnEndsOf(t, path); len(got) != 0 {
		t.Fatalf("turn ends: got %v, want none", got)
	}
}

func TestLastTurnEndsAnswersEachSessionIndependently(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)

	steps := []struct {
		sec  int64
		pane Pane
	}{
		{0, turnPane("orrery-1", Fields{State: "done"})},
		{2, turnPane("orrery-2", Fields{State: "working"})},
		{5, turnPane("orrery-2", Fields{State: "done"})},
		{8, turnPane("orrery-2", Fields{State: "working"})},
	}
	for _, step := range steps {
		if err := s.SyncPane(ctx, "-L a", step.pane, turnTime(step.sec)); err != nil {
			t.Fatalf("SyncPane at %d: %v", step.sec, err)
		}
	}

	want := map[string]int64{
		"orrery-1": turnTime(0).Unix(),
		"orrery-2": turnTime(5).Unix(),
	}
	if got := lastTurnEndsOf(t, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("turn ends: got %v, want %v", got, want)
	}
}

func TestLastTurnEndsReturnsUnavailableWhenStoreIsMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	path := filepath.Join(dir, "orrery.db")

	_, err := NewReader(path).LastTurnEnds(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("LastTurnEnds of missing store: got %v, want ErrUnavailable", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store's directory was created or unreadable: %v", statErr)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store file was created or unreadable: %v", statErr)
	}
}
