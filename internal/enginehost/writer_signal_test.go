package enginehost

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// writerSignalRetry is the Retry the refusing signal test configures, so its fake timer's duration is a known value.
const writerSignalRetry = time.Minute

// writerSignalCall is one ApplyEngine call of a writerSignalApplier: its number, counted from 1, and its ops.
type writerSignalCall struct {
	n   int
	ops []store.EngineOp
}

// writerSignalApplier is the Applier of the signal tests. It stores nothing. It counts its ApplyEngine calls and sends
// each call on started as the call begins, before it can hold or refuse. refuses and holds decide, by call number,
// which calls refuse with a store error and which wait until open. Run is its only caller.
type writerSignalApplier struct {
	refuses func(call int) bool
	holds   func(call int) bool
	started chan writerSignalCall
	gate    chan struct{}
	once    sync.Once

	mu    sync.Mutex
	calls [][]store.EngineOp
}

// newWriterSignalApplier returns a writerSignalApplier that refuses the calls refuses picks and holds the calls holds
// picks. A nil function picks no call.
func newWriterSignalApplier(refuses, holds func(call int) bool) *writerSignalApplier {
	return &writerSignalApplier{
		refuses: refuses, holds: holds, started: make(chan writerSignalCall, 16), gate: make(chan struct{}),
	}
}

func (a *writerSignalApplier) ApplyEngine(_ context.Context, ops []store.EngineOp, _ time.Time) error {
	a.mu.Lock()
	a.calls = append(a.calls, ops)
	n := len(a.calls)
	a.mu.Unlock()
	a.started <- writerSignalCall{n: n, ops: ops}
	if a.holds != nil && a.holds(n) {
		<-a.gate
	}
	if a.refuses != nil && a.refuses(n) {
		// Not a row-gone error, so the writer keeps the call's ops for a retry.
		return errors.New("writer signal test: store refused the batch")
	}
	return nil
}

// open releases every held call, and holds no call that begins after it.
func (a *writerSignalApplier) open() {
	a.once.Do(func() { close(a.gate) })
}

// recorded returns the ops of every call so far, in call order.
func (a *writerSignalApplier) recorded() [][]store.EngineOp {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([][]store.EngineOp(nil), a.calls...)
}

// writerSignalTimer is a timer a writerSignalTimers armed: its duration, and the channel the writer waits on for it to
// fire.
type writerSignalTimer struct {
	d time.Duration
	c chan time.Time
}

// writerSignalTimers is the NewTimer of the signal tests. It arms no real timer: each timer it arms is sent on armed,
// and the timer fires only when a test sends on its channel.
type writerSignalTimers struct {
	armed chan writerSignalTimer
}

func newWriterSignalTimers() *writerSignalTimers {
	return &writerSignalTimers{armed: make(chan writerSignalTimer, 8)}
}

func (ts *writerSignalTimers) newTimer(d time.Duration) (<-chan time.Time, func() bool) {
	c := make(chan time.Time)
	ts.armed <- writerSignalTimer{d: d, c: c}
	return c, func() bool { return true }
}

// TestWriterSignalCommitStoresInOneApply checks that a Commit wakes the writer at once: with no timer fired, the Commit
// returns after exactly one ApplyEngine call.
func TestWriterSignalCommitStoresInOneApply(t *testing.T) {
	t.Parallel()
	applier := newWriterSignalApplier(nil, nil)
	timers := newWriterSignalTimers()
	w := NewWriter(WriterConfig{Applier: applier, NewTimer: timers.newTimer})
	startRun(t, w)

	if err := w.Commit(t.Context(), "a", prompt("a", "committed")); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := len(applier.recorded()); got != 1 {
		t.Fatalf("ApplyEngine calls when Commit returned: got %d, want 1", got)
	}
}

// TestWriterSignalPutsDuringFlushLandTogether checks that ops queued while a flush is in flight go in the next flush
// together: the Puts made while the first call is held land in one further ApplyEngine call once it is released.
func TestWriterSignalPutsDuringFlushLandTogether(t *testing.T) {
	t.Parallel()
	applier := newWriterSignalApplier(nil, func(call int) bool { return call == 1 })
	timers := newWriterSignalTimers()
	w := NewWriter(WriterConfig{Applier: applier, NewTimer: timers.newTimer})
	startRun(t, w)
	ctx := t.Context()

	if err := w.Put(ctx, "a", prompt("a", "first")); err != nil {
		t.Fatalf("Put first: %v", err)
	}
	// The Put woke a flush, and that flush is now held in its first call.
	receive(t, applier.started)

	want := make([]store.EngineOp, 0, 10)
	for i := range 10 {
		op := prompt("a", fmt.Sprintf("burst-%d", i))
		want = append(want, op)
		if err := w.Put(ctx, "a", op); err != nil {
			t.Fatalf("Put burst-%d while the flush is held: %v", i, err)
		}
	}
	applier.open()

	second := receive(t, applier.started)
	if second.n != 2 || !reflect.DeepEqual(second.ops, want) {
		t.Fatalf("second call: got call %d with %+v, want call 2 with the ten Puts %+v", second.n, second.ops, want)
	}
	if got := len(applier.recorded()); got != 2 {
		t.Fatalf("ApplyEngine calls: got %d, want 2 (the held flush, then the ten Puts)", got)
	}
}

// TestWriterSignalRefusedOpWaitsForRetry checks that an op the store refuses is not sent again while the retry timer is
// armed, even when a later Put wakes the writer, and that the timer's firing sends it exactly once.
func TestWriterSignalRefusedOpWaitsForRetry(t *testing.T) {
	t.Parallel()
	applier := newWriterSignalApplier(
		func(call int) bool { return call == 1 },
		func(call int) bool { return call >= 2 },
	)
	timers := newWriterSignalTimers()
	w := NewWriter(WriterConfig{Applier: applier, Retry: writerSignalRetry, NewTimer: timers.newTimer})
	startRun(t, w)
	// Registered after startRun, so it runs before the writer stops: a call still held then is released first.
	t.Cleanup(applier.open)
	ctx := t.Context()

	if err := w.Put(ctx, "a", prompt("a", "refused")); err != nil {
		t.Fatalf("Put refused: %v", err)
	}
	receive(t, applier.started)
	timer := receive(t, timers.armed)
	if timer.d != writerSignalRetry {
		t.Fatalf("retry armed for %v, want %v", timer.d, writerSignalRetry)
	}

	// A later Put wakes the writer while the retry is armed, and the writer must not send the refused op then. If it did,
	// its call would begin before the timer fires, and the select would take that call. The send on the timer completes
	// only once the writer is waiting on the timer again, which it is only after it has handled the wake.
	if err := w.Put(ctx, "a", prompt("a", "later")); err != nil {
		t.Fatalf("Put later: %v", err)
	}
	select {
	case timer.c <- time.Time{}:
	case early := <-applier.started:
		t.Fatalf("ApplyEngine call %d began before the retry timer fired, with %+v", early.n, early.ops)
	}

	fired := receive(t, applier.started)
	want := []store.EngineOp{prompt("a", "refused"), prompt("a", "later")}
	if fired.n != 2 || !reflect.DeepEqual(fired.ops, want) {
		t.Fatalf("call after the timer: got call %d with %+v, want call 2 with %+v", fired.n, fired.ops, want)
	}
	applier.open()

	// Commit returns once its op is stored, so every call before it has been made. A call that sent the refused op again
	// would show here.
	last := prompt("a", "last")
	if err := w.Commit(ctx, "a", last); err != nil {
		t.Fatalf("Commit last: %v", err)
	}
	calls := applier.recorded()
	if len(calls) != 3 {
		t.Fatalf("ApplyEngine calls: got %d, want 3 (refused, the timer's batch, then last)", len(calls))
	}
	if refused := []store.EngineOp{prompt("a", "refused")}; !reflect.DeepEqual(calls[0], refused) {
		t.Fatalf("first call: got %+v, want %+v", calls[0], refused)
	}
	if !reflect.DeepEqual(calls[1], want) {
		t.Fatalf("second call: got %+v, want %+v", calls[1], want)
	}
	if lastCall := []store.EngineOp{last}; !reflect.DeepEqual(calls[2], lastCall) {
		t.Fatalf("third call: got %+v, want %+v", calls[2], lastCall)
	}
}
