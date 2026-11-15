package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/joshsymonds/orrery/internal/waittest"
)

// withConnectGate runs gate before each connection the store opens.
func withConnectGate(gate func(context.Context)) Option {
	return func(c *openConfig) { c.connectGate = gate }
}

// withCloseWaiting runs waiting once Close has closed the pool and is about to wait for the connections left.
func withCloseWaiting(waiting func()) Option {
	return func(c *openConfig) { c.closeWaiting = waiting }
}

// TestCloseWaitsForAConnectThePoolStartsOnItsOwn has database/sql open a connection on its own goroutine, as it does
// when the store's only connection is discarded while a call waits for one, holds that open, and closes the store.
// Close must wait for the open and its connection's close, so no -wal or -shm file appears after Close returns.
func TestCloseWaitsForAConnectThePoolStartsOnItsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedMigrated(t, path)
	var connects atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	waiting := make(chan struct{})
	s, err := Open(ctx, path, noSync(),
		withConnectGate(func(context.Context) {
			// The first connection is Open's own; the second is the one the pool opens on its own goroutine.
			if connects.Add(1) == 2 {
				close(entered)
				<-release
			}
		}),
		withCloseWaiting(func() { close(waiting) }),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	held, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatalf("take the store's connection: %v", err)
	}
	waitCtx, cancelWait := context.WithCancel(ctx)
	defer cancelWait()
	waited := make(chan error, 1)
	go func() {
		conn, connErr := s.db.Conn(waitCtx)
		if connErr == nil {
			connErr = conn.Close()
		}
		waited <- connErr
	}()
	waitEnded, waitedResult := waittest.Closed(waited)
	// The pool counts a call as waiting once it has queued it.
	waittest.Until(t, waitEnded, "the pool's queued Conn call", func() bool { return s.db.Stats().WaitCount > 0 })
	// Discarding the held connection while the call waits makes the pool open another on its own goroutine.
	err = held.Raw(func(any) error { return driver.ErrBadConn })
	if !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("discard the held connection: %v, want driver.ErrBadConn", err)
	}
	<-entered

	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v while the pool's own connect was still open", err)
	case <-waiting:
	}
	close(release)
	if err := <-closed; err != nil {
		t.Fatalf("Close: %v", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Lstat(path + suffix); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Lstat(%s) after Close = %v, want the file gone", filepath.Base(path+suffix), err)
		}
	}
	cancelWait()
	<-waitedResult
}
