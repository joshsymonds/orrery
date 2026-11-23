package store_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// baseFrom and baseTo are the commits the base move tests move an epic between, and baseOther is a commit the epic is
// not based on.
const (
	baseFrom  = "1f0e2d3c4b5a69788796a5b4c3d2e1f0a9b8c7d6"
	baseTo    = "7c1e9d4f0a2b8e6c3d5f7a9b1c2e4d6f8a0b2c4e"
	baseOther = "9a8b7c6d5e4f30211f2e3d4c5b6a79880a1b2c3d"
)

// baseMovedText is the tell moving buildEpic from baseFrom to baseTo adds.
const baseMovedText = "base moved from " + baseFrom + " to " + baseTo + ", origin's main"

// openBaseStore opens a fresh store with buildEpic accepted at base from, with no task, and returns it.
func openBaseStore(ctx context.Context, t *testing.T, from string) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	store.SeedMigrated(t, path)
	s, err := store.Open(ctx, path, store.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	row := buildEpicRow(time.Unix(1700000000, 0))
	row.Base = from
	if err := s.CreateEpic(ctx, row); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	return s
}

// planBaseTask plans task 1 of buildEpic, open with a zero build, as openBuildStore does.
func planBaseTask(ctx context.Context, t *testing.T, s *store.Store) {
	t.Helper()
	turn, err := s.ClaimTurn(ctx, buildEpic, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, buildEpic, turn.Seq, buildPlan(), writtenAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
}

// untoldTellsOfBuildEpic returns the tells of buildEpic that Attention reports as untold, nil when it reports none.
func untoldTellsOfBuildEpic(ctx context.Context, t *testing.T, s *store.Store) []store.EpicTell {
	t.Helper()
	attention, err := s.Attention(ctx)
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	for _, entry := range attention {
		if entry.Epic == buildEpic {
			return entry.Tells
		}
	}
	return nil
}

// baseState is the base, the stored base and the untold tells of buildEpic as the store reads them.
type baseState struct {
	base       string
	storedBase string
	tells      []store.EpicTell
}

// baseStateOf returns the base state of buildEpic.
func baseStateOf(ctx context.Context, t *testing.T, s *store.Store) baseState {
	t.Helper()
	row := epicOf(ctx, t, s)
	return baseState{base: row.Base, storedBase: row.StoredBase, tells: untoldTellsOfBuildEpic(ctx, t, s)}
}

func TestMoveEpicBase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	at := time.Unix(1700000900, 0)

	t.Run("moved", func(t *testing.T) {
		t.Parallel()
		s := openBaseStore(ctx, t, baseFrom)

		moved, err := s.MoveEpicBase(ctx, buildEpic, baseFrom, baseTo, at)
		if err != nil {
			t.Fatalf("MoveEpicBase: %v", err)
		}
		if !moved {
			t.Errorf("MoveEpicBase moved = false, want true")
		}
		row := epicOf(ctx, t, s)
		if row.Base != baseTo || row.StoredBase != baseTo {
			t.Errorf("base and stored base = %q and %q, want both %q", row.Base, row.StoredBase, baseTo)
		}
		want := []store.EpicTell{{Seq: 1, Text: baseMovedText, At: at}}
		if got := untoldTellsOfBuildEpic(ctx, t, s); !reflect.DeepEqual(got, want) {
			t.Errorf("tells after the move = %+v, want %+v", got, want)
		}
	})

	heldCases := []struct {
		name  string
		setup func(ctx context.Context, t *testing.T, s *store.Store)
		from  string
	}{
		{name: "open task", setup: planBaseTask, from: baseFrom},
		{name: "done task", setup: func(ctx context.Context, t *testing.T, s *store.Store) {
			t.Helper()
			planBaseTask(ctx, t, s)
			if err := s.AcceptTask(ctx, buildEpic, 1, store.TaskBuild{}, "reviewed"); err != nil {
				t.Fatalf("AcceptTask: %v", err)
			}
		}, from: baseFrom},
		{name: "branch", setup: func(ctx context.Context, t *testing.T, s *store.Store) {
			t.Helper()
			if err := s.SetEpicBranch(ctx, buildEpic, "created"); err != nil {
				t.Fatalf("SetEpicBranch: %v", err)
			}
		}, from: baseFrom},
		{name: "from not the base", setup: func(context.Context, *testing.T, *store.Store) {}, from: baseOther},
	}
	for _, heldCase := range heldCases {
		t.Run("held/"+heldCase.name, func(t *testing.T) {
			t.Parallel()
			s := openBaseStore(ctx, t, baseFrom)
			heldCase.setup(ctx, t, s)
			before := baseStateOf(ctx, t, s)

			moved, err := s.MoveEpicBase(ctx, buildEpic, heldCase.from, baseTo, at)
			if err != nil {
				t.Fatalf("MoveEpicBase: %v", err)
			}
			if moved {
				t.Errorf("MoveEpicBase moved = true, want false")
			}
			if got := baseStateOf(ctx, t, s); !reflect.DeepEqual(got, before) {
				t.Errorf("state after the refused move = %+v, want %+v", got, before)
			}
		})
	}
}

func TestTellEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openBaseStore(ctx, t, baseFrom)
	at := time.Unix(1700000900, 0)

	if err := s.TellEpic(ctx, buildEpic, "first tell", at); err != nil {
		t.Fatalf("TellEpic: %v", err)
	}
	want := []store.EpicTell{{Seq: 1, Text: "first tell", At: at}}
	if got := untoldTellsOfBuildEpic(ctx, t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("tells after TellEpic = %+v, want %+v", got, want)
	}
}
