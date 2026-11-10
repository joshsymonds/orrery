package integration_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// exampleSessionID is the Claude Code session id every fixture hook carries.
const exampleSessionID = "example-session"

// storeFixture is one private tmux server with a pane, a fake claude, a fake ntfy topic, and the socket and project
// a hook needs. orreryd started from it keeps its store under stateHome.
type storeFixture struct {
	client     *tmuxctl.Client
	server     string
	pane       string
	socket     string
	project    string
	transcript string
	fakeClaude string
	urlFile    string
	stateHome  string
	dbPath     string
	notices    chan notice
}

// newStoreFixture starts the pane and writes the fake claude, ntfy URL file, project and transcript a daemon needs.
func newStoreFixture(t *testing.T) *storeFixture {
	t.Helper()
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")

	dir := t.TempDir()
	stateHome := t.TempDir()
	f := &storeFixture{
		client:     client,
		server:     name,
		pane:       pane,
		socket:     filepath.Join(dir, "orreryd.sock"),
		project:    filepath.Join(dir, exampleProjectName),
		transcript: filepath.Join(dir, "transcript.jsonl"),
		fakeClaude: filepath.Join(dir, "fake-claude.sh"),
		urlFile:    filepath.Join(dir, "ntfy-url"),
		stateHome:  stateHome,
		dbPath:     filepath.Join(stateHome, "orrery", "orrery.db"),
	}
	if err := os.WriteFile(f.fakeClaude, []byte(fakeClaudeScript), 0o700); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	ntfyURL, notices := fakeNtfy(t)
	f.notices = notices
	if err := os.WriteFile(f.urlFile, []byte(ntfyURL+"\n"), 0o600); err != nil {
		t.Fatalf("write ntfy URL file: %v", err)
	}
	if err := os.Mkdir(f.project, 0o700); err != nil {
		t.Fatalf("make project: %v", err)
	}
	if err := os.WriteFile(f.transcript, []byte(exampleTranscript), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return f
}

// startOrreryd starts orreryd against the fixture's tmux server with XDG_STATE_HOME set to its state home, and waits
// for its socket to listen.
func (f *storeFixture) startOrreryd(t *testing.T) *orreryProcess {
	t.Helper()
	daemon := startBinary(t, "orreryd",
		scrubbedEnv("ORRERY_NTFY_URL_FILE="+f.urlFile, "XDG_STATE_HOME="+f.stateHome),
		"-L", f.server, "--socket", f.socket,
		"--claude", f.fakeClaude, "--summary-model", exampleContextName+"="+exampleModel)
	daemon.waitForListener(t, "unix", f.socket)
	return daemon
}

// hook sends one hook JSON for the fixture's pane.
func (f *storeFixture) hook(t *testing.T, hook map[string]any) {
	t.Helper()
	sendHook(t, f.socket, f.pane, f.project, hook)
}

// userPromptHook is a UserPromptSubmit for the fixture's session.
func (f *storeFixture) userPromptHook() map[string]any {
	return map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"session_id":      exampleSessionID,
		"transcript_path": f.transcript,
	}
}

// permissionHook is a PermissionRequest for the fixture's session.
func (f *storeFixture) permissionHook() map[string]any {
	return map[string]any{
		"hook_event_name": "PermissionRequest",
		"session_id":      exampleSessionID,
		"transcript_path": f.transcript,
		"tool_name":       "example-tool",
		"tool_input":      map[string]any{"command": "example"},
	}
}

// stopHook is a Stop for the fixture's session, carrying the last assistant message.
func (f *storeFixture) stopHook() map[string]any {
	return map[string]any{
		"hook_event_name":        "Stop",
		"session_id":             exampleSessionID,
		"transcript_path":        f.transcript,
		"last_assistant_message": exampleLastMessage,
	}
}

// sessionEndHook is a SessionEnd for the fixture's session.
func (f *storeFixture) sessionEndHook() map[string]any {
	return map[string]any{
		"hook_event_name": "SessionEnd",
		"session_id":      exampleSessionID,
		"transcript_path": f.transcript,
	}
}

// paneRow returns the store row that pane's @orrery_id names, and the row the pane's mirrored options describe.
// ok is false while the pane has no id or the store has no row for it.
func (f *storeFixture) paneRow(t *testing.T, db *sql.DB, pane string) (got, want sqlcgen.Session, ok bool) {
	t.Helper()
	id := readOption(t, f.client, pane, protocol.OptionID)
	if id == "" {
		return sqlcgen.Session{}, sqlcgen.Session{}, false
	}
	got, err := sqlcgen.New(db).GetSession(t.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlcgen.Session{}, sqlcgen.Session{}, false
	}
	if err != nil {
		t.Fatalf("read row %s: %v", id, err)
	}
	want = sqlcgen.Session{
		ID:            id,
		Server:        "-L " + f.server,
		Pane:          pane,
		State:         readOption(t, f.client, pane, protocol.OptionState),
		Changed:       readOption(t, f.client, pane, protocol.OptionChanged),
		Seen:          readOption(t, f.client, pane, protocol.OptionSeen),
		Question:      readOption(t, f.client, pane, protocol.OptionQuestion),
		Summary:       readOption(t, f.client, pane, protocol.OptionSummary),
		Label:         readOption(t, f.client, pane, protocol.OptionLabel),
		Context:       readOption(t, f.client, pane, protocol.OptionContext),
		Tool:          readOption(t, f.client, pane, protocol.OptionTool),
		ClaudeSession: readOption(t, f.client, pane, protocol.OptionClaudeSession),
		Transcript:    readOption(t, f.client, pane, protocol.OptionTranscript),
	}
	return got, want, true
}

// waitForRow polls until the store row for pane equals the pane's mirrored options, failing the test after waitLimit.
// The daemon syncs a pane after its hook is applied, and the hook client returns before that sync, so the row can
// trail the options for a moment.
func (f *storeFixture) waitForRow(t *testing.T, db *sql.DB, pane string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		got, want, ok := f.paneRow(t, db, pane)
		if ok && got == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("pane %s row never matched its options: row %+v (found %t), want %+v", pane, got, ok, want)
		case <-ticker.C:
		}
	}
}

// openStoreForInspection opens the store file at path with its own connection, closed at cleanup.
func openStoreForInspection(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store for inspection: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// sessionField returns the row field that store field name holds, or nil for an unknown name.
func sessionField(row *sqlcgen.Session, name string) *string {
	switch name {
	case "state":
		return &row.State
	case "changed":
		return &row.Changed
	case "seen":
		return &row.Seen
	case "question":
		return &row.Question
	case "summary":
		return &row.Summary
	case "label":
		return &row.Label
	case "context":
		return &row.Context
	case "tool":
		return &row.Tool
	case "claude_session":
		return &row.ClaudeSession
	case "transcript":
		return &row.Transcript
	}
	return nil
}

// replayEvents rebuilds the sessions table from events, applied in seq order. A session_changed event for a session
// that no session_added event created fails the test.
func replayEvents(t *testing.T, events []sqlcgen.Event) map[string]sqlcgen.Session {
	t.Helper()
	rebuilt := map[string]sqlcgen.Session{}
	for _, event := range events {
		switch event.Kind {
		case store.EventSessionAdded:
			var payload struct {
				Server string `json:"server"`
				Pane   string `json:"pane"`
			}
			if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
				t.Fatalf("decode %s payload %q: %v", event.Kind, event.Payload, err)
			}
			rebuilt[event.SessionID] = sqlcgen.Session{ID: event.SessionID, Server: payload.Server, Pane: payload.Pane}
		case store.EventSessionChanged:
			row, ok := rebuilt[event.SessionID]
			if !ok {
				t.Fatalf("%s for session %s before its %s", event.Kind, event.SessionID, store.EventSessionAdded)
			}
			var payload struct {
				Fields map[string]string `json:"fields"`
			}
			if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
				t.Fatalf("decode %s payload %q: %v", event.Kind, event.Payload, err)
			}
			for name, value := range payload.Fields {
				field := sessionField(&row, name)
				if field == nil {
					t.Fatalf("%s names unknown field %q", event.Kind, name)
				}
				*field = value
			}
			rebuilt[event.SessionID] = row
		case store.EventSessionRemoved:
			delete(rebuilt, event.SessionID)
		default:
			t.Fatalf("unknown event kind %q", event.Kind)
		}
	}
	return rebuilt
}

// fileMode returns the permission bits of the file at path.
func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func TestStoreMirrorsHooksIntoRowsAndEvents(t *testing.T) {
	f := newStoreFixture(t)
	daemon := f.startOrreryd(t)

	f.hook(t, f.userPromptHook())
	waitForOption(t, f.client, f.pane, protocol.OptionState, string(protocol.StateWorking))
	waitForOption(t, f.client, f.pane, protocol.OptionSummary, exampleSummary)
	db := openStoreForInspection(t, f.dbPath)

	// The summary is written by the summarizer, not a hook, so the row is checked after the next hook syncs the pane.
	f.hook(t, f.permissionHook())
	waitForOption(t, f.client, f.pane, protocol.OptionState, string(protocol.StateWaiting))
	f.waitForRow(t, db, f.pane)

	f.hook(t, f.stopHook())
	waitForOption(t, f.client, f.pane, protocol.OptionState, string(protocol.StateDone))
	f.waitForRow(t, db, f.pane)

	f.hook(t, f.sessionEndHook())
	waitForOption(t, f.client, f.pane, protocol.OptionState, "")
	f.waitForRow(t, db, f.pane)

	wantExit(t, daemon.stop(t), 0)
	if got := fileMode(t, f.dbPath); got != 0o600 {
		t.Fatalf("store file mode = %o, want 600", got)
	}

	q := sqlcgen.New(db)
	ctx := t.Context()
	sessions, err := q.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("store has %d sessions, want the one pane", len(sessions))
	}
	events, err := q.ListEvents(ctx)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	rebuilt := replayEvents(t, events)
	if len(rebuilt) != len(sessions) {
		t.Fatalf("replayed %d sessions, store has %d; events %+v", len(rebuilt), len(sessions), events)
	}
	for _, row := range sessions {
		if got, ok := rebuilt[row.ID]; !ok || got != row {
			t.Fatalf("replayed session %+v (found %t), store row %+v", got, ok, row)
		}
	}
	if len(events) == 0 {
		t.Fatal("store recorded no events")
	}
}
