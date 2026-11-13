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

// attachLockWait is the longest a sync or tidy waits for a store that is still opening. A timer never fires early,
// so a dropped tidy has waited at least this long.
const attachLockWait = 200 * time.Millisecond

// attachStartWait is how long a test lets a sync reach its wait before it attaches the store. It leaves most of
// attachLockWait for the attach to arrive in time.
const attachStartWait = 25 * time.Millisecond

// attachAtOnceBudget bounds a call that must return without waiting, such as a tidy after Attach(nil).
const attachAtOnceBudget = time.Second

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
	if err := srv.client.SetPaneOption(t.Context(), srv.pane, protocol.OptionState, "waiting"); err != nil {
		t.Fatalf("SetPaneOption: %v", err)
	}

	synced := make(chan struct{})
	go func() {
		defer close(synced)
		m.SyncPane(t.Context(), srv.server, srv.pane)
	}()
	time.Sleep(attachStartWait)
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
	start := time.Now()
	m.Tidy(t.Context(), srv.server)
	if elapsed := time.Since(start); elapsed > attachAtOnceBudget {
		t.Fatalf("Tidy after Attach(nil) took %v, want at most %v", elapsed, attachAtOnceBudget)
	}
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

	start := time.Now()
	m.Tidy(t.Context(), srv.server)
	if elapsed := time.Since(start); elapsed < attachLockWait {
		t.Fatalf("Tidy before Attach took %v, want at least %v", elapsed, attachLockWait)
	}
	if dropped := tidyLogMessages(t, logs.snapshot(), tidyDroppedMsg); len(dropped) != 1 {
		t.Fatalf("%q records = %d, want 1", tidyDroppedMsg, len(dropped))
	}
	wantOption(t, srv, protocol.OptionID, "")
}
