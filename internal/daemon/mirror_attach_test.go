package daemon_test

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// attachLogs collects the mirror's log output. The daemon's connection goroutines write it while the test reads it.
type attachLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *attachLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// snapshot returns the log so far as a buffer that tidyLogRecords and tidyLogMessages read.
func (l *attachLogs) snapshot() *bytes.Buffer {
	l.mu.Lock()
	defer l.mu.Unlock()
	return bytes.NewBufferString(l.buf.String())
}

// newAttachLogs returns a JSON logger that writes to a new attachLogs.
func newAttachLogs() (*attachLogs, *slog.Logger) {
	logs := &attachLogs{}
	return logs, slog.New(slog.NewJSONHandler(logs, nil))
}

func TestMirrorAttachFrameBeforeOpenLandsAfterOpen(t *testing.T) {
	srv := startServer(t)
	st, db := openFrameStore(t)
	logs, logger := newAttachLogs()
	m := daemon.NewPendingMirror(time.Now, logger)
	// Every wait for the store or the lock has already expired, so the frame's sync is deferred at once.
	expired := make(chan time.Time)
	close(expired)
	daemon.SetMirrorLockWait(m, func() <-chan time.Time { return expired })
	socket := serve(t, daemon.Config{Server: srv.server, Mirror: m, Logger: logger})

	sendFrame(t, socket, frameFor(userPromptSubmitHook, srv.pane, srv.socket))
	wantOption(t, srv, protocol.OptionState, "working")
	wantOption(t, srv, protocol.OptionID, "")
	if rows := listSessions(t, db); len(rows) != 0 {
		t.Fatalf("rows before Attach = %+v, want none", rows)
	}
	deferred := tidyLogMessages(t, logs.snapshot(), mirrorSyncDeferredMsg)
	if len(deferred) != 1 || deferred[0]["reason"] != "store still opening" {
		t.Fatalf("%q records before Attach = %v, want one for the store still opening", mirrorSyncDeferredMsg, deferred)
	}

	// The deferred sync lands once the store is attached, with no further frame.
	m.Attach(st)
	awaitPaneRow(t, srv, db)

	sendFrame(t, socket, frameFor(permissionHook, srv.pane, srv.socket))
	wantOption(t, srv, protocol.OptionState, "waiting")
	awaitPaneRow(t, srv, db)
}

func TestMirrorAttachWaitingSyncWritesRow(t *testing.T) {
	srv := startServer(t)
	st, db := openFrameStore(t)
	_, logger := newAttachLogs()
	m := daemon.NewPendingMirror(time.Now, logger)
	// The sync's wait for the store never expires, and armed reports that the sync has reached it.
	armed := make(chan struct{}, 1)
	daemon.SetMirrorLockWait(m, func() <-chan time.Time {
		armed <- struct{}{}
		return nil
	})
	if err := srv.client.SetPaneOption(t.Context(), srv.pane, protocol.OptionState, "waiting"); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}

	synced := make(chan struct{})
	go func() {
		defer close(synced)
		m.SyncPane(t.Context(), srv.server, srv.pane)
	}()
	<-armed
	m.Attach(st)
	select {
	case <-synced:
	case <-time.After(ioTimeout):
		t.Fatal("SyncPane did not return after Attach")
	}
	checkPaneRow(t, srv, db)
}

func TestMirrorAttachNilIsSilent(t *testing.T) {
	srv := startServer(t)
	logs, logger := newAttachLogs()
	m := daemon.NewPendingMirror(time.Now, logger)
	m.Attach(nil)
	socket := serve(t, daemon.Config{Server: srv.server, Mirror: m, Logger: logger})

	sendFrame(t, socket, frameFor(userPromptSubmitHook, srv.pane, srv.socket))
	wantOption(t, srv, protocol.OptionState, "working")
	wantOption(t, srv, protocol.OptionID, "")

	// A tidy would mint an id for the pane, so an unset id shows that the tidy made no tmux call.
	m.Tidy(t.Context(), srv.server)
	wantOption(t, srv, protocol.OptionID, "")

	for _, record := range tidyLogRecords(t, logs.snapshot()) {
		if msg, _ := record["msg"].(string); strings.HasPrefix(msg, "mirror") {
			t.Fatalf("mirror logged after Attach(nil): %v", record)
		}
	}
}

func TestMirrorAttachTidyBeforeOpenWaitsThenLandsAfterOpen(t *testing.T) {
	srv := startServer(t)
	st, db := openFrameStore(t)
	logs, logger := newAttachLogs()
	m := daemon.NewPendingMirror(time.Now, logger)
	// The tidy's wait for the store runs on a channel the test closes, so it is deferred only once the test lets the
	// wait expire.
	armed, expire := expiringLockWait(m)

	tidied := make(chan struct{})
	go func() {
		m.Tidy(t.Context(), srv.server)
		close(tidied)
	}()
	<-armed
	select {
	case <-tidied:
		t.Fatal("Tidy before Attach returned before its wait for the store expired")
	default:
	}
	close(expire)
	<-tidied
	if deferred := tidyLogMessages(t, logs.snapshot(), mirrorTidyDeferredMsg); len(deferred) != 1 {
		t.Fatalf("%q records = %d, want 1", mirrorTidyDeferredMsg, len(deferred))
	}
	wantOption(t, srv, protocol.OptionID, "")
	if rows := listSessions(t, db); len(rows) != 0 {
		t.Fatalf("rows before Attach = %+v, want none", rows)
	}

	// The deferred tidy lands once the store is attached: it mints the pane's id and writes its row.
	m.Attach(st)
	awaitPaneRow(t, srv, db)
}
