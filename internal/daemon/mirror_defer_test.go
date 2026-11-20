package daemon_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
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

// awaitPaneRow waits until m has run the deferred work it holds, then checks the pane's row and its events with
// checkPaneRow. Wait returns once no sync or tidy is deferred or running, so the row and the pane's @orrery_id are
// written by then; the wait has no deadline of its own, and the test binary's timeout ends one that never does.
func awaitPaneRow(t *testing.T, m *daemon.Mirror, srv testServer, db *sql.DB) {
	t.Helper()
	m.Wait()
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
	awaitPaneRow(t, m, srv, db)
}
