package daemon_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// lockedStoreBudget bounds how long a frame may take to reach its observer while the store is write-locked.
const lockedStoreBudget = time.Second

// openFrameStore opens a store at a fresh path under t.TempDir and a second connection to the same file for
// reads and for holding the write lock.
func openFrameStore(t *testing.T) (*store.Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	st, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store Close: %v", err)
		}
	})
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_busy_timeout=200&_journal_mode=WAL"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db Close: %v", err)
		}
	})
	return st, db
}

// rowMap returns every column of row under its store name, so rows, replayed rows and tmux options compare
// with maps.Equal.
func rowMap(row sqlcgen.Session) map[string]string {
	return map[string]string{
		"id":             row.ID,
		"server":         row.Server,
		"pane":           row.Pane,
		"state":          row.State,
		"changed":        row.Changed,
		"seen":           row.Seen,
		"question":       row.Question,
		"summary":        row.Summary,
		"label":          row.Label,
		"context":        row.Context,
		"tool":           row.Tool,
		"claude_session": row.ClaudeSession,
		"transcript":     row.Transcript,
	}
}

// tmuxRow returns the pane's row as its tmux options describe it. An unset option reads as "", which is the
// store's value for unset.
func tmuxRow(t *testing.T, srv testServer) map[string]string {
	t.Helper()
	id, ok := option(t, srv, protocol.OptionID)
	if !ok {
		t.Fatalf("%s unset on pane %s", protocol.OptionID, srv.pane)
	}
	// rowFields maps each store field name to the tmux option it mirrors.
	rowFields := []struct{ name, option string }{
		{"state", protocol.OptionState},
		{"changed", protocol.OptionChanged},
		{"seen", protocol.OptionSeen},
		{"question", protocol.OptionQuestion},
		{"summary", protocol.OptionSummary},
		{"label", protocol.OptionLabel},
		{"context", protocol.OptionContext},
		{"tool", protocol.OptionTool},
		{"claude_session", protocol.OptionClaudeSession},
		{"transcript", protocol.OptionTranscript},
	}
	row := map[string]string{"id": id, "server": "-L " + srv.server.Name, "pane": srv.pane}
	for _, field := range rowFields {
		value, _ := option(t, srv, field.option)
		row[field.name] = value
	}
	return row
}

// replayEvents rebuilds every session from the events table, applied in seq order, and returns the rows
// keyed by session id.
func replayEvents(t *testing.T, db *sql.DB) map[string]map[string]string {
	t.Helper()
	events, err := sqlcgen.New(db).ListEvents(t.Context())
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	rows := map[string]map[string]string{}
	for _, event := range events {
		switch event.Kind {
		case store.EventSessionAdded:
			var payload struct {
				Server string `json:"server"`
				Pane   string `json:"pane"`
			}
			if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
				t.Fatalf("decode %s payload: %v", event.Kind, err)
			}
			row := rowMap(sqlcgen.Session{})
			row["id"] = event.SessionID
			row["server"] = payload.Server
			row["pane"] = payload.Pane
			rows[event.SessionID] = row
		case store.EventSessionChanged:
			var payload struct {
				Fields map[string]string `json:"fields"`
			}
			if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
				t.Fatalf("decode %s payload: %v", event.Kind, err)
			}
			row, ok := rows[event.SessionID]
			if !ok {
				t.Fatalf("%s for session %s before its session_added", event.Kind, event.SessionID)
			}
			for name, value := range payload.Fields {
				row[name] = value
			}
		case store.EventSessionRemoved:
			delete(rows, event.SessionID)
		default:
			t.Fatalf("event %d has unknown kind %q", event.Seq, event.Kind)
		}
	}
	return rows
}

// checkPaneRow asserts that the pane's row equals its tmux options, and that replaying the event log in seq
// order gives the same row.
func checkPaneRow(t *testing.T, srv testServer, db *sql.DB) {
	t.Helper()
	want := tmuxRow(t, srv)
	id := want["id"]
	row, err := sqlcgen.New(db).GetSession(t.Context(), id)
	if err != nil {
		t.Fatalf("GetSession(%s): %v", id, err)
	}
	if got := rowMap(row); !maps.Equal(got, want) {
		t.Fatalf("row = %v, want tmux options %v", got, want)
	}
	replayed, ok := replayEvents(t, db)[id]
	if !ok {
		t.Fatalf("replaying events gives no session %s", id)
	}
	if !maps.Equal(replayed, want) {
		t.Fatalf("replayed row = %v, want %v", replayed, want)
	}
}

func TestFrameSyncRowMatchesTmuxAndReplay(t *testing.T) {
	srv := startServer(t)
	st, db := openFrameStore(t)
	socket := serve(t, daemon.Config{
		Server: srv.server,
		Mirror: daemon.NewMirror(st, time.Now, nil),
	})

	steps := []struct {
		name string
		hook string
	}{
		{"prompt submitted", userPromptSubmitHook},
		{"permission requested", permissionHook},
		{"stopped", stopHook},
		{"session ended", sessionEndHook},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			sendFrame(t, socket, frameFor(step.hook, srv.pane, srv.socket))
			checkPaneRow(t, srv, db)
		})
	}
}

func TestFrameSyncUnchangedFrameUpdatesRow(t *testing.T) {
	const (
		firstHook = `{"hook_event_name":"UserPromptSubmit","prompt":"synthetic prompt",` +
			`"session_id":"synthetic-session-1","transcript_path":"/synthetic/transcripts/one.jsonl"}`
		nextHook = `{"hook_event_name":"UserPromptSubmit","prompt":"synthetic prompt",` +
			`"session_id":"synthetic-session-2","transcript_path":"/synthetic/transcripts/two.jsonl"}`
	)
	srv := startServer(t)
	st, db := openFrameStore(t)
	transitions := make(chan daemon.Transition, 4)
	socket := serve(t, daemon.Config{
		Server:    srv.server,
		Mirror:    daemon.NewMirror(st, time.Now, nil),
		Observers: []daemon.Observer{func(transition daemon.Transition) { transitions <- transition }},
	})

	sendFrame(t, socket, frameFor(firstHook, srv.pane, srv.socket))
	checkPaneRow(t, srv, db)
	if got := drain(transitions); len(got) != 1 {
		t.Fatalf("first frame observed %d transitions, want 1", len(got))
	}

	sendFrame(t, socket, frameFor(nextHook, srv.pane, srv.socket))
	wantOption(t, srv, protocol.OptionClaudeSession, "synthetic-session-2")
	wantOption(t, srv, protocol.OptionTranscript, "/synthetic/transcripts/two.jsonl")
	checkPaneRow(t, srv, db)
	if got := drain(transitions); len(got) != 0 {
		t.Fatalf("unchanged frame observed %d transitions, want 0: %+v", len(got), got)
	}
}

func TestFrameSyncObserversReceiveEachTransitionOnce(t *testing.T) {
	srv := startServer(t)
	st, _ := openFrameStore(t)
	transitions := make(chan daemon.Transition, 16)
	socket := serve(t, daemon.Config{
		Server:    srv.server,
		Mirror:    daemon.NewMirror(st, time.Now, nil),
		Observers: []daemon.Observer{func(transition daemon.Transition) { transitions <- transition }},
	})

	steps := []struct {
		name string
		hook string
		want [][2]protocol.State // nil when the frame changes no state
	}{
		{"prompt submitted", userPromptSubmitHook, [][2]protocol.State{{"", protocol.StateWorking}}},
		{"prompt repeated", userPromptSubmitHook, nil},
		{"permission requested", permissionHook, [][2]protocol.State{{protocol.StateWorking, protocol.StateWaiting}}},
		{"stopped", stopHook, [][2]protocol.State{{protocol.StateWaiting, protocol.StateDone}}},
		{"session ended", sessionEndHook, [][2]protocol.State{{protocol.StateDone, ""}}},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			sendFrame(t, socket, frameFor(step.hook, srv.pane, srv.socket))
			observed := drain(transitions)
			got := make([][2]protocol.State, 0, len(observed))
			for _, transition := range observed {
				if transition.PaneID != srv.pane {
					t.Fatalf("transition pane = %s, want %s", transition.PaneID, srv.pane)
				}
				got = append(got, [2]protocol.State{transition.From, transition.To})
			}
			if !slices.Equal(got, step.want) {
				t.Fatalf("transitions = %v, want %v", got, step.want)
			}
		})
	}
}

func TestFrameSyncStoreWriteLockedStillReachesTmuxAndObserver(t *testing.T) {
	srv := startServer(t)
	st, db := openFrameStore(t)
	ctx := context.Background()

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold store write lock: %v", err)
	}
	t.Cleanup(func() {
		if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
			t.Errorf("release store write lock: %v", err)
		}
		if err := conn.Close(); err != nil {
			t.Errorf("close lock connection: %v", err)
		}
	})

	observed := make(chan time.Time, 1)
	socket := serve(t, daemon.Config{
		Server: srv.server,
		Mirror: daemon.NewMirror(st, time.Now, nil),
		Observers: []daemon.Observer{func(daemon.Transition) {
			observed <- time.Now()
		}},
	})

	sent := time.Now()
	sendFrame(t, socket, frameFor(permissionHook, srv.pane, srv.socket))

	select {
	case at := <-observed:
		if elapsed := at.Sub(sent); elapsed > lockedStoreBudget {
			t.Fatalf("observer reached %v after the frame was sent, want at most %v", elapsed, lockedStoreBudget)
		}
	default:
		t.Fatal("observer was not called for the frame")
	}
	wantOption(t, srv, protocol.OptionState, "waiting")

	rows, err := sqlcgen.New(db).ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows while the store is write-locked = %+v, want none", rows)
	}
}

// burstPanes is how many panes send a frame at once in the locked-store burst test. Frames that waited on each
// other's store writes would put the last observer past lockedStoreBudget.
const burstPanes = 8

// sendFrameErr sends frame to the daemon socket and waits for the daemon to close the connection, returning any
// failure instead of failing the test, so it can run on its own goroutine.
func sendFrameErr(socketPath string, frame protocol.HookFrame) error {
	ctx, cancel := context.WithTimeout(context.Background(), ioTimeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("dial %s: %w", socketPath, err)
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(ioTimeout)); err != nil {
		return fmt.Errorf("set deadline: %w", err)
	}
	if err = protocol.WriteFrame(conn, frame); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	if _, err = io.Copy(io.Discard, conn); err != nil && !errors.Is(err, syscall.ECONNRESET) {
		return fmt.Errorf("wait for close: %w", err)
	}
	return nil
}

func TestFrameSyncLockedStoreBurstStillReachesObserversInTime(t *testing.T) {
	srv := startServer(t)
	st, db := openFrameStore(t)
	ctx := context.Background()

	panes := []string{srv.pane}
	for len(panes) < burstPanes {
		out, err := srv.client.Run(t.Context(), "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "s1", testCommand)
		if err != nil {
			t.Fatalf("new-window: %v", err)
		}
		panes = append(panes, strings.TrimSpace(out))
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold store write lock: %v", err)
	}
	t.Cleanup(func() {
		if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
			t.Errorf("release store write lock: %v", err)
		}
		if err := conn.Close(); err != nil {
			t.Errorf("close lock connection: %v", err)
		}
	})

	observed := make(chan time.Time, burstPanes)
	socket := serve(t, daemon.Config{
		Server: srv.server,
		Mirror: daemon.NewMirror(st, time.Now, nil),
		Observers: []daemon.Observer{func(daemon.Transition) {
			observed <- time.Now()
		}},
	})

	sent := time.Now()
	errs := make(chan error, burstPanes)
	for _, pane := range panes {
		go func() { errs <- sendFrameErr(socket, frameFor(permissionHook, pane, srv.socket)) }()
	}
	for range panes {
		if err := <-errs; err != nil {
			t.Fatalf("send frame: %v", err)
		}
	}
	for range panes {
		select {
		case at := <-observed:
			if elapsed := at.Sub(sent); elapsed > lockedStoreBudget {
				t.Fatalf("an observer was reached %v after the burst was sent, want at most %v", elapsed,
					lockedStoreBudget)
			}
		default:
			t.Fatal("an observer was not called for its frame")
		}
	}
	for _, pane := range panes {
		got, _, err := srv.client.PaneOption(t.Context(), pane, protocol.OptionState)
		if err != nil {
			t.Fatalf("read %s state: %v", pane, err)
		}
		if got != string(protocol.StateWaiting) {
			t.Fatalf("pane %s state = %q, want %q", pane, got, protocol.StateWaiting)
		}
	}
}
