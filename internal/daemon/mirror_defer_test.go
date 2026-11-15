package daemon_test

import (
	"database/sql"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// Log messages of a sync and a tidy that could not start within the mirror's lock wait.
const (
	mirrorSyncDeferredMsg = "mirror sync deferred"
	mirrorTidyDeferredMsg = "mirror tidy deferred"
)

// expiringLockWait makes m's waits for the store and the lock expire only when the returned channel closes. Each wait
// that m arms sends on armed first.
func expiringLockWait(m *daemon.Mirror) (armed <-chan struct{}, expire chan time.Time) {
	armedCh := make(chan struct{}, 1)
	expire = make(chan time.Time)
	daemon.SetMirrorLockWait(m, func() <-chan time.Time {
		armedCh <- struct{}{}
		return expire
	})
	return armedCh, expire
}

// paneRowMatches reports whether the pane has an @orrery_id and its row equals its tmux options.
func paneRowMatches(t *testing.T, srv testServer, db *sql.DB) bool {
	t.Helper()
	if id, ok := option(t, srv, protocol.OptionID); !ok || id == "" {
		return false
	}
	want := tmuxRow(t, srv)
	row, err := sqlcgen.New(db).GetSession(t.Context(), want["id"])
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("GetSession(%s): %v", want["id"], err)
	}
	return maps.Equal(rowMap(row), want)
}

// awaitPaneRow polls until the pane's row equals its tmux options, then checks the row and its events with
// checkPaneRow. ioTimeout is a hang guard, never a timing assumption: the deferred work runs as soon as the store and
// the lock are free.
func awaitPaneRow(t *testing.T, srv testServer, db *sql.DB) {
	t.Helper()
	guard := time.After(ioTimeout)
	for !paneRowMatches(t, srv, db) {
		select {
		case <-guard:
			t.Fatalf("pane %s row never matched its tmux options %v", srv.pane, tmuxRow(t, srv))
		case <-time.After(10 * time.Millisecond):
		}
	}
	checkPaneRow(t, srv, db)
}

// runPastLockWait runs fn, which waits on m's lock wait, lets that wait expire once fn has armed it, and returns once
// fn has.
func runPastLockWait(t *testing.T, armed <-chan struct{}, expire chan time.Time, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-armed
	close(expire)
	<-done
}

func TestMirrorTidyPastTheLockWaitLandsOnceTheLockFrees(t *testing.T) {
	srv := startServer(t)
	st, db := openFrameStore(t)
	logs, logger := newAttachLogs()
	m := daemon.NewMirror(st, time.Now, logger)
	armed, expire := expiringLockWait(m)
	if err := srv.client.SetPaneOption(t.Context(), srv.pane, protocol.OptionState, "working"); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}

	free := daemon.HoldMirrorLock(m)
	runPastLockWait(t, armed, expire, func() { m.Tidy(t.Context(), srv.server) })
	if rows := listSessions(t, db); len(rows) != 0 {
		t.Fatalf("rows while the lock is held = %+v, want none", rows)
	}
	wantOption(t, srv, protocol.OptionID, "")
	if deferred := tidyLogMessages(t, logs.snapshot(), mirrorTidyDeferredMsg); len(deferred) != 1 {
		t.Fatalf("%q records = %d, want 1", mirrorTidyDeferredMsg, len(deferred))
	}

	free()
	awaitPaneRow(t, srv, db)
}
