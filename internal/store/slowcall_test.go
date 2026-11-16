package store

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// slowCallClock is a clock that moves only when a test advances it.
type slowCallClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *slowCallClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *slowCallClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// syncBuffer is a bytes.Buffer safe to write from the store's goroutines and read from the test's.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// slowCallFixture is a store whose calls are timed on clock, with its slow-call warnings in logs and their count in
// counted.
type slowCallFixture struct {
	store   *Store
	clock   *slowCallClock
	logs    *syncBuffer
	counted *atomic.Int32
}

func newSlowCallFixture(t *testing.T) slowCallFixture {
	t.Helper()
	f := slowCallFixture{
		clock:   &slowCallClock{now: time.Unix(1_800_000_000, 0)},
		logs:    &syncBuffer{},
		counted: &atomic.Int32{},
	}
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	s, err := Open(t.Context(), path,
		WithSynchronous(SynchronousOff),
		withCallClock(f.clock.Now),
		WithLogger(slog.New(slog.NewTextHandler(f.logs, nil))),
		WithSlowCallCounter(func(context.Context) { f.counted.Add(1) }),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	})
	f.store = s
	return f
}

// wantSlowCall requires that the store warned of exactly one slow call, whose warning holds each of fields, and
// counted it.
func (f slowCallFixture) wantSlowCall(t *testing.T, fields ...string) {
	t.Helper()
	logs := f.logs.String()
	if got := strings.Count(logs, `msg="slow store call"`); got != 1 {
		t.Fatalf("slow store call warnings = %d, want 1:\n%s", got, logs)
	}
	for _, field := range fields {
		if !strings.Contains(logs, field) {
			t.Fatalf("warning lacks %s:\n%s", field, logs)
		}
	}
	if got := f.counted.Load(); got != 1 {
		t.Fatalf("slow calls counted = %d, want 1", got)
	}
}

// holdConnection takes the store's one connection, so the next call waits for it, and returns its release.
func (f slowCallFixture) holdConnection(t *testing.T) func() {
	t.Helper()
	held, err := f.store.db.Conn(t.Context())
	if err != nil {
		t.Fatalf("take the store's connection: %v", err)
	}
	return func() {
		if closeErr := held.Close(); closeErr != nil {
			t.Fatalf("release the store's connection: %v", closeErr)
		}
	}
}

// awaitQueued waits until a call beyond the before calls the pool has queued waits for the connection. It fails at
// once if ended, the end of that call, closes first.
func (f slowCallFixture) awaitQueued(t *testing.T, before int64, ended <-chan struct{}) {
	t.Helper()
	waittest.Until(t, ended, "the call's wait for the store's connection", func() bool {
		return f.store.db.Stats().WaitCount > before
	})
}

// A query that waits 150 ms for the store's one connection, held by another call, and then runs at once is logged
// with that wait and no run.
func TestSlowCallLogsItsWaitForTheConnection(t *testing.T) {
	t.Parallel()
	f := newSlowCallFixture(t)
	before := f.store.db.Stats().WaitCount
	release := f.holdConnection(t)

	done := make(chan error, 1)
	go func() {
		_, err := f.store.Holds(t.Context())
		done <- err
	}()
	ended, result := waittest.Closed(done)
	f.awaitQueued(t, before, ended)
	f.clock.advance(150 * time.Millisecond)
	release()
	if err := <-result; err != nil {
		t.Fatalf("Holds: %v", err)
	}
	f.wantSlowCall(t, "queries=ListHolds", "wait=150ms", "run=0s")
}

// A read transaction that waits for the connection is one call from BeginTx to its end, named by every query it ran.
func TestSlowReadTransactionIsOneCallUntilItEnds(t *testing.T) {
	t.Parallel()
	f := newSlowCallFixture(t)
	before := f.store.db.Stats().WaitCount
	release := f.holdConnection(t)

	done := make(chan error, 1)
	go func() {
		_, err := f.store.Pushed(t.Context())
		done <- err
	}()
	ended, result := waittest.Closed(done)
	f.awaitQueued(t, before, ended)
	f.clock.advance(150 * time.Millisecond)
	release()
	if err := <-result; err != nil {
		t.Fatalf("Pushed: %v", err)
	}
	f.wantSlowCall(t, "wait=150ms", "run=0s",
		"queries=BEGIN,ListPushedNotices,ListPushedEpicNotices,ListPushedLimitNotices,ListPushedAlertNotices ")
}

// A write that gets the connection at once and then runs 150 ms is logged with no wait and that run, named by its
// begin, its queries and its commit.
func TestSlowWriteLogsItsRunOnTheConnection(t *testing.T) {
	t.Parallel()
	f := newSlowCallFixture(t)
	err := f.store.write(t.Context(), func(queries *sqlcgen.Queries) error {
		f.clock.advance(150 * time.Millisecond)
		_, listErr := queries.ListHolds(t.Context())
		return listErr
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f.wantSlowCall(t, `queries="BEGIN IMMEDIATE,ListHolds,COMMIT"`, "wait=0s", "run=150ms")
}

// A call of exactly 100 ms is not over the threshold: it is neither logged nor counted.
func TestStoreCallOfTheThresholdIsNotSlow(t *testing.T) {
	t.Parallel()
	f := newSlowCallFixture(t)
	err := f.store.write(t.Context(), func(queries *sqlcgen.Queries) error {
		f.clock.advance(slowCallThreshold)
		_, listErr := queries.ListHolds(t.Context())
		return listErr
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if logs := f.logs.String(); logs != "" {
		t.Fatalf("a call of the threshold was logged:\n%s", logs)
	}
	if got := f.counted.Load(); got != 0 {
		t.Fatalf("slow calls counted = %d, want 0", got)
	}
}
