package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// storeRetryBusyLine and storeRetryOpenLine are the messages openStore logs for a busy open and for an open that
// succeeded. The text handler writes each as msg followed by the quoted message.
const (
	storeRetryBusyLine = `msg="store busy, retrying open"`
	storeRetryOpenLine = `msg="store open"`
	storeRetryNoStore  = "serving without it"
)

// holdStoreWriteLock takes the write lock of the store file at path, on a connection of the test's own, and returns the
// func that releases it. The lock is released by the first call or when the test ends, whichever comes first.
func holdStoreWriteLock(t *testing.T, path string) func() {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open lock database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close lock database: %v", err)
		}
	})
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("lock connection: %v", err)
	}
	if _, err = conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("take write lock: %v", err)
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			if _, err := conn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
				t.Errorf("release write lock: %v", err)
			}
			if err := conn.Close(); err != nil {
				t.Errorf("close lock connection: %v", err)
			}
		})
	}
	t.Cleanup(release)
	return release
}

// storeRetryEpics returns an Epics client that dials the session socket at path.
func storeRetryEpics(t *testing.T, path string) enginev1connect.EpicsClient {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", path)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return enginev1connect.NewEpicsClient(&http.Client{Transport: transport}, "http://epics.invalid")
}

// TestStoreOpenRetryServesWithStoreAfterBusyOpen checks that a --sessions orreryd whose store is busy when it opens tries
// the open again only when its retry timer fires, and then serves with the store: the store opens, the Epics service
// answers ListEpics, and orreryd never logs that it serves without the store. The store file is empty, so its
// migrations must write, and the write waits on the lock the test holds. The busy timeout is 0, so the refused write
// waits out no timeout.
func TestStoreOpenRetryServesWithStoreAfterBusyOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	getenv := metricsWiringEnv(t)
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make store directory: %v", err)
	}
	if err = os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("make empty store file: %v", err)
	}
	release := holdStoreWriteLock(t, path)

	notify := filepath.Join(t.TempDir(), "notify.sock")
	ready, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: notify, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen notify socket: %v", err)
	}
	t.Cleanup(func() {
		if err := ready.Close(); err != nil {
			t.Errorf("close notify socket: %v", err)
		}
	})

	socket := filepath.Join(t.TempDir(), "orreryd.sock")
	opts, err := parseFlags([]string{
		"--sessions", "--session-shell", "/bin/sh",
		"--socket", socket,
	}, getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	opts.notifySocket = notify
	opts.storeBusy = 0
	// The fake retry timer hands each wait to waits, and the test fires it by sending on ticks.
	waits := make(chan time.Duration, 1)
	ticks := make(chan time.Time, 1)
	opts.storeRetryAfter = func(d time.Duration) <-chan time.Time {
		waits <- d
		return ticks
	}

	var logs logBuffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	errc := make(chan error, 1)
	go func() { errc <- serve(ctx, opts, logger, getenv) }()
	ended, result := waittest.Closed(errc)
	t.Cleanup(func() {
		cancel()
		<-ended
	})

	// The open is busy, so openStore logs it and waits on the retry timer. The wait ends at the first sign of anything
	// else: a store-less serve, a second busy open, or the wait itself. Each of the first two fails the test at once.
	waittest.Until(t, ended, "the store's retry wait", func() bool {
		logged := logs.String()
		return len(waits) > 0 || strings.Contains(logged, storeRetryNoStore) ||
			strings.Contains(logged, storeRetryOpenLine) || strings.Count(logged, storeRetryBusyLine) > 1
	})
	logged := logs.String()
	if strings.Contains(logged, storeRetryNoStore) {
		t.Fatalf("orreryd serves without its store after a busy open; logs %q", logged)
	}
	if n := strings.Count(logged, storeRetryBusyLine); n != 1 {
		t.Fatalf("busy opens logged = %d before the retry timer fired, want 1; logs %q", n, logged)
	}
	if got := <-waits; got != storeOpenRetry {
		t.Fatalf("retry wait = %v, want storeOpenRetry %v", got, storeOpenRetry)
	}

	release()
	ticks <- time.Time{}

	// READY=1 follows startSessions, so the sessions socket listens once it arrives. The read has no deadline of its own:
	// serve's start is real store work, and the test binary's timeout ends a wait that never ends.
	if _, _, err = ready.ReadFromUnix(make([]byte, 64)); err != nil {
		t.Fatalf("read READY=1: %v", err)
	}

	if _, err = storeRetryEpics(t, protocol.SessionSocketPath(socket)).ListEpics(
		ctx, connect.NewRequest(&v1.ListEpicsRequest{}),
	); err != nil {
		t.Fatalf("ListEpics after the retried open: %v", err)
	}
	if n := strings.Count(logs.String(), storeRetryOpenLine); n != 1 {
		t.Errorf("store opens logged = %d, want 1; logs %q", n, logs.String())
	}
	if strings.Contains(logs.String(), storeRetryNoStore) {
		t.Errorf("logs %q say orreryd serves without its store", logs.String())
	}

	cancel()
	if err = <-result; err != nil {
		t.Fatalf("serve: %v", err)
	}
}
