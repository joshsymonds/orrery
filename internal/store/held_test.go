package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// applyHeld applies ops to s at at, and fails the test when the write fails.
func applyHeld(t *testing.T, s *Store, at time.Time, ops ...EngineOp) {
	t.Helper()
	if err := s.ApplyEngine(context.Background(), ops, at); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

func TestHeldHoldKeepsMessagesInOrderAndKeepsTheFirstTextOfAPromptID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)
	applyHeld(t, s, t0, EngineAdd{ID: "eng-a", Dir: "/a", State: "working"})
	applyHeld(t, s, t0,
		EngineHold{ID: "eng-a", PromptID: "p1", Text: "first"},
		EngineHold{ID: "eng-a", PromptID: "p2", Text: "second"},
		EngineHold{ID: "eng-a", PromptID: "p1", Text: "repeat"},
	)

	got, err := s.HeldPrompts(ctx, "eng-a")
	if err != nil {
		t.Fatalf("HeldPrompts: %v", err)
	}
	want := []HeldPrompt{{PromptID: "p1", Text: "first"}, {PromptID: "p2", Text: "second"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("held messages = %+v, want %+v", got, want)
	}
}

func TestHeldReleaseRemovesOnePromptAndIgnoresAnAbsentOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)
	applyHeld(t, s, t0,
		EngineAdd{ID: "eng-a", Dir: "/a", State: "working"},
		EngineHold{ID: "eng-a", PromptID: "p1", Text: "first"},
		EngineHold{ID: "eng-a", PromptID: "p2", Text: "second"},
	)
	applyHeld(t, s, t0,
		EngineHeldRelease{ID: "eng-a", PromptID: "p1"},
		EngineHeldRelease{ID: "eng-a", PromptID: "absent"},
	)

	got, err := s.HeldPrompts(ctx, "eng-a")
	if err != nil {
		t.Fatalf("HeldPrompts: %v", err)
	}
	if want := []HeldPrompt{{PromptID: "p2", Text: "second"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("held messages after the release = %+v, want %+v", got, want)
	}
}

func TestHeldHoldOfMissingSessionIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	err := s.ApplyEngine(
		ctx,
		[]EngineOp{EngineHold{ID: "missing", PromptID: "p1", Text: "x"}},
		time.Unix(1_700_000_000, 0),
	)
	if !errors.Is(err, ErrNoEngineSession) {
		t.Fatalf("hold of a missing session: err = %v, want ErrNoEngineSession", err)
	}
	got, err := s.HeldPrompts(ctx, "missing")
	if err != nil {
		t.Fatalf("HeldPrompts of a missing session: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("held messages of a missing session = %+v, want none", got)
	}
}

func TestHeldReaderReadsTheSessionsMessagesInOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	t0 := time.Unix(1_700_000_000, 0)
	applyHeld(t, s, t0,
		EngineAdd{ID: "eng-a", Dir: "/a", State: "working"},
		EngineAdd{ID: "eng-b", Dir: "/b", State: "working"},
		EngineHold{ID: "eng-a", PromptID: "p1", Text: "first"},
		EngineHold{ID: "eng-b", PromptID: "other", Text: "elsewhere"},
		EngineHold{ID: "eng-a", PromptID: "p2", Text: "second"},
	)

	got, err := NewReader(path).HeldPrompts(ctx, "eng-a")
	if err != nil {
		t.Fatalf("Reader.HeldPrompts: %v", err)
	}
	want := []HeldPrompt{{PromptID: "p1", Text: "first"}, {PromptID: "p2", Text: "second"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reader.HeldPrompts = %+v, want %+v", got, want)
	}
	none, err := NewReader(path).HeldPrompts(ctx, "missing")
	if err != nil {
		t.Fatalf("Reader.HeldPrompts of a missing session: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("Reader.HeldPrompts of a missing session = %+v, want none", none)
	}
}

// TestHeldSessionKeepsItsRowInRetentionWhileItHoldsMessages keeps a session with no event in retention, since it holds a
// message. Once the message is released, the next retention pass removes the session.
func TestHeldSessionKeepsItsRowInRetentionWhileItHoldsMessages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)
	applyHeld(t, s, t0,
		EngineAdd{ID: "eng-a", Dir: "/a", State: "working"},
		EngineHold{ID: "eng-a", PromptID: "p1", Text: "kept"},
	)

	pastRetention := t0.Add(EventRetention + time.Hour)
	if err := s.SyncServer(ctx, "-L a", nil, pastRetention); err != nil {
		t.Fatalf("SyncServer past retention: %v", err)
	}
	if got, want := engineIDsOf(t, s), []string{"eng-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("engine sessions holding a message: got %v, want %v", got, want)
	}
	if got, err := s.HeldPrompts(ctx, "eng-a"); err != nil || len(got) != 1 {
		t.Fatalf("held messages past retention = %+v, %v; want the one message", got, err)
	}

	applyHeld(t, s, pastRetention, EngineHeldRelease{ID: "eng-a", PromptID: "p1"})
	if err := s.SyncServer(ctx, "-L a", nil, pastRetention.Add(time.Hour)); err != nil {
		t.Fatalf("SyncServer after the release: %v", err)
	}
	if got := engineIDsOf(t, s); len(got) != 0 {
		t.Fatalf("engine sessions after the release = %v, want none", got)
	}
}
