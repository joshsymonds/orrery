package enginehost

import (
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// consumedAt returns an EngineConsumed of session id that sets only the consumed offset.
func consumedAt(id string, offset int64) store.EngineConsumed {
	return store.EngineConsumed{ID: id, Consumed: offset}
}

// waitQueuedOps blocks until the writer's queue holds exactly ops, failing the test after waitTimeout.
func waitQueuedOps(t *testing.T, w *Writer, ops ...store.EngineOp) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for {
		w.mu.Lock()
		got := opsOf(w.queue)
		w.mu.Unlock()
		if reflect.DeepEqual(got, ops) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited for queued ops %+v, got %+v", ops, got)
		}
		time.Sleep(time.Millisecond)
	}
}

// A Put of only an EngineConsumed is outside the op limit: it returns while its session sits at the limit.
func TestConsumedOffsetPutNeverWaitsAtTheLimit(t *testing.T) {
	s, _ := openStore(t)
	addSessions(t, s, "a")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	// A one-hour tick never fires, so session a stays at its limit of one unstored op.
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour, Limit: 1})
	ctx := t.Context()
	if err := w.Put(ctx, "a", prompt("a", "a1")); err != nil {
		t.Fatalf("Put a1 under the limit: %v", err)
	}

	put := make(chan error, 1)
	go func() { put <- w.Put(ctx, "a", consumedAt("a", 5)) }()
	if err := receive(t, put); err != nil {
		t.Fatalf("Put of only an EngineConsumed at the limit: %v", err)
	}
}

// An EngineConsumed does not count against the limit, so it leaves room for the session's next non-text op.
func TestConsumedOffsetDoesNotCountAgainstTheLimit(t *testing.T) {
	s, _ := openStore(t)
	addSessions(t, s, "a")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour, Limit: 1})
	ctx := t.Context()
	if err := w.Put(ctx, "a", consumedAt("a", 5)); err != nil {
		t.Fatalf("Put consumed: %v", err)
	}

	put := make(chan error, 1)
	go func() { put <- w.Put(ctx, "a", prompt("a", "a1")) }()
	if err := receive(t, put); err != nil {
		t.Fatalf("Put a1 after a queued consumed offset: %v", err)
	}
}

// A queued EngineConsumed absorbs the session's next one: the stored op takes the later offset, and each optional
// field takes the later op's value when set and keeps the queued one's otherwise.
func TestQueuedConsumedOffsetsStoreAsOneOpWithLaterOffsetAndMergedFields(t *testing.T) {
	s, _ := openStore(t)
	addSessions(t, s, "a")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour})
	ctx := t.Context()

	first := store.EngineConsumed{
		ID: "a", Consumed: 5, ReplayFrom: new(int64(3)), Output: new("first"), Background: new(true),
	}
	later := store.EngineConsumed{ID: "a", Consumed: 10, Output: new("second")}
	for _, op := range []store.EngineConsumed{first, later} {
		if err := w.Put(ctx, "a", op); err != nil {
			t.Fatalf("Put %+v: %v", op, err)
		}
	}
	w.flush(ctx)

	calls := applier.callsOf()
	if len(calls) != 1 {
		t.Fatalf("ApplyEngine calls: got %d, want 1", len(calls))
	}
	want := store.EngineConsumed{
		ID: "a", Consumed: 10, ReplayFrom: new(int64(3)), Output: new("second"), Background: new(true),
	}
	if got := []store.EngineOp{want}; !reflect.DeepEqual(calls[0], got) {
		t.Fatalf("ops in the call: got %+v, want %+v", calls[0], got)
	}
	row, err := s.EngineSession(ctx, "a")
	if err != nil {
		t.Fatalf("EngineSession a: %v", err)
	}
	if row.Consumed != 10 || row.ReplayFrom != 3 || row.TurnOutput != "second" || !row.Background {
		t.Fatalf("stored state of a: consumed %d, replay from %d, output %q, background %t; want 10, 3, second, true",
			row.Consumed, row.ReplayFrom, row.TurnOutput, row.Background)
	}
}

// An EngineConsumed of one session never merges into a queued one of another session.
func TestConsumedOffsetOfAnotherSessionIsNotMerged(t *testing.T) {
	s, _ := openStore(t)
	addSessions(t, s, "a", "b")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour})
	ctx := t.Context()

	for _, op := range []store.EngineConsumed{consumedAt("a", 5), consumedAt("b", 7), consumedAt("a", 10)} {
		if err := w.Put(ctx, op.ID, op); err != nil {
			t.Fatalf("Put %+v: %v", op, err)
		}
	}
	w.flush(ctx)

	calls := applier.callsOf()
	if len(calls) != 1 {
		t.Fatalf("ApplyEngine calls: got %d, want 1", len(calls))
	}
	if want := []store.EngineOp{consumedAt("a", 10), consumedAt("b", 7)}; !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("ops in the call: got %+v, want %+v", calls[0], want)
	}
}

// An EngineLaunch resets the consumed offset to zero, so a consumed offset queued after it must not move back
// before it: the store would keep zero and the session would replay or lose its conversation.
func TestConsumedOffsetAfterQueuedLaunchIsNotMerged(t *testing.T) {
	s, _ := openStore(t)
	addSessions(t, s, "a")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour})
	ctx := t.Context()

	launch := store.EngineLaunch{ID: "a"}
	for _, op := range []store.EngineOp{consumedAt("a", 5), launch, consumedAt("a", 10)} {
		if err := w.Put(ctx, "a", op); err != nil {
			t.Fatalf("Put %+v: %v", op, err)
		}
	}
	w.flush(ctx)

	calls := applier.callsOf()
	if len(calls) != 1 {
		t.Fatalf("ApplyEngine calls: got %d, want 1", len(calls))
	}
	if want := []store.EngineOp{consumedAt("a", 5), launch, consumedAt("a", 10)}; !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("ops in the call: got %+v, want %+v", calls[0], want)
	}
	row, err := s.EngineSession(ctx, "a")
	if err != nil {
		t.Fatalf("EngineSession a: %v", err)
	}
	if row.Consumed != 10 {
		t.Fatalf("stored consumed offset of a: got %d, want 10", row.Consumed)
	}
}

// An EngineConsumed queued before the session's row is added must not move past the EngineAdd that creates the row.
func TestConsumedOffsetAfterQueuedAddIsNotMerged(t *testing.T) {
	s, _ := openStore(t)
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour})
	ctx := t.Context()

	add := store.EngineAdd{ID: "a", Dir: "/work", State: "working"}
	for _, op := range []store.EngineOp{consumedAt("a", 5), add, consumedAt("a", 10)} {
		if err := w.Put(ctx, "a", op); err != nil {
			t.Fatalf("Put %+v: %v", op, err)
		}
	}
	// The first attempt drops the consumed op that precedes the row, and the retry stores the rest.
	w.flush(ctx)

	calls := applier.callsOf()
	if len(calls) < 1 {
		t.Fatal("ApplyEngine was never called")
	}
	if want := []store.EngineOp{consumedAt("a", 5), add, consumedAt("a", 10)}; !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("ops in the first call: got %+v, want %+v", calls[0], want)
	}
	row, err := s.EngineSession(ctx, "a")
	if err != nil {
		t.Fatalf("EngineSession a: %v", err)
	}
	if row.Consumed != 10 {
		t.Fatalf("stored consumed offset of a: got %d, want 10", row.Consumed)
	}
}

// A Commit of an EngineConsumed that was absorbed into a queued one returns only once the merged op is stored.
func TestCommitOfAbsorbedConsumedOffsetReturnsOnceMergedOpIsStored(t *testing.T) {
	s, _ := openStore(t)
	addSessions(t, s, "a")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour})
	ctx := t.Context()

	if err := w.Put(ctx, "a", consumedAt("a", 5)); err != nil {
		t.Fatalf("Put consumed 5: %v", err)
	}
	commit := make(chan error, 1)
	go func() { commit <- w.Commit(ctx, "a", consumedAt("a", 10)) }()
	waitQueuedOps(t, w, consumedAt("a", 10))

	select {
	case err := <-commit:
		t.Fatalf("Commit returned before the merged op was stored: %v", err)
	default:
	}
	w.flush(ctx)
	if err := receive(t, commit); err != nil {
		t.Fatalf("Commit of the absorbed consumed offset: %v", err)
	}
	row, err := s.EngineSession(ctx, "a")
	if err != nil {
		t.Fatalf("EngineSession a: %v", err)
	}
	if row.Consumed != 10 {
		t.Fatalf("stored consumed offset of a after Commit returned: got %d, want 10", row.Consumed)
	}
}
