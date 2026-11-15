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

// mirrorSyncDroppedMsg is the log message a dropped pane sync writes.
const mirrorSyncDroppedMsg = "mirror sync dropped"

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

func TestMirrorAttachFrameBeforeAndAfterOpen(t *testing.T) {
	srv := startServer(t)
	st, db := openFrameStore(t)
	logs, logger := newAttachLogs()
	m := daemon.NewPendingMirror(time.Now, logger)
	socket := serve(t, daemon.Config{Server: srv.server, Mirror: m, Logger: logger})

	sendFrame(t, socket, frameFor(userPromptSubmitHook, srv.pane, srv.socket))
	wantOption(t, srv, protocol.OptionState, "working")
	wantOption(t, srv, protocol.OptionID, "")
	if rows := listSessions(t, db); len(rows) != 0 {
		t.Fatalf("rows before Attach = %+v, want none", rows)
	}
	if dropped := tidyLogMessages(t, logs.snapshot(), mirrorSyncDroppedMsg); len(dropped) != 1 {
		t.Fatalf("%q records before Attach = %d, want 1", mirrorSyncDroppedMsg, len(dropped))
	}

	m.Attach(st)
	sendFrame(t, socket, frameFor(permissionHook, srv.pane, srv.socket))
	wantOption(t, srv, protocol.OptionState, "waiting")
	checkPaneRow(t, srv, db)
	if dropped := tidyLogMessages(t, logs.snapshot(), mirrorSyncDroppedMsg); len(dropped) != 1 {
		t.Fatalf("%q records after Attach = %d, want still 1", mirrorSyncDroppedMsg, len(dropped))
	}
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
	<-synced
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

func TestMirrorAttachTidyBeforeOpenWaitsThenDrops(t *testing.T) {
	srv := startServer(t)
	logs, logger := newAttachLogs()
	m := daemon.NewPendingMirror(time.Now, logger)
	// The tidy's wait for the store runs on a channel the test closes, so it drops only once the test lets the wait
	// expire.
	armed := make(chan struct{}, 1)
	expire := make(chan time.Time)
	daemon.SetMirrorLockWait(m, func() <-chan time.Time {
		armed <- struct{}{}
		return expire
	})

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
	if dropped := tidyLogMessages(t, logs.snapshot(), tidyDroppedMsg); len(dropped) != 1 {
		t.Fatalf("%q records = %d, want 1", tidyDroppedMsg, len(dropped))
	}
	wantOption(t, srv, protocol.OptionID, "")
}
