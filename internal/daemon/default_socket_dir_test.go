package daemon_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	// defaultSocketTidyInterval is the tidy period these tests serve with.
	defaultSocketTidyInterval = 20 * time.Millisecond
	// severalTidies is long enough for the tidy to run about ten times at defaultSocketTidyInterval.
	severalTidies = 10 * defaultSocketTidyInterval
	// defaultSocketSkipMsg is the warning the tidy logs when it skips the default socket.
	defaultSocketSkipMsg = "tmux default socket skipped"
)

// lockedBuffer collects log output from Serve's tidy goroutine while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// skipRecords returns the default socket skip records in logs.
func skipRecords(t *testing.T, logs *lockedBuffer) []map[string]any {
	t.Helper()
	var matched []map[string]any
	for line := range strings.SplitSeq(logs.String(), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if record["msg"] == defaultSocketSkipMsg {
			matched = append(matched, record)
		}
	}
	return matched
}

// waitSkipRecords waits until logs holds at least n skip records.
func waitSkipRecords(t *testing.T, logs *lockedBuffer, n int) {
	t.Helper()
	deadline := time.Now().Add(ioTimeout)
	for {
		if len(skipRecords(t, logs)) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d %q records not logged after %v; logs:\n%s", n, defaultSocketSkipMsg, ioTimeout, logs.String())
		}
		time.Sleep(tidyPollInterval)
	}
}

// checkSkipRecord fails the test unless record is a warning for server with reason.
func checkSkipRecord(t *testing.T, record map[string]any, server, reason string) {
	t.Helper()
	if level := record["level"]; level != "WARN" {
		t.Fatalf("%q level = %v, want WARN", defaultSocketSkipMsg, level)
	}
	if got := record["server"]; got != server {
		t.Fatalf("%q server = %v, want %q", defaultSocketSkipMsg, got, server)
	}
	if got := record["reason"]; got != reason {
		t.Fatalf("%q reason = %v, want %q", defaultSocketSkipMsg, got, reason)
	}
}

// socketDirPath returns the tmux-<uid> directory under base that holds a default socket.
func socketDirPath(base string) string {
	return filepath.Join(base, fmt.Sprintf("tmux-%d", os.Getuid()))
}

// makeSocketDir creates dir, a child of an existing directory, with exactly mode, free of the umask.
func makeSocketDir(t *testing.T, dir string, mode os.FileMode) {
	t.Helper()
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("Mkdir(%s): %v", dir, err)
	}
	setMode(t, dir, mode)
}

// setMode sets path to exactly mode, free of the umask.
func setMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod(%s, %o): %v", path, mode, err)
	}
}

// startSocketServer starts a tmux server on socket with one session and returns its pane id. Cleanup kills the
// server.
func startSocketServer(t *testing.T, socket string) string {
	t.Helper()
	client, err := tmuxctl.New(tmuxctl.Server{Socket: socket})
	if err != nil {
		t.Fatalf("New(%s): %v", socket, err)
	}
	t.Cleanup(func() {
		_, err := client.Run(context.Background(), "kill-server")
		if err != nil && !strings.Contains(err.Error(), "no server running") &&
			!strings.Contains(err.Error(), "error connecting to") {
			t.Errorf("kill-server: %v", err)
		}
	})
	if _, err := client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "s1",
		"-c", t.TempDir(), testCommand); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	return strings.Fields(out)[0]
}

// newSocketPane adds a window to the session on socket and returns its pane id.
func newSocketPane(t *testing.T, socket string) string {
	t.Helper()
	client, err := tmuxctl.New(tmuxctl.Server{Socket: socket})
	if err != nil {
		t.Fatalf("New(%s): %v", socket, err)
	}
	out, err := client.Run(t.Context(), "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "s1", testCommand)
	if err != nil {
		t.Fatalf("new-window: %v", err)
	}
	return strings.TrimSpace(out)
}

// assertNoPaneID fails the test when pane on socket has an @orrery_id.
func assertNoPaneID(t *testing.T, socket, pane string) {
	t.Helper()
	client, err := tmuxctl.New(tmuxctl.Server{Socket: socket})
	if err != nil {
		t.Fatalf("New(%s): %v", socket, err)
	}
	value, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionID)
	if err != nil {
		t.Fatalf("PaneOption(%s, %s): %v", pane, protocol.OptionID, err)
	}
	if ok {
		t.Fatalf("pane %s has %s = %q, want the tidy to skip it", pane, protocol.OptionID, value)
	}
}

// hasSocketRow reports whether the store has a row for pane under the server column of socket.
func hasSocketRow(t *testing.T, db *sql.DB, socket, pane string) bool {
	t.Helper()
	for _, row := range listSessions(t, db) {
		if row.Server == "-S "+socket && row.Pane == pane {
			return true
		}
	}
	return false
}

// serveDefaultSocket runs Serve with socket as the default server, a mirror on st, and the tidy at
// defaultSocketTidyInterval. Log output goes to logs.
func serveDefaultSocket(t *testing.T, st *store.Store, socket string, logs *lockedBuffer) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	serve(t, daemon.Config{
		DefaultServer: tmuxctl.Server{Socket: socket},
		Mirror:        daemon.NewMirror(st, tidyClock, logger),
		TidyInterval:  defaultSocketTidyInterval,
		Logger:        logger,
	})
}

// TestDefaultSocketAccessibleToOthersSkipped: a directory with other-user access is never contacted, and the skip
// is logged once.
func TestDefaultSocketAccessibleToOthersSkipped(t *testing.T) {
	socket := filepath.Join(socketDirPath(t.TempDir()), "default")
	makeSocketDir(t, filepath.Dir(socket), 0o777)
	pane := startSocketServer(t, socket)
	st, db := openFrameStore(t)
	logs := &lockedBuffer{}
	serveDefaultSocket(t, st, socket, logs)

	waitSkipRecords(t, logs, 1)
	time.Sleep(severalTidies)

	if rows := listSessions(t, db); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}
	assertNoPaneID(t, socket, pane)
	records := skipRecords(t, logs)
	if len(records) != 1 {
		t.Fatalf("%q records = %d, want 1; logs:\n%s", defaultSocketSkipMsg, len(records), logs.String())
	}
	checkSkipRecord(t, records[0], "-S "+socket, "other users have access")
}

// TestDefaultSocketSymlinkDirSkipped: a tmux-<uid> symlink to a private directory that holds the server is never
// contacted.
func TestDefaultSocketSymlinkDirSkipped(t *testing.T) {
	base := t.TempDir()
	private := filepath.Join(base, "private")
	makeSocketDir(t, private, 0o700)
	dir := socketDirPath(base)
	if err := os.Symlink(private, dir); err != nil {
		t.Fatalf("Symlink(%s, %s): %v", private, dir, err)
	}
	pane := startSocketServer(t, filepath.Join(private, "default"))
	socket := filepath.Join(dir, "default")
	st, db := openFrameStore(t)
	logs := &lockedBuffer{}
	serveDefaultSocket(t, st, socket, logs)

	waitSkipRecords(t, logs, 1)
	time.Sleep(severalTidies)

	if rows := listSessions(t, db); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none", rows)
	}
	assertNoPaneID(t, filepath.Join(private, "default"), pane)
	records := skipRecords(t, logs)
	if len(records) != 1 {
		t.Fatalf("%q records = %d, want 1; logs:\n%s", defaultSocketSkipMsg, len(records), logs.String())
	}
	checkSkipRecord(t, records[0], "-S "+socket, "not a plain directory")
}

// TestDefaultSocketPrivateDirToggles: a 0700 directory is tidied, a 0777 chmod stops the tidy with one skip
// record, and a chmod back to 0700 tidies again.
func TestDefaultSocketPrivateDirToggles(t *testing.T) {
	socket := filepath.Join(socketDirPath(t.TempDir()), "default")
	dir := filepath.Dir(socket)
	makeSocketDir(t, dir, 0o700)
	first := startSocketServer(t, socket)
	st, db := openFrameStore(t)
	logs := &lockedBuffer{}
	serveDefaultSocket(t, st, socket, logs)

	waitForPaneID(t, mustSocketClient(t, socket), first)
	if !hasSocketRow(t, db, socket, first) {
		t.Fatalf("no row for pane %s before the chmod; rows = %+v", first, listSessions(t, db))
	}

	setMode(t, dir, 0o777)
	waitSkipRecords(t, logs, 1)
	// The skip record is logged by a tidy that ran after the chmod. Every later tidy checks the directory again,
	// so no tidy that passed the check before the chmod can still be listing panes when the pane below is created.
	second := newSocketPane(t, socket)
	time.Sleep(severalTidies)
	assertNoPaneID(t, socket, second)
	if hasSocketRow(t, db, socket, second) {
		t.Fatalf("row for pane %s written while the directory is accessible to others", second)
	}

	setMode(t, dir, 0o700)
	third := newSocketPane(t, socket)
	waitForPaneID(t, mustSocketClient(t, socket), third)
	if !hasSocketRow(t, db, socket, third) {
		t.Fatalf("no row for pane %s after the chmod back to 0700; rows = %+v", third, listSessions(t, db))
	}

	records := skipRecords(t, logs)
	if len(records) != 1 {
		t.Fatalf("%q records = %d, want 1; logs:\n%s", defaultSocketSkipMsg, len(records), logs.String())
	}
	checkSkipRecord(t, records[0], "-S "+socket, "other users have access")
}

// TestDefaultSocketSkipLogsReasonChange: the skip is logged again when its reason changes, and not on each tidy
// in between.
func TestDefaultSocketSkipLogsReasonChange(t *testing.T) {
	base := t.TempDir()
	socket := filepath.Join(socketDirPath(base), "default")
	dir := filepath.Dir(socket)
	makeSocketDir(t, dir, 0o777)
	startSocketServer(t, socket)
	moved := filepath.Join(base, "moved")
	// Cleanup runs last in first-in-last-out order, so this restores the directory before startSocketServer's
	// kill-server connects through the socket path.
	t.Cleanup(func() {
		if err := os.Rename(moved, dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("restore socket directory: %v", err)
		}
	})
	st, _ := openFrameStore(t)
	logs := &lockedBuffer{}
	serveDefaultSocket(t, st, socket, logs)

	waitSkipRecords(t, logs, 1)
	time.Sleep(severalTidies)
	if got := len(skipRecords(t, logs)); got != 1 {
		t.Fatalf("%q records = %d before the reason changed, want 1; logs:\n%s",
			defaultSocketSkipMsg, got, logs.String())
	}

	if err := os.Rename(dir, moved); err != nil {
		t.Fatalf("Rename(%s, %s): %v", dir, moved, err)
	}
	waitSkipRecords(t, logs, 2)
	time.Sleep(severalTidies)
	records := skipRecords(t, logs)
	if len(records) != 2 {
		t.Fatalf("%q records = %d, want 2; logs:\n%s", defaultSocketSkipMsg, len(records), logs.String())
	}
	checkSkipRecord(t, records[0], "-S "+socket, "other users have access")
	checkSkipRecord(t, records[1], "-S "+socket, "directory missing")
}

// mustSocketClient returns a client for socket, failing the test on error.
func mustSocketClient(t *testing.T, socket string) *tmuxctl.Client {
	t.Helper()
	client, err := tmuxctl.New(tmuxctl.Server{Socket: socket})
	if err != nil {
		t.Fatalf("New(%s): %v", socket, err)
	}
	return client
}
