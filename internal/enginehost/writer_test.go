package enginehost

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	// The blank import registers the pure-Go "sqlite" driver the lock test opens a second connection with.
	_ "modernc.org/sqlite"
)

// waitTimeout bounds every wait on a channel in these tests. It is a hang guard, never a timing assumption.
const waitTimeout = 10 * time.Second

// recordingApplier forwards to a real store and records every ApplyEngine call, failed ones included.
type recordingApplier struct {
	store  *store.Store
	mu     sync.Mutex
	calls  [][]store.EngineOp
	notify chan struct{}
}

func (a *recordingApplier) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	a.mu.Lock()
	a.calls = append(a.calls, ops)
	a.mu.Unlock()
	select {
	case a.notify <- struct{}{}:
	default:
	}
	return a.store.ApplyEngine(ctx, ops, now)
}

// callsOf returns a copy of the recorded calls.
func (a *recordingApplier) callsOf() [][]store.EngineOp {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([][]store.EngineOp(nil), a.calls...)
}

// waitCalls blocks until at least n ApplyEngine calls have been made.
func (a *recordingApplier) waitCalls(t *testing.T, n int) {
	t.Helper()
	timeout := time.After(waitTimeout)
	for {
		a.mu.Lock()
		got := len(a.calls)
		a.mu.Unlock()
		if got >= n {
			return
		}
		select {
		case <-a.notify:
		case <-timeout:
			t.Fatalf("waited for %d ApplyEngine calls, got %d", n, got)
		}
	}
}

// openStore opens a store in a temp dir and closes it when the test ends.
func openStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store Close: %v", err)
		}
	})
	return s, path
}

// addSessions inserts engine session rows so ops can name them.
func addSessions(t *testing.T, s *store.Store, ids ...string) {
	t.Helper()
	ops := make([]store.EngineOp, 0, len(ids))
	for _, id := range ids {
		ops = append(ops, store.EngineAdd{ID: id, Dir: "/work", State: "working"})
	}
	if err := s.ApplyEngine(t.Context(), ops, time.Unix(1, 0)); err != nil {
		t.Fatalf("add sessions: %v", err)
	}
}

// startRun runs w until the returned stop is called, and stops it at the end of the test if still running.
func startRun(t *testing.T, w *Writer) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(stop)
	return stop
}

// promptTexts returns the text of every engine_prompt event of a session, in seq order.
func promptTexts(t *testing.T, path, id string) []string {
	t.Helper()
	history, err := store.NewReader(path).EngineHistory(t.Context(), id, 0, 10_000)
	if err != nil {
		t.Fatalf("EngineHistory %s: %v", id, err)
	}
	var texts []string
	for _, event := range history.Events {
		if event.Kind != store.EventEnginePrompt {
			continue
		}
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode prompt payload %q: %v", event.Payload, err)
		}
		texts = append(texts, payload.Text)
	}
	return texts
}

// lockWrites holds the store's write lock from a second connection until the returned release is called.
func lockWrites(t *testing.T, path string) (release func()) {
	t.Helper()
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open second connection: %v", err)
	}
	conn, err := other.Conn(t.Context())
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	if _, err := conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("take write lock: %v", err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			// The cleanup runs after t.Context is canceled, so the rollback takes a background context.
			if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
				t.Errorf("release write lock: %v", err)
			}
			if err := conn.Close(); err != nil {
				t.Errorf("close second connection: %v", err)
			}
			if err := other.Close(); err != nil {
				t.Errorf("close second database: %v", err)
			}
		})
	}
	t.Cleanup(release)
	return release
}

// receive returns the value sent on ch, failing the test if none arrives within waitTimeout.
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitTimeout):
		t.Fatal("timed out waiting for a result")
		var zero T
		return zero
	}
}

func prompt(id, text string) store.EngineOp {
	return store.EnginePrompt{ID: id, Text: text}
}

func TestFlushStoresEverySessionInOneApplyCallInPutOrder(t *testing.T) {
	s, path := openStore(t)
	addSessions(t, s, "a", "b", "c")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	// A one-hour tick never fires during the test, so every flush is one the test makes.
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour})
	ctx := t.Context()

	for _, put := range []struct {
		id   string
		text string
	}{{"a", "a1"}, {"b", "b1"}, {"a", "a2"}, {"c", "c1"}, {"b", "b2"}} {
		if err := w.Put(ctx, put.id, prompt(put.id, put.text)); err != nil {
			t.Fatalf("Put %s %s: %v", put.id, put.text, err)
		}
	}
	w.flush(ctx)

	calls := applier.callsOf()
	if len(calls) != 1 {
		t.Fatalf("ApplyEngine calls: got %d, want 1", len(calls))
	}
	want := []store.EngineOp{
		prompt("a", "a1"), prompt("b", "b1"), prompt("a", "a2"), prompt("c", "c1"), prompt("b", "b2"),
	}
	if !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("ops in the call: got %+v, want %+v", calls[0], want)
	}
	if got := promptTexts(t, path, "a"); !reflect.DeepEqual(got, []string{"a1", "a2"}) {
		t.Fatalf("session a stored texts: got %v", got)
	}
	if got := promptTexts(t, path, "b"); !reflect.DeepEqual(got, []string{"b1", "b2"}) {
		t.Fatalf("session b stored texts: got %v", got)
	}
}

func TestRepeatedEngineTextWhileQueuedStoresLastTextInOneCall(t *testing.T) {
	s, _ := openStore(t)
	addSessions(t, s, "a")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour})
	ctx := t.Context()

	block := func(text string) store.EngineOp {
		return store.EngineText{ID: "a", MessageID: "m1", Index: 0, Kind: "text", Text: text}
	}
	for _, text := range []string{"he", "hel", "hello"} {
		if err := w.Put(ctx, "a", block(text)); err != nil {
			t.Fatalf("Put text %q: %v", text, err)
		}
	}
	w.flush(ctx)

	calls := applier.callsOf()
	if len(calls) != 1 {
		t.Fatalf("ApplyEngine calls: got %d, want 1", len(calls))
	}
	if want := []store.EngineOp{block("hello")}; !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("ops in the call: got %+v, want %+v", calls[0], want)
	}
}

func TestEngineTextAfterItsDoneIsNotCoalescedIntoIt(t *testing.T) {
	s, _ := openStore(t)
	addSessions(t, s, "a")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour})
	ctx := t.Context()

	text := store.EngineText{ID: "a", MessageID: "m1", Index: 0, Kind: "text", Text: "first"}
	done := store.EngineTextDone{ID: "a", MessageID: "m1", Index: 0}
	next := store.EngineText{ID: "a", MessageID: "m1", Index: 0, Kind: "text", Text: "second"}
	for _, op := range []store.EngineOp{text, done, next} {
		if err := w.Put(ctx, "a", op); err != nil {
			t.Fatalf("Put %+v: %v", op, err)
		}
	}
	w.flush(ctx)

	calls := applier.callsOf()
	if len(calls) != 1 {
		t.Fatalf("ApplyEngine calls: got %d, want 1", len(calls))
	}
	if want := []store.EngineOp{text, done, next}; !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("ops in the call: got %+v, want %+v", calls[0], want)
	}
}

func TestPutBlocksAtLimitWhileStoreIsLockedAndStoresEveryOpOnce(t *testing.T) {
	s, path := openStore(t)
	addSessions(t, s, "a", "b")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: 5 * time.Millisecond, Limit: 3})
	stop := startRun(t, w)
	ctx := t.Context()
	release := lockWrites(t, path)

	for _, text := range []string{"a1", "a2", "a3"} {
		if err := w.Put(ctx, "a", prompt("a", text)); err != nil {
			t.Fatalf("Put a %s under the limit: %v", text, err)
		}
	}
	blocked := make(chan error, 1)
	go func() { blocked <- w.Put(ctx, "a", prompt("a", "a4")) }()

	// Two failed flushes prove the writer ticked while the store was locked, so a1 to a3 were retried, not lost.
	applier.waitCalls(t, 2)
	select {
	case err := <-blocked:
		t.Fatalf("Put past the limit returned while the store was locked: %v", err)
	default:
	}

	// Another session is not held back by session a's limit.
	if err := w.Put(ctx, "b", prompt("b", "b1")); err != nil {
		t.Fatalf("Put b while a is at its limit: %v", err)
	}

	release()
	if err := receive(t, blocked); err != nil {
		t.Fatalf("blocked Put after release: %v", err)
	}
	stop()

	if got, want := promptTexts(t, path, "a"), []string{"a1", "a2", "a3", "a4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session a stored texts: got %v, want %v", got, want)
	}
	if got, want := promptTexts(t, path, "b"), []string{"b1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session b stored texts: got %v, want %v", got, want)
	}
}

func TestCommitReturnsOnlyAfterItsOpsAreStored(t *testing.T) {
	s, path := openStore(t)
	addSessions(t, s, "a")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: 5 * time.Millisecond})
	startRun(t, w)

	if err := w.Commit(t.Context(), "a", prompt("a", "committed")); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got, want := promptTexts(t, path, "a"), []string{"committed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stored texts after Commit returned: got %v, want %v", got, want)
	}
}

func TestCancelingRunFlushesQueuedOps(t *testing.T) {
	s, path := openStore(t)
	addSessions(t, s, "a", "b")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour})
	stop := startRun(t, w)

	if err := w.Put(t.Context(), "a", prompt("a", "queued-a")); err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if err := w.Put(t.Context(), "b", prompt("b", "queued-b")); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	stop()

	if got, want := promptTexts(t, path, "a"), []string{"queued-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session a stored texts: got %v, want %v", got, want)
	}
	if got, want := promptTexts(t, path, "b"), []string{"queued-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session b stored texts: got %v, want %v", got, want)
	}
}

func TestPutAndCommitAfterRunStopsReturnErrWriterStopped(t *testing.T) {
	s, _ := openStore(t)
	addSessions(t, s, "a")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour, Limit: 1})
	stop := startRun(t, w)
	stop()

	if err := w.Put(t.Context(), "a", prompt("a", "late")); !errors.Is(err, ErrWriterStopped) {
		t.Fatalf("Put after Run stopped: got %v, want ErrWriterStopped", err)
	}
	if err := w.Commit(t.Context(), "a", prompt("a", "late")); !errors.Is(err, ErrWriterStopped) {
		t.Fatalf("Commit after Run stopped: got %v, want ErrWriterStopped", err)
	}
}

func TestPutBlockedAtLimitReturnsErrWriterStoppedWhenRunStops(t *testing.T) {
	s, path := openStore(t)
	addSessions(t, s, "a")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: 5 * time.Millisecond, Limit: 1})
	stop := startRun(t, w)
	release := lockWrites(t, path)
	ctx := t.Context()

	if err := w.Put(ctx, "a", prompt("a", "a1")); err != nil {
		t.Fatalf("Put a1 under the limit: %v", err)
	}
	blocked := make(chan error, 1)
	go func() { blocked <- w.Put(ctx, "a", prompt("a", "a2")) }()
	applier.waitCalls(t, 1)

	// Stopping Run with the store still locked leaves a1 unstored, and the blocked Put reports that rather than hang.
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	if err := receive(t, blocked); !errors.Is(err, ErrWriterStopped) {
		t.Fatalf("blocked Put after Run stopped: got %v, want ErrWriterStopped", err)
	}
	release()
	receive(t, stopped)
}

// gatedApplier forwards to a real store, but its first call signals entered and waits for release, so a test can
// act while that ApplyEngine call is in flight.
type gatedApplier struct {
	store   *store.Store
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (a *gatedApplier) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	a.once.Do(func() {
		close(a.entered)
		<-a.release
	})
	return a.store.ApplyEngine(ctx, ops, now)
}

func TestPutDuringTheLastFlushReturnsErrWriterStopped(t *testing.T) {
	s, path := openStore(t)
	addSessions(t, s, "a")
	applier := &gatedApplier{store: s, entered: make(chan struct{}), release: make(chan struct{})}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour})
	stop := startRun(t, w)

	if err := w.Put(t.Context(), "a", prompt("a", "queued")); err != nil {
		t.Fatalf("Put before stop: %v", err)
	}
	stopped := make(chan struct{})
	go func() {
		stop()
		close(stopped)
	}()
	receive(t, applier.entered)

	// The last flush has taken the queue, so nothing would store an op queued now: Put must refuse it.
	lateErr := w.Put(t.Context(), "a", prompt("a", "late"))
	close(applier.release)
	receive(t, stopped)

	if !errors.Is(lateErr, ErrWriterStopped) {
		t.Fatalf("Put during the last flush: got %v, want ErrWriterStopped", lateErr)
	}
	if got, want := promptTexts(t, path, "a"), []string{"queued"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stored texts: got %v, want %v", got, want)
	}
}

// ctxApplier records whether the context of its first ApplyEngine call was canceled while that call was in flight.
// The first call signals entered and waits for release, so a test can cancel Run during a tick flush.
type ctxApplier struct {
	store    *store.Store
	once     sync.Once
	entered  chan struct{}
	release  chan struct{}
	canceled chan bool
}

func (a *ctxApplier) ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error {
	a.once.Do(func() {
		close(a.entered)
		<-a.release
		a.canceled <- ctx.Err() != nil
	})
	return a.store.ApplyEngine(ctx, ops, now)
}

// A store write that is canceled mid-commit can report an error although SQLite committed it, and the writer would
// queue and store it again, so no flush may run on a context that Run's cancellation reaches.
func TestRunCancelDoesNotCancelATickFlushInFlight(t *testing.T) {
	s, path := openStore(t)
	addSessions(t, s, "a")
	applier := &ctxApplier{
		store: s, entered: make(chan struct{}), release: make(chan struct{}), canceled: make(chan bool, 1),
	}
	w := NewWriter(WriterConfig{Applier: applier, Tick: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		w.Run(ctx)
	}()

	if err := w.Put(t.Context(), "a", prompt("a", "once")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	receive(t, applier.entered)
	cancel()
	close(applier.release)
	canceled := receive(t, applier.canceled)
	receive(t, stopped)

	if canceled {
		t.Fatal("the tick flush in flight saw Run's cancellation")
	}
	if got, want := promptTexts(t, path, "a"), []string{"once"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stored texts: got %v, want %v", got, want)
	}
}

// waitQueued blocks until the writer's queue holds at least n ops, failing the test after waitTimeout.
func waitQueued(t *testing.T, w *Writer, n int) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for {
		w.mu.Lock()
		got := len(w.queue)
		w.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d queued ops, got %d", n, got)
		}
		time.Sleep(time.Millisecond)
	}
}

// A session whose row the retention sync removed must not stall the writer: its op is dropped with a warning, its
// Commit fails wrapping ErrNoEngineSession, and the other session's op in the same batch is stored that tick.
func TestMissingEngineSessionDropsItsOpAndStoresTheRest(t *testing.T) {
	ctx := t.Context()
	s, path := openStore(t)
	// Session a's only event is at start. A sync one retention period and a second later removes its row, while session
	// b's event at that later time keeps b.
	start := time.Unix(1_700_000_000, 0)
	addA := store.EngineAdd{ID: "a", Dir: "/work", State: "working"}
	if err := s.ApplyEngine(ctx, []store.EngineOp{addA}, start); err != nil {
		t.Fatalf("add session a: %v", err)
	}
	now := start.Add(store.EventRetention + time.Second)
	addB := store.EngineAdd{ID: "b", Dir: "/work", State: "working"}
	if err := s.ApplyEngine(ctx, []store.EngineOp{addB}, now); err != nil {
		t.Fatalf("add session b: %v", err)
	}
	if err := s.SyncServer(ctx, "orrery-test", nil, now); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if _, err := store.NewReader(path).EngineHistory(ctx, "a", 0, 1); !errors.Is(err, store.ErrNoEngineSession) {
		t.Fatalf("session a after retention sync: got %v, want ErrNoEngineSession", err)
	}

	var logs bytes.Buffer
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{
		Applier: applier, Tick: time.Hour, Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	stop := startRun(t, w)

	// Session b's op is queued first, so the op for the missing row is at index 1 of the batch.
	if err := w.Put(ctx, "b", prompt("b", "b1")); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	idle := "idle"
	commitA := make(chan error, 1)
	go func() {
		commitA <- w.Commit(ctx, "a", store.EngineSet{ID: "a", Changes: store.EngineChanges{State: &idle}})
	}()
	waitQueued(t, w, 2)
	// The tick is one flush the test makes, so both ops share one batch.
	w.flush(ctx)

	if err := receive(t, commitA); !errors.Is(err, store.ErrNoEngineSession) {
		t.Fatalf("Commit for session a: got %v, want an error wrapping ErrNoEngineSession", err)
	}
	if got, want := promptTexts(t, path, "b"), []string{"b1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session b stored texts: got %v, want %v", got, want)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "session=a") {
		t.Fatalf("warning does not name session a: %q", logs.String())
	}

	calls := applier.callsOf()
	if len(calls) != 2 {
		t.Fatalf("ApplyEngine calls: got %d, want 2 (the batch, then the batch without a's op)", len(calls))
	}
	if want := []store.EngineOp{prompt("b", "b1")}; !reflect.DeepEqual(calls[1], want) {
		t.Fatalf("ops in the second call: got %+v, want %+v", calls[1], want)
	}

	// The dropped op is never sent again, and canceling Run leaves b stored.
	w.flush(ctx)
	stop()
	if got := len(applier.callsOf()); got != 2 {
		t.Fatalf("ApplyEngine calls after another flush and stop: got %d, want 2", got)
	}
	if got, want := promptTexts(t, path, "b"), []string{"b1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session b stored texts after stop: got %v, want %v", got, want)
	}
}

// A Commit whose step op names a step with no row must not stall the writer: its op is dropped with a warning naming
// its session, its Commit fails wrapping ErrNoStep, and the other session's op in the same batch is stored that tick.
func TestMissingStepDropsItsOpAndStoresTheRest(t *testing.T) {
	ctx := t.Context()
	s, path := openStore(t)
	addSessions(t, s, "a", "b")

	var logs bytes.Buffer
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{
		Applier: applier, Tick: time.Hour, Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	stop := startRun(t, w)

	// Session a has an engine row but no step row. Session b's op is queued first, so the op for the missing step is at
	// index 1 of the batch.
	if err := w.Put(ctx, "b", prompt("b", "b1")); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	commitA := make(chan error, 1)
	go func() {
		commitA <- w.Commit(ctx, "a", store.StepEnd{ID: "a", End: "done", Status: "done"})
	}()
	waitQueued(t, w, 2)
	// The tick is one flush the test makes, so both ops share one batch.
	w.flush(ctx)

	if err := receive(t, commitA); !errors.Is(err, store.ErrNoStep) {
		t.Fatalf("Commit for session a: got %v, want an error wrapping ErrNoStep", err)
	}
	if got, want := promptTexts(t, path, "b"), []string{"b1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session b stored texts: got %v, want %v", got, want)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "session=a") {
		t.Fatalf("warning does not name session a: %q", logs.String())
	}
	if strings.Contains(logs.String(), "ops kept for retry") {
		t.Fatalf("the batch was requeued for retry: %q", logs.String())
	}

	calls := applier.callsOf()
	if len(calls) != 2 {
		t.Fatalf("ApplyEngine calls: got %d, want 2 (the batch, then the batch without a's op)", len(calls))
	}
	if want := []store.EngineOp{prompt("b", "b1")}; !reflect.DeepEqual(calls[1], want) {
		t.Fatalf("ops in the second call: got %+v, want %+v", calls[1], want)
	}

	// The dropped op is never sent again.
	w.flush(ctx)
	stop()
	if got := len(applier.callsOf()); got != 2 {
		t.Fatalf("ApplyEngine calls after another flush and stop: got %d, want 2", got)
	}
}

// promptGroups returns one SessionOps per id, each holding a single prompt op for that session.
func promptGroups(ids ...string) []SessionOps {
	groups := make([]SessionOps, 0, len(ids))
	for _, id := range ids {
		groups = append(groups, SessionOps{ID: id, Ops: []store.EngineOp{prompt(id, id+"-1")}})
	}
	return groups
}

// Many sessions passed to one CommitAll reach the Applier in one ApplyEngine call, whatever the tick.
func TestCommitAllStoresManySessionsInOneApplyCall(t *testing.T) {
	for _, tc := range []struct {
		name string
		tick time.Duration
	}{
		{name: "short tick", tick: time.Millisecond},
		{name: "default tick", tick: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, path := openStore(t)
			ids := make([]string, 1000)
			for i := range ids {
				ids[i] = fmt.Sprintf("s%04d", i)
			}
			addSessions(t, s, ids...)
			applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
			w := NewWriter(WriterConfig{Applier: applier, Tick: tc.tick})
			startRun(t, w)

			errs, err := w.CommitAll(t.Context(), promptGroups(ids...))
			if err != nil {
				t.Fatalf("CommitAll: %v", err)
			}
			if len(errs) != len(ids) {
				t.Fatalf("errors: got %d, want one per group (%d)", len(errs), len(ids))
			}
			for i, e := range errs {
				if e != nil {
					t.Fatalf("group %s: got error %v, want nil", ids[i], e)
				}
			}

			calls := applier.callsOf()
			if len(calls) != 1 {
				t.Fatalf("ApplyEngine calls: got %d, want 1", len(calls))
			}
			if got := len(calls[0]); got != len(ids) {
				t.Fatalf("ops in the call: got %d, want %d", got, len(ids))
			}
			for _, id := range []string{ids[0], ids[len(ids)-1]} {
				if got, want := promptTexts(t, path, id), []string{id + "-1"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("session %s stored texts: got %v, want %v", id, got, want)
				}
			}
		})
	}
}

// A group whose row is gone gets its dropped error; every other group gets nil and is stored in the same tick.
func TestCommitAllDropsOnlyTheGroupWhoseRowIsGone(t *testing.T) {
	ctx := t.Context()
	s, path := openStore(t)
	// Session a is never added, so its row is gone.
	addSessions(t, s, "b", "c")

	var logs bytes.Buffer
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{
		Applier: applier, Tick: 5 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	startRun(t, w)

	idle := "idle"
	groups := []SessionOps{
		{ID: "b", Ops: []store.EngineOp{prompt("b", "b1")}},
		{ID: "a", Ops: []store.EngineOp{store.EngineSet{ID: "a", Changes: store.EngineChanges{State: &idle}}}},
		{ID: "c", Ops: []store.EngineOp{prompt("c", "c1")}},
	}
	errs, err := w.CommitAll(ctx, groups)
	if err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	if len(errs) != 3 {
		t.Fatalf("errors: got %d, want 3", len(errs))
	}
	if errs[0] != nil || errs[2] != nil {
		t.Fatalf("groups b and c: got errors %v and %v, want nil", errs[0], errs[2])
	}
	if !errors.Is(errs[1], store.ErrNoEngineSession) {
		t.Fatalf("group a: got %v, want an error wrapping ErrNoEngineSession", errs[1])
	}
	if got, want := promptTexts(t, path, "b"), []string{"b1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session b stored texts: got %v, want %v", got, want)
	}
	if got, want := promptTexts(t, path, "c"), []string{"c1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("session c stored texts: got %v, want %v", got, want)
	}
	if !strings.Contains(logs.String(), "session=a") {
		t.Fatalf("warning does not name session a: %q", logs.String())
	}

	calls := applier.callsOf()
	if len(calls) != 2 {
		t.Fatalf("ApplyEngine calls: got %d, want 2 (the batch, then the batch without a's op)", len(calls))
	}
	if want := []store.EngineOp{prompt("b", "b1"), prompt("c", "c1")}; !reflect.DeepEqual(calls[1], want) {
		t.Fatalf("ops in the second call: got %+v, want %+v", calls[1], want)
	}
}

// A stopped writer makes CommitAll return ErrWriterStopped and no per-group errors.
func TestCommitAllAfterRunStopsReturnsErrWriterStopped(t *testing.T) {
	s, _ := openStore(t)
	addSessions(t, s, "a")
	applier := &recordingApplier{store: s, notify: make(chan struct{}, 1)}
	w := NewWriter(WriterConfig{Applier: applier, Tick: time.Hour})
	stop := startRun(t, w)
	stop()

	errs, err := w.CommitAll(t.Context(), promptGroups("a"))
	if !errors.Is(err, ErrWriterStopped) {
		t.Fatalf("CommitAll after Run stopped: got %v, want ErrWriterStopped", err)
	}
	if errs != nil {
		t.Fatalf("errors after Run stopped: got %v, want nil", errs)
	}
}

// No group, or groups with no ops, return at once: the writer is not running, and the context is already canceled, so
// any wait would fail.
func TestCommitAllWithNoOpsReturnsAtOnce(t *testing.T) {
	w := NewWriter(WriterConfig{Tick: time.Hour})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	errs, err := w.CommitAll(ctx, nil)
	if err != nil || len(errs) != 0 {
		t.Fatalf("CommitAll with no groups: got %v, %v; want no errors and no error", errs, err)
	}
	errs, err = w.CommitAll(ctx, []SessionOps{{ID: "a"}, {ID: "b", Ops: []store.EngineOp{}}})
	if err != nil {
		t.Fatalf("CommitAll with groups that have no ops: got error %v", err)
	}
	if len(errs) != 2 || errs[0] != nil || errs[1] != nil {
		t.Fatalf("CommitAll with groups that have no ops: got %v, want two nil errors", errs)
	}
}
