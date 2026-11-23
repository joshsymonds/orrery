package daemon

import (
	"bytes"
	"database/sql"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// mirrorLogs is a log destination the drain's goroutine writes while the test reads it.
type mirrorLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p under the lock.
func (l *mirrorLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// count returns how many logged lines hold every one of parts.
func (l *mirrorLogs) count(parts ...string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for line := range strings.SplitSeq(l.buf.String(), "\n") {
		matched := line != ""
		for _, part := range parts {
			matched = matched && strings.Contains(line, part)
		}
		if matched {
			n++
		}
	}
	return n
}

// busyMirror is a mirror over a store whose busy timeout is 0, so a write while the test holds the write lock fails busy
// at once, with the drain's wait before rerunning busy work on channels the test drives.
type busyMirror struct {
	m    *Mirror
	db   *sql.DB
	logs *mirrorLogs
	// armed receives once each time the drain begins its wait before rerunning busy work, and retry ends that wait.
	armed chan struct{}
	retry chan time.Time
	lock  *sql.Conn
}

// newBusyMirror returns a busyMirror whose store's write lock the test holds.
func newBusyMirror(t *testing.T) busyMirror {
	t.Helper()
	st, path := openMirrorStore(t, store.WithBusyTimeout(0))
	db := openMirrorDB(t, path)
	logs := &mirrorLogs{}
	bm := busyMirror{
		m: NewMirror(st, mirrorNow, slog.New(slog.NewTextHandler(logs, nil))), db: db, logs: logs,
		armed: make(chan struct{}, 1), retry: make(chan time.Time),
	}
	bm.m.busyWait = func() <-chan time.Time {
		bm.armed <- struct{}{}
		return bm.retry
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close lock connection: %v", err)
		}
	})
	if _, err = conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold store write lock: %v", err)
	}
	bm.lock = conn
	return bm
}

// release frees the store's write lock.
func (bm busyMirror) release(t *testing.T) {
	t.Helper()
	if _, err := bm.lock.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatalf("release store write lock: %v", err)
	}
}

// TestMirrorDefersSyncWhenStoreWriteLockedAndSyncsOnceItFrees syncs a pane while the store's write lock is held. The
// sync is deferred, not dropped, and changes nothing. The drain waits before each rerun, and a rerun the lock still
// refuses waits again. Once the lock frees, the next rerun stores the pane's row and sets its id.
func TestMirrorDefersSyncWhenStoreWriteLockedAndSyncsOnceItFrees(t *testing.T) {
	ms := startMirrorServer(t)
	setMirrorValues(t, ms)
	bm := newBusyMirror(t)

	bm.m.SyncPane(t.Context(), ms.server, ms.pane)
	<-bm.armed
	if n := bm.logs.count("mirror sync deferred", "store busy"); n != 1 {
		t.Fatalf("busy deferrals logged = %d, want 1", n)
	}
	if id := mirrorOption(t, ms, protocol.OptionID); id != "" {
		t.Fatalf("%s = %q after a busy store write, want unset", protocol.OptionID, id)
	}
	if got := mirrorOption(t, ms, protocol.OptionState); got != mirrorValues()[protocol.OptionState] {
		t.Fatalf("%s = %q after a busy store write, want unchanged %q", protocol.OptionState, got,
			mirrorValues()[protocol.OptionState])
	}
	if rows := mirrorRows(t, bm.db); len(rows) != 0 {
		t.Fatalf("rows after a busy store write = %+v, want none", rows)
	}

	bm.retry <- time.Time{}
	<-bm.armed
	if n := bm.logs.count("mirror sync deferred", "store busy"); n != 2 {
		t.Fatalf("busy deferrals logged after a rerun under the lock = %d, want 2", n)
	}

	bm.release(t)
	bm.retry <- time.Time{}
	bm.m.Wait()
	id := mirrorOption(t, ms, protocol.OptionID)
	if !mirrorIDPattern.MatchString(id) {
		t.Fatalf("%s = %q after the lock freed, want a minted id", protocol.OptionID, id)
	}
	rows := mirrorRows(t, bm.db)
	if want := wantMirrorRow(id, "-L "+ms.server.Name, ms.pane); len(rows) != 1 || rows[0] != want {
		t.Fatalf("rows after the lock freed = %+v, want one row %+v", rows, want)
	}
	if n := bm.logs.count("mirror sync dropped"); n != 0 {
		t.Fatalf("dropped syncs logged = %d, want none", n)
	}
}

// TestMirrorDefersTidyWhenStoreWriteLockedAndTidiesOnceItFrees tidies a server while the store's write lock is held.
// The tidy is deferred, not dropped, and the drain's rerun once the lock frees stores the server's pane.
func TestMirrorDefersTidyWhenStoreWriteLockedAndTidiesOnceItFrees(t *testing.T) {
	ms := startMirrorServer(t)
	setMirrorValues(t, ms)
	bm := newBusyMirror(t)

	bm.m.Tidy(t.Context(), ms.server)
	<-bm.armed
	if n := bm.logs.count("mirror tidy deferred", "store busy"); n != 1 {
		t.Fatalf("busy tidy deferrals logged = %d, want 1", n)
	}
	if rows := mirrorRows(t, bm.db); len(rows) != 0 {
		t.Fatalf("rows after a busy tidy = %+v, want none", rows)
	}

	bm.release(t)
	bm.retry <- time.Time{}
	bm.m.Wait()
	id := mirrorOption(t, ms, protocol.OptionID)
	rows := mirrorRows(t, bm.db)
	if want := wantMirrorRow(id, "-L "+ms.server.Name, ms.pane); len(rows) != 1 || rows[0] != want {
		t.Fatalf("rows after the lock freed = %+v, want one row %+v", rows, want)
	}
	if n := bm.logs.count("mirror tidy dropped"); n != 0 {
		t.Fatalf("dropped tidies logged = %d, want none", n)
	}
}

// TestMirrorWaitDropsASyncTheStoreStillRefuses defers a sync for a busy store, then calls Wait with the lock still held.
// Wait ends the drain's wait at once, the drain's last rerun is refused and logged as dropped, and Wait returns.
func TestMirrorWaitDropsASyncTheStoreStillRefuses(t *testing.T) {
	ms := startMirrorServer(t)
	setMirrorValues(t, ms)
	bm := newBusyMirror(t)

	bm.m.SyncPane(t.Context(), ms.server, ms.pane)
	<-bm.armed
	bm.m.Wait()
	if n := bm.logs.count("mirror sync dropped", "database is locked"); n != 1 {
		t.Fatalf("dropped syncs logged at Wait = %d, want 1", n)
	}
	if rows := mirrorRows(t, bm.db); len(rows) != 0 {
		t.Fatalf("rows after Wait under the lock = %+v, want none", rows)
	}

	bm.m.SyncPane(t.Context(), ms.server, ms.pane)
	if n := bm.logs.count("mirror sync dropped", "database is locked"); n != 2 {
		t.Fatalf("dropped syncs logged after Wait = %d, want 2: a busy sync is no longer deferred", n)
	}
}
