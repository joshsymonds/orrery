package daemon_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// openFrameStore opens a store at a fresh path under t.TempDir and a second connection to the same file for
// reads and for holding the write lock. The store opens with opts after storetest.NoSync.
func openFrameStore(t *testing.T, opts ...store.Option) (*store.Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	st, err := store.Open(t.Context(), path, append([]store.Option{storetest.NoSync()}, opts...)...)
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
	updates := make(chan daemon.PaneUpdate)
	serve(t, daemon.Config{
		Server:       srv.server,
		Mirror:       daemon.NewMirror(st, time.Now, nil),
		Updates:      updates,
		TidyInterval: time.Hour,
	})
	waitForPaneID(t, srv.client, srv.pane)

	unbound := registryPaneUpdate(t, srv, paneregistry.StatusIdle, "", registryPrompt)
	unbound.Unbound = true
	steps := []struct {
		name   string
		update daemon.PaneUpdate
	}{
		{"prompt submitted", registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt)},
		{"permission requested", registryPaneUpdate(t, srv, paneregistry.StatusWaiting,
			paneregistry.WaitingPermission, registryPrompt, registryBash)},
		{"stopped", registryPaneUpdate(t, srv, paneregistry.StatusIdle, "", registryPrompt, registryFinal)},
		{"session ended", unbound},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			applyUpdate(updates, step.update)
			checkPaneRow(t, srv, db)
		})
	}
}

func TestFrameSyncUnchangedFrameUpdatesRow(t *testing.T) {
	srv := startServer(t)
	st, db := openFrameStore(t)
	transitions := make(chan daemon.Transition, 4)
	updates := make(chan daemon.PaneUpdate)
	serve(t, daemon.Config{
		Server:       srv.server,
		Mirror:       daemon.NewMirror(st, time.Now, nil),
		Updates:      updates,
		Observers:    []daemon.Observer{func(transition daemon.Transition) { transitions <- transition }},
		TidyInterval: time.Hour,
	})
	waitForPaneID(t, srv.client, srv.pane)

	first := registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt)
	first.SessionID = "synthetic-session-1"
	first.Transcript = "/synthetic/transcripts/one.jsonl"
	applyUpdate(updates, first)
	checkPaneRow(t, srv, db)
	if got := drain(transitions); len(got) != 1 {
		t.Fatalf("first update observed %d transitions, want 1", len(got))
	}

	next := registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt)
	next.SessionID = "synthetic-session-2"
	next.Transcript = "/synthetic/transcripts/two.jsonl"
	applyUpdate(updates, next)
	wantOption(t, srv, protocol.OptionClaudeSession, "synthetic-session-2")
	wantOption(t, srv, protocol.OptionTranscript, "/synthetic/transcripts/two.jsonl")
	checkPaneRow(t, srv, db)
	if got := drain(transitions); len(got) != 0 {
		t.Fatalf("unchanged update observed %d transitions, want 0: %+v", len(got), got)
	}
}

func TestFrameSyncObserversReceiveEachTransitionOnce(t *testing.T) {
	srv := startServer(t)
	st, _ := openFrameStore(t)
	transitions := make(chan daemon.Transition, 16)
	updates := make(chan daemon.PaneUpdate)
	serve(t, daemon.Config{
		Server:    srv.server,
		Mirror:    daemon.NewMirror(st, time.Now, nil),
		Updates:   updates,
		Observers: []daemon.Observer{func(transition daemon.Transition) { transitions <- transition }},
	})

	unbound := registryPaneUpdate(t, srv, paneregistry.StatusIdle, "", registryPrompt)
	unbound.Unbound = true
	steps := []struct {
		name   string
		update daemon.PaneUpdate
		want   [][2]protocol.State // nil when the update changes no state
	}{
		{"prompt submitted", registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt),
			[][2]protocol.State{{"", protocol.StateWorking}}},
		{"prompt repeated", registryPaneUpdate(t, srv, paneregistry.StatusBusy, "", registryPrompt), nil},
		{"permission requested", registryPaneUpdate(t, srv, paneregistry.StatusWaiting,
			paneregistry.WaitingPermission, registryPrompt, registryBash),
			[][2]protocol.State{{protocol.StateWorking, protocol.StateWaiting}}},
		{"stopped", registryPaneUpdate(t, srv, paneregistry.StatusIdle, "", registryPrompt, registryFinal),
			[][2]protocol.State{{protocol.StateWaiting, protocol.StateDone}}},
		{"session ended", unbound, [][2]protocol.State{{protocol.StateDone, ""}}},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			applyUpdate(updates, step.update)
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
	// A busy timeout of 0 fails the locked sync at once, so the update waits out no timeout.
	st, db := openFrameStore(t, store.WithBusyTimeout(0))
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
	mirror := daemon.NewMirror(st, time.Now, nil)
	updates := make(chan daemon.PaneUpdate)
	serve(t, daemon.Config{
		Server:  srv.server,
		Mirror:  mirror,
		Updates: updates,
		Observers: []daemon.Observer{func(daemon.Transition) {
			observed <- time.Now()
		}},
	})
	// The locked sync is deferred for the drain, which Wait ends, with the lock still held, before the store closes.
	t.Cleanup(mirror.Wait)

	applyUpdate(updates, registryPaneUpdate(t, srv, paneregistry.StatusWaiting, paneregistry.WaitingPermission,
		registryPrompt, registryBash))

	select {
	case <-observed:
	default:
		t.Fatal("observer was not called for the update")
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

// burstPanes is how many panes take an update in the locked-store burst test.
const burstPanes = 8

func TestFrameSyncLockedStoreBurstStillReachesObserversInTime(t *testing.T) {
	srv := startServer(t)
	// A busy timeout of 0 fails each locked sync at once, so the updates wait out no timeout.
	st, db := openFrameStore(t, store.WithBusyTimeout(0))
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
	mirror := daemon.NewMirror(st, time.Now, nil)
	updates := make(chan daemon.PaneUpdate)
	serve(t, daemon.Config{
		Server:  srv.server,
		Mirror:  mirror,
		Updates: updates,
		Observers: []daemon.Observer{func(daemon.Transition) {
			observed <- time.Now()
		}},
	})
	// The locked syncs are deferred for the drain, which Wait ends, with the lock still held, before the store closes.
	t.Cleanup(mirror.Wait)

	for _, pane := range panes {
		update := registryPaneUpdate(t, srv, paneregistry.StatusWaiting, paneregistry.WaitingPermission,
			registryPrompt, registryBash)
		update.PaneID = pane
		applyUpdate(updates, update)
	}
	for range panes {
		select {
		case <-observed:
		default:
			t.Fatal("an observer was not called for its update")
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
