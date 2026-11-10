package daemon_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Log messages that Mirror.Tidy writes about a server's list-panes transitions.
const (
	tidyUnreachableMsg = "tmux server unreachable"
	tidyReachableMsg   = "tmux server reachable again"
	tidyDroppedMsg     = "mirror tidy dropped"
)

// tidyLogServer returns a tmux server on a socket under a fresh temp directory, and the directory. The server is
// not started. Cleanup kills any server left on the socket; the directory is removed after that.
func tidyLogServer(t *testing.T) (tmuxctl.Server, string) {
	t.Helper()
	dir := t.TempDir()
	server := tmuxctl.Server{Socket: filepath.Join(dir, "tmux.sock")}
	t.Cleanup(func() {
		client, err := tmuxctl.New(server)
		if err != nil {
			t.Errorf("New(%+v): %v", server, err)
			return
		}
		_, err = client.Run(context.Background(), "kill-server")
		if err != nil && !strings.Contains(err.Error(), "no server running") &&
			!strings.Contains(err.Error(), "error connecting to") {
			t.Errorf("kill-server: %v", err)
		}
	})
	return server, dir
}

// tidyLogRecords decodes each JSON log line in logs.
func tidyLogRecords(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for line := range strings.SplitSeq(strings.TrimSuffix(logs.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

// tidyLogMessages returns the records whose msg is msg.
func tidyLogMessages(t *testing.T, logs *bytes.Buffer, msg string) []map[string]any {
	t.Helper()
	var matched []map[string]any
	for _, record := range tidyLogRecords(t, logs) {
		if record["msg"] == msg {
			matched = append(matched, record)
		}
	}
	return matched
}

func TestTidyLogsUnreachableOnce(t *testing.T) {
	server, _ := tidyLogServer(t)
	st, db := openFrameStore(t)
	ctx := t.Context()
	serverColumn := "-S " + server.Socket

	// A row under the unreachable server stays as it is: a failed tidy must not replace the server's rows.
	if err := st.SyncPane(ctx, serverColumn, store.Pane{
		ID: "tidy-log-seed", Pane: "%7", Fields: store.Fields{State: "working"},
	}, tidyClock()); err != nil {
		t.Fatalf("SyncPane seed: %v", err)
	}
	rowsBefore := listSessions(t, db)
	eventsBefore := listEvents(t, db)
	if len(rowsBefore) != 1 || len(eventsBefore) == 0 {
		t.Fatalf("setup rows = %d, events = %d; want one row and some events", len(rowsBefore), len(eventsBefore))
	}

	logs := &bytes.Buffer{}
	m := daemon.NewMirror(st, tidyClock, slog.New(slog.NewJSONHandler(logs, nil)))
	for range 3 {
		m.Tidy(ctx, server)
	}

	unreachable := tidyLogMessages(t, logs, tidyUnreachableMsg)
	if len(unreachable) != 1 {
		t.Fatalf("%q records = %d, want 1; logs:\n%s", tidyUnreachableMsg, len(unreachable), logs)
	}
	if level := unreachable[0]["level"]; level != "WARN" {
		t.Fatalf("%q level = %v, want WARN", tidyUnreachableMsg, level)
	}
	if got := unreachable[0]["server"]; got != serverColumn {
		t.Fatalf("%q server = %v, want %q", tidyUnreachableMsg, got, serverColumn)
	}
	if _, ok := unreachable[0]["error"]; !ok {
		t.Fatalf("%q record has no error attribute: %v", tidyUnreachableMsg, unreachable[0])
	}
	if dropped := tidyLogMessages(t, logs, tidyDroppedMsg); len(dropped) != 0 {
		t.Fatalf("%q records = %v, want none", tidyDroppedMsg, dropped)
	}

	if got := listSessions(t, db); !reflect.DeepEqual(got, rowsBefore) {
		t.Fatalf("rows after failing tidies = %+v, want unchanged %+v", got, rowsBefore)
	}
	if got := listEvents(t, db); !reflect.DeepEqual(got, eventsBefore) {
		t.Fatalf("events after failing tidies = %+v, want unchanged %+v", got, eventsBefore)
	}
}

func TestTidyLogsReachableAgainOnce(t *testing.T) {
	server, dir := tidyLogServer(t)
	st, db := openFrameStore(t)
	ctx := t.Context()
	serverColumn := "-S " + server.Socket
	logs := &bytes.Buffer{}
	m := daemon.NewMirror(st, tidyClock, slog.New(slog.NewJSONHandler(logs, nil)))

	m.Tidy(ctx, server)
	if got := len(tidyLogMessages(t, logs, tidyUnreachableMsg)); got != 1 {
		t.Fatalf("%q records before the server started = %d, want 1", tidyUnreachableMsg, got)
	}

	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	if _, err := client.Run(ctx, "-f", noConfig, "new-session", "-d", "-s", "s1",
		"-c", dir, testCommand); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	out, err := client.Run(ctx, "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	pane := strings.TrimSpace(out)

	m.Tidy(ctx, server)
	reachable := tidyLogMessages(t, logs, tidyReachableMsg)
	if len(reachable) != 1 {
		t.Fatalf("%q records = %d, want 1; logs:\n%s", tidyReachableMsg, len(reachable), logs)
	}
	if level := reachable[0]["level"]; level != "INFO" {
		t.Fatalf("%q level = %v, want INFO", tidyReachableMsg, level)
	}
	if got := reachable[0]["server"]; got != serverColumn {
		t.Fatalf("%q server = %v, want %q", tidyReachableMsg, got, serverColumn)
	}
	found := false
	for _, row := range listSessions(t, db) {
		if row.Server == serverColumn && row.Pane == pane {
			found = true
		}
	}
	if !found {
		t.Fatalf("no row for pane %s on %q after recovery; rows = %+v", pane, serverColumn, listSessions(t, db))
	}

	before := len(tidyLogRecords(t, logs))
	m.Tidy(ctx, server)
	records := tidyLogRecords(t, logs)
	if len(records) != before {
		t.Fatalf("tidy on a reachable server logged %d more records, want none; new: %v",
			len(records)-before, records[before:])
	}
	transitions := len(tidyLogMessages(t, logs, tidyUnreachableMsg)) + len(tidyLogMessages(t, logs, tidyReachableMsg))
	if transitions != 2 {
		t.Fatalf("transition records = %d, want 2", transitions)
	}
}
