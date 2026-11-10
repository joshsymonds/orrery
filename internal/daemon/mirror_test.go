package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// mirrorBudget is the one-second bound a dropped sync must meet.
const mirrorBudget = time.Second

// mirrorNoConfig keeps the user's tmux config away from the test servers.
const mirrorNoConfig = "/dev/null"

// mirrorCommand is the process each test pane runs.
const mirrorCommand = "sleep 300"

// mirrorIDPattern matches a minted id: 128 random bits in lowercase hex.
var mirrorIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// mirrorNow is the clock every mirror in these tests uses.
func mirrorNow() time.Time { return time.Unix(1700000000, 0) }

// mirrorServer is a private tmux server with one session and the pane that session starts.
type mirrorServer struct {
	client *tmuxctl.Client
	server tmuxctl.Server
	pane   string
}

// startMirrorServer starts a private tmux server under a random name and kills it at cleanup.
func startMirrorServer(t *testing.T) mirrorServer {
	t.Helper()
	ms := newMirrorSession(t, tmuxctl.Server{Name: mirrorServerName(t)})
	t.Cleanup(func() {
		if _, err := ms.client.Run(context.Background(), "kill-server"); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
	return ms
}

// newMirrorSession starts a detached session on server and returns its first pane. It registers no cleanup.
func newMirrorSession(t *testing.T, server tmuxctl.Server) mirrorServer {
	t.Helper()
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	if _, err := client.Run(t.Context(), "-f", mirrorNoConfig, "new-session", "-d", "-s", "s1",
		"-c", t.TempDir(), mirrorCommand); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	return mirrorServer{client: client, server: server, pane: strings.Fields(out)[0]}
}

// mirrorServerName returns a unique synthetic tmux server name.
func mirrorServerName(t *testing.T) string {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	return fmt.Sprintf("orrery-mirror-test-%d-%x", os.Getpid(), suffix)
}

// mirrorOption returns the pane-scoped option name on ms's pane; "" when unset.
func mirrorOption(t *testing.T, ms mirrorServer, name string) string {
	t.Helper()
	value, _, err := ms.client.PaneOption(t.Context(), ms.pane, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return value
}

// mirrorValues returns the ten mirrored option values a test pane starts with.
func mirrorValues() map[string]string {
	return map[string]string{
		protocol.OptionState:         "waiting",
		protocol.OptionChanged:       "1700000000",
		protocol.OptionSeen:          "1700000001",
		protocol.OptionQuestion:      "synthetic question",
		protocol.OptionSummary:       "synthetic summary",
		protocol.OptionLabel:         "synthetic label",
		protocol.OptionContext:       "synthetic context",
		protocol.OptionTool:          "Bash",
		protocol.OptionClaudeSession: "synthetic-session",
		protocol.OptionTranscript:    "/synthetic/transcript.jsonl",
	}
}

// setMirrorValues sets every mirrored option on ms's pane.
func setMirrorValues(t *testing.T, ms mirrorServer) {
	t.Helper()
	for name, value := range mirrorValues() {
		if err := ms.client.SetPaneOption(t.Context(), ms.pane, name, value); err != nil {
			t.Fatalf("set %s: %v", name, err)
		}
	}
}

// openMirrorStore opens a store in a fresh temporary directory and returns it with its file path.
func openMirrorStore(t *testing.T) (*store.Store, string) {
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
	return st, path
}

// openMirrorDB opens a second connection to the store file at path, for reading rows and holding the write lock.
func openMirrorDB(t *testing.T, path string) *sql.DB {
	t.Helper()
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
	return db
}

func mirrorRows(t *testing.T, db *sql.DB) []sqlcgen.Session {
	t.Helper()
	rows, err := sqlcgen.New(db).ListSessions(t.Context())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	return rows
}

func mirrorEvents(t *testing.T, db *sql.DB) []sqlcgen.Event {
	t.Helper()
	events, err := sqlcgen.New(db).ListEvents(t.Context())
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	return events
}

// wantMirrorRow is the row a sync of a pane holding mirrorValues must produce.
func wantMirrorRow(id, server, pane string) sqlcgen.Session {
	return sqlcgen.Session{
		ID:            id,
		Server:        server,
		Pane:          pane,
		State:         mirrorValues()[protocol.OptionState],
		Changed:       mirrorValues()[protocol.OptionChanged],
		Seen:          mirrorValues()[protocol.OptionSeen],
		Question:      mirrorValues()[protocol.OptionQuestion],
		Summary:       mirrorValues()[protocol.OptionSummary],
		Label:         mirrorValues()[protocol.OptionLabel],
		Context:       mirrorValues()[protocol.OptionContext],
		Tool:          mirrorValues()[protocol.OptionTool],
		ClaudeSession: mirrorValues()[protocol.OptionClaudeSession],
		Transcript:    mirrorValues()[protocol.OptionTranscript],
	}
}

func TestMirrorMintsIDAndMirrorsOptions(t *testing.T) {
	ms := startMirrorServer(t)
	setMirrorValues(t, ms)
	st, path := openMirrorStore(t)
	db := openMirrorDB(t, path)
	m := NewMirror(st, mirrorNow, nil)

	m.SyncPane(t.Context(), ms.server, ms.pane)

	id := mirrorOption(t, ms, protocol.OptionID)
	if !mirrorIDPattern.MatchString(id) {
		t.Fatalf("%s = %q, want 32 lowercase hex digits", protocol.OptionID, id)
	}
	server := "-L " + ms.server.Name
	rows := mirrorRows(t, db)
	if len(rows) != 1 || rows[0] != wantMirrorRow(id, server, ms.pane) {
		t.Fatalf("rows after first sync = %+v, want one row %+v", rows, wantMirrorRow(id, server, ms.pane))
	}

	events := mirrorEvents(t, db)
	if len(events) != 2 {
		t.Fatalf("events after first sync = %+v, want session_added and session_changed", events)
	}
	if events[0].Kind != store.EventSessionAdded || events[0].SessionID != id {
		t.Fatalf("event 0 = %+v, want %s for %s", events[0], store.EventSessionAdded, id)
	}
	var added map[string]string
	if err := json.Unmarshal([]byte(events[0].Payload), &added); err != nil {
		t.Fatalf("decode session_added payload: %v", err)
	}
	if added["server"] != server || added["pane"] != ms.pane || len(added) != 2 {
		t.Fatalf("session_added payload = %s, want server %q and pane %q", events[0].Payload, server, ms.pane)
	}
	if events[1].Kind != store.EventSessionChanged || events[1].SessionID != id {
		t.Fatalf("event 1 = %+v, want %s for %s", events[1], store.EventSessionChanged, id)
	}
	var changed struct {
		Fields map[string]string `json:"fields"`
	}
	if err := json.Unmarshal([]byte(events[1].Payload), &changed); err != nil {
		t.Fatalf("decode session_changed payload: %v", err)
	}
	if len(changed.Fields) != len(mirrorValues()) {
		t.Fatalf("session_changed fields = %v, want all ten mirrored fields", changed.Fields)
	}

	m.SyncPane(t.Context(), ms.server, ms.pane)

	if got := mirrorOption(t, ms, protocol.OptionID); got != id {
		t.Fatalf("%s after second sync = %q, want kept %q", protocol.OptionID, got, id)
	}
	if rows := mirrorRows(t, db); len(rows) != 1 || rows[0] != wantMirrorRow(id, server, ms.pane) {
		t.Fatalf("rows after second sync = %+v", rows)
	}
	if again := mirrorEvents(t, db); len(again) != len(events) {
		t.Fatalf("second sync appended events: before %d, after %d", len(events), len(again))
	}
}

func TestMirrorReusedPaneGetsNewRow(t *testing.T) {
	ms := startMirrorServer(t)
	setMirrorValues(t, ms)
	st, path := openMirrorStore(t)
	db := openMirrorDB(t, path)
	m := NewMirror(st, mirrorNow, nil)
	m.SyncPane(t.Context(), ms.server, ms.pane)
	firstID := mirrorOption(t, ms, protocol.OptionID)

	if _, err := ms.client.Run(t.Context(), "kill-server"); err != nil {
		t.Fatalf("kill-server: %v", err)
	}
	reused := newMirrorSession(t, ms.server)
	if reused.pane != ms.pane {
		t.Fatalf("pane id after restart = %s, want reused %s", reused.pane, ms.pane)
	}

	m.SyncPane(t.Context(), reused.server, reused.pane)

	secondID := mirrorOption(t, reused, protocol.OptionID)
	if !mirrorIDPattern.MatchString(secondID) || secondID == firstID {
		t.Fatalf("%s on reused pane = %q, want a new id (old %q)", protocol.OptionID, secondID, firstID)
	}
	var found bool
	for _, row := range mirrorRows(t, db) {
		if row.ID == secondID {
			found = row.Pane == reused.pane && row.Server == "-L "+reused.server.Name
		}
	}
	if !found {
		t.Fatalf("no row %s for reused pane %s", secondID, reused.pane)
	}
}

func TestMirrorDropsSyncWhenLockWaitExceedsBudget(t *testing.T) {
	ms := startMirrorServer(t)
	setMirrorValues(t, ms)
	st, path := openMirrorStore(t)
	db := openMirrorDB(t, path)
	logs := &bytes.Buffer{}
	m := NewMirror(st, mirrorNow, slog.New(slog.NewTextHandler(logs, nil)))

	m.lock <- struct{}{}
	start := time.Now()
	m.SyncPane(t.Context(), ms.server, ms.pane)
	elapsed := time.Since(start)
	<-m.lock

	if elapsed > mirrorBudget {
		t.Fatalf("SyncPane took %v with the lock held, want at most %v", elapsed, mirrorBudget)
	}
	if logs.Len() == 0 {
		t.Fatal("dropped sync was not logged")
	}
	if id := mirrorOption(t, ms, protocol.OptionID); id != "" {
		t.Fatalf("%s = %q with the lock held, want unset", protocol.OptionID, id)
	}
	if rows := mirrorRows(t, db); len(rows) != 0 {
		t.Fatalf("rows with the lock held = %+v, want none", rows)
	}
	if events := mirrorEvents(t, db); len(events) != 0 {
		t.Fatalf("events with the lock held = %+v, want none", events)
	}

	m.SyncPane(t.Context(), ms.server, ms.pane)
	if rows := mirrorRows(t, db); len(rows) != 1 {
		t.Fatalf("rows after the lock was released = %+v, want one", rows)
	}
}

func TestMirrorDropsSyncWhenStoreWriteLocked(t *testing.T) {
	ms := startMirrorServer(t)
	setMirrorValues(t, ms)
	st, path := openMirrorStore(t)
	db := openMirrorDB(t, path)
	ctx := t.Context()

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold store write lock: %v", err)
	}
	logs := &bytes.Buffer{}
	m := NewMirror(st, mirrorNow, slog.New(slog.NewTextHandler(logs, nil)))

	start := time.Now()
	m.SyncPane(ctx, ms.server, ms.pane)
	elapsed := time.Since(start)

	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("release store write lock: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close lock connection: %v", err)
	}

	if elapsed > mirrorBudget {
		t.Fatalf("SyncPane took %v with the store write-locked, want at most %v", elapsed, mirrorBudget)
	}
	if logs.Len() == 0 {
		t.Fatal("dropped sync was not logged")
	}
	if id := mirrorOption(t, ms, protocol.OptionID); id != "" {
		t.Fatalf("%s = %q after a failed store write, want unset", protocol.OptionID, id)
	}
	if got := mirrorOption(t, ms, protocol.OptionState); got != mirrorValues()[protocol.OptionState] {
		t.Fatalf("%s = %q after a failed store write, want unchanged %q", protocol.OptionState, got,
			mirrorValues()[protocol.OptionState])
	}
	if rows := mirrorRows(t, db); len(rows) != 0 {
		t.Fatalf("rows after a failed store write = %+v, want none", rows)
	}

	m.SyncPane(ctx, ms.server, ms.pane)
	if rows := mirrorRows(t, db); len(rows) != 1 {
		t.Fatalf("rows after the write lock was released = %+v, want one", rows)
	}
}
