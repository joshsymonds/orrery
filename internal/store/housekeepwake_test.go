package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// wakes records the ids a housekeeping hook is called with, in call order.
type wakes struct {
	ids []string
}

func (w *wakes) record(id string) {
	w.ids = append(w.ids, id)
}

// wantWakes fails t unless the calls recorded since the last check are want, in order.
func wantWakes(t *testing.T, seen *wakes, want ...string) {
	t.Helper()
	got := seen.ids
	seen.ids = nil
	if !slices.Equal(got, want) {
		t.Errorf("hook calls = %v, want %v", got, want)
	}
}

// TestHousekeepWakeEpicStored creates an epic, which calls the hook once with its id. A create that fails because the
// id is stored calls nothing, and a nil hook is cleared.
func TestHousekeepWakeEpicStored(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)
	seen := &wakes{}
	s.OnEpicStored(seen.record)

	createEpic(t, s, fullEpic("repo/a"))
	wantWakes(t, seen, "repo/a")

	if err := s.CreateEpic(context.Background(), fullEpic("repo/a")); !errors.Is(err, ErrEpicExists) {
		t.Fatalf("CreateEpic of a stored id = %v, want ErrEpicExists", err)
	}
	wantWakes(t, seen)

	s.OnEpicStored(nil)
	createEpic(t, s, fullEpic("repo/b"))
	wantWakes(t, seen)
}

// TestHousekeepWakeEpicEnded sets an epic to each phase ListUnreportedEpics reports, which calls the hook with its id,
// and to reviewing, which calls nothing.
func TestHousekeepWakeEpicEnded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	createEpic(t, s, fullEpic("repo/a"))
	seen := &wakes{}
	s.OnEpicEnded(seen.record)
	at := time.Unix(1_700_000_100, 0)
	setPhase := func(phase string) {
		t.Helper()
		if err := s.write(ctx, func(queries *sqlcgen.Queries) error {
			return setEpicPhase(ctx, queries, "repo/a", phase, at)
		}); err != nil {
			t.Fatalf("set phase %s: %v", phase, err)
		}
	}

	setPhase("reviewing")
	wantWakes(t, seen)
	for _, phase := range []string{
		"released", "done", "ended_with_gaps", "closed_superseded", "closed_shipped", "closed_abandoned",
	} {
		setPhase(phase)
		wantWakes(t, seen, "repo/a")
	}
}

// TestHousekeepWakeFirstPrompt sends an engine session two prompts and a step session one. The engine session's first
// prompt calls the hook once with its id, and its second prompt and the step session's prompt call nothing.
func TestHousekeepWakeFirstPrompt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineAdd{ID: "s1", Dir: "/b", State: "idle", Kind: KindStep},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add sessions: %v", err)
	}
	seen := &wakes{}
	s.OnFirstPrompt(seen.record)
	prompt := func(id, writeID string) {
		t.Helper()
		if err := s.ApplyEngine(ctx, []EngineOp{
			EnginePrompt{ID: id, Text: "prompt " + writeID, WriteID: writeID},
		}, engineNowAt()); err != nil {
			t.Fatalf("ApplyEngine prompt of %s: %v", id, err)
		}
	}

	prompt("e1", "w1")
	wantWakes(t, seen, "e1")
	prompt("e1", "w2")
	wantWakes(t, seen)
	prompt("s1", "w3")
	wantWakes(t, seen)
}

// TestHousekeepWakeTurnEnded ends turns of an engine session and of a step session. Each turn end of the engine session
// calls the hook with its id, a batch that ends two engine sessions' turns calls it once per session, and the step
// session's turn end calls nothing.
func TestHousekeepWakeTurnEnded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EngineAdd{ID: "s1", Dir: "/c", State: "idle", Kind: KindStep},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add sessions: %v", err)
	}
	seen := &wakes{}
	s.OnTurnEnded(seen.record)
	turnEnd := func(id string) EngineTurnEnded {
		return EngineTurnEnded{ID: id, Subtype: "success", TerminalReason: "done", Result: "ok", Origin: "user"}
	}
	apply := func(ops ...EngineOp) {
		t.Helper()
		if err := s.ApplyEngine(ctx, ops, engineNowAt()); err != nil {
			t.Fatalf("ApplyEngine: %v", err)
		}
	}

	apply(turnEnd("e1"))
	wantWakes(t, seen, "e1")
	apply(turnEnd("e1"))
	wantWakes(t, seen, "e1")
	apply(turnEnd("e1"), turnEnd("e2"), turnEnd("e1"))
	wantWakes(t, seen, "e1", "e2")
	apply(turnEnd("s1"))
	wantWakes(t, seen)
}

// TestHousekeepWakeRolledBackWrite runs marked statements in writes that then fail, and no hook is called: a store
// write whose epic phase statement ran and then was refused, and an engine batch whose prompt and turn end ran before
// an op that failed.
func TestHousekeepWakeRolledBackWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	createEpic(t, s, fullEpic("repo/a"))
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "idle"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add session: %v", err)
	}
	seen := &wakes{}
	s.OnEpicEnded(seen.record)
	s.OnFirstPrompt(seen.record)
	s.OnTurnEnded(seen.record)

	refused := errors.New("synthetic refusal")
	at := time.Unix(1_700_000_100, 0)
	if err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := setEpicPhase(ctx, queries, "repo/a", "released", at); err != nil {
			return err
		}
		return refused
	}); !errors.Is(err, refused) {
		t.Fatalf("refused write = %v, want the refusal", err)
	}
	wantWakes(t, seen)

	err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "e1", Text: "first", WriteID: "w1"},
		EngineTurnEnded{ID: "e1", Subtype: "success", TerminalReason: "done", Result: "ok", Origin: "user"},
		EngineSet{ID: "missing"},
	}, engineNowAt())
	if !errors.Is(err, ErrNoEngineSession) {
		t.Fatalf("ApplyEngine with a failing op = %v, want ErrNoEngineSession", err)
	}
	wantWakes(t, seen)
}
