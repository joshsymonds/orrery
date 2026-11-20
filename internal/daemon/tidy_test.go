package daemon_test

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	"golang.org/x/sys/unix"

	"github.com/joshsymonds/orrery/internal/daemon"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// tidyLocalServer is the store's server column for a tidy test server started by startTidyServer.
func tidyLocalServer(srv testServer) string {
	return "-L " + srv.server.Name
}

// tidyClock is the fixed clock every mirror in the tidy tests reads.
func tidyClock() time.Time { return time.Unix(1700000000, 0) }

// startTidyServer starts a private tmux server with one session whose pane runs testCommand. Cleanup kills it
// unless the test already has, since a test may stop the server itself.
func startTidyServer(t *testing.T) testServer {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	server := tmuxctl.Server{Name: fmt.Sprintf("orrery-tidy-test-%d-%x", os.Getpid(), suffix)}
	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	t.Cleanup(func() {
		err := tmuxtest.Stop(t, server)
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
	path, err := client.Run(t.Context(), "display", "-p", "#{socket_path}")
	if err != nil {
		t.Fatalf("display socket_path: %v", err)
	}
	socket := strings.TrimSpace(path)
	return testServer{
		client: client,
		server: server,
		socket: socket,
		pane:   strings.Fields(out)[0],
	}
}

// newPane adds a window running testCommand to srv's session and returns its pane id.
func newPane(t *testing.T, srv testServer) string {
	t.Helper()
	out, err := srv.client.Run(t.Context(), "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "s1", testCommand)
	if err != nil {
		t.Fatalf("new-window: %v", err)
	}
	return strings.TrimSpace(out)
}

// waitForPaneID waits until pane's @orrery_id is set and returns it. An after-set-option hook appends to a file in the
// test's temp dir each time a server option is set, and inotify reports each append, so the wait blocks on the kernel
// rather than on a tmux wait channel, which holds only one pending signal. The file is watched before the first check,
// so a set that lands between a check and the wait leaves an event queued and is not lost. The wait checks the option
// first and again after each event. It has no deadline of its own: the test binary's timeout ends a wait that never ends.
func waitForPaneID(t *testing.T, client *tmuxctl.Client, pane string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "option-set")
	// The hook's command names path between single quotes for the shell, so path must hold no character the shell or
	// tmux would read.
	if strings.ContainsFunc(path, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("/._-", r)
	}) {
		t.Fatalf("temp file %q names a character the after-set-option hook cannot quote", path)
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		t.Fatalf("inotify_init1: %v", err)
	}
	defer func() {
		if err := unix.Close(fd); err != nil {
			t.Errorf("close inotify for %s: %v", path, err)
		}
	}()
	if _, err := unix.InotifyAddWatch(fd, path, unix.IN_MODIFY); err != nil {
		t.Fatalf("inotify_add_watch %s: %v", path, err)
	}
	if _, err := client.Run(t.Context(), "set-hook", "-g", "after-set-option",
		"run-shell -b 'printf . >> "+path+"'"); err != nil {
		t.Fatalf("set-hook after-set-option: %v", err)
	}
	for {
		value, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionID)
		if err != nil {
			t.Fatalf("PaneOption(%s, %s): %v", pane, protocol.OptionID, err)
		}
		if ok {
			return value
		}
		awaitInotifyEvent(t, fd, path)
	}
}

// awaitInotifyEvent blocks until the inotify descriptor fd reports an event for path, and reads the events it has
// queued, so the next wait sees only events that come after this one.
func awaitInotifyEvent(t *testing.T, fd int, path string) {
	t.Helper()
	var events [4096]byte
	for {
		_, err := unix.Read(fd, events[:])
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			t.Fatalf("read inotify for %s: %v", path, err)
		}
		return
	}
}

// mustSession returns the row with id, failing the test when it is absent.
func mustSession(t *testing.T, db *sql.DB, id string) sqlcgen.Session {
	t.Helper()
	row, err := sqlcgen.New(db).GetSession(t.Context(), id)
	if err != nil {
		t.Fatalf("GetSession(%s): %v", id, err)
	}
	return row
}

// listSessions returns every row in the store.
func listSessions(t *testing.T, db *sql.DB) []sqlcgen.Session {
	t.Helper()
	rows, err := sqlcgen.New(db).ListSessions(t.Context())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	return rows
}

// listEvents returns every event in the store, in seq order.
func listEvents(t *testing.T, db *sql.DB) []sqlcgen.Event {
	t.Helper()
	events, err := sqlcgen.New(db).ListEvents(t.Context())
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	return events
}

// kindsBySession returns each session's event kinds in seq order.
func kindsBySession(events []sqlcgen.Event) map[string][]string {
	kinds := map[string][]string{}
	for _, event := range events {
		kinds[event.SessionID] = append(kinds[event.SessionID], event.Kind)
	}
	return kinds
}

func TestTidyRemovesKilledPaneAddsNewPaneAndCopiesOptions(t *testing.T) {
	srv := startTidyServer(t)
	st, db := openFrameStore(t)
	ctx := t.Context()
	m := daemon.NewMirror(st, tidyClock, nil)

	m.Tidy(ctx, srv.server)
	firstID := waitForPaneID(t, srv.client, srv.pane)

	second := newPane(t, srv)
	if _, err := srv.client.Run(ctx, "kill-pane", "-t", srv.pane); err != nil {
		t.Fatalf("kill-pane: %v", err)
	}
	m.Tidy(ctx, srv.server)
	secondID := waitForPaneID(t, srv.client, second)

	if row, err := sqlcgen.New(db).GetSession(ctx, firstID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("killed pane's row = %+v, %v; want no row", row, err)
	}
	row := mustSession(t, db, secondID)
	if row.Pane != second || row.Server != tidyLocalServer(srv) {
		t.Fatalf("new pane's row = server %q pane %q, want server %q pane %q",
			row.Server, row.Pane, tidyLocalServer(srv), second)
	}

	if err := srv.client.SetPaneOption(ctx, second, protocol.OptionState, "done"); err != nil {
		t.Fatalf("set %s: %v", protocol.OptionState, err)
	}
	m.Tidy(ctx, srv.server)
	if got := mustSession(t, db, secondID).State; got != "done" {
		t.Fatalf("row state = %q after tidy, want %q", got, "done")
	}

	events := listEvents(t, db)
	wantKinds := map[string][]string{
		firstID:  {store.EventSessionAdded, store.EventSessionRemoved},
		secondID: {store.EventSessionAdded, store.EventSessionChanged},
	}
	if got := kindsBySession(events); !reflect.DeepEqual(got, wantKinds) {
		t.Fatalf("event kinds = %v, want %v", got, wantKinds)
	}
	last := events[len(events)-1]
	if last.SessionID != secondID || last.Kind != store.EventSessionChanged ||
		last.Payload != `{"fields":{"state":"done"}}` {
		t.Fatalf("last event = %+v, want session_changed for %s with state done", last, secondID)
	}
}

func TestTidyPrunesEventsOlderThanRetention(t *testing.T) {
	srv := startTidyServer(t)
	st, db := openFrameStore(t)
	ctx := t.Context()
	m := daemon.NewMirror(st, tidyClock, nil)

	// Rows of another server are untouched by the tidy, so their events survive only as pruning allows.
	const other = "-L tidy-other"
	now := tidyClock()
	old := now.Add(-store.EventRetention - time.Hour)
	recent := now.Add(-store.EventRetention + time.Hour)
	if err := st.SyncPane(ctx, other, store.Pane{
		ID: "tidy-old", Pane: "%901", Fields: store.Fields{State: "working"},
	}, old); err != nil {
		t.Fatalf("SyncPane old: %v", err)
	}
	if err := st.SyncPane(ctx, other, store.Pane{
		ID: "tidy-recent", Pane: "%902", Fields: store.Fields{State: "working"},
	}, recent); err != nil {
		t.Fatalf("SyncPane recent: %v", err)
	}

	m.Tidy(ctx, srv.server)

	cutoff := now.Add(-store.EventRetention).Unix()
	for _, event := range listEvents(t, db) {
		if event.At < cutoff {
			t.Fatalf("event %+v is older than the retention cutoff %d", event, cutoff)
		}
	}
	wantRecent := []string{store.EventSessionAdded, store.EventSessionChanged}
	got := kindsBySession(listEvents(t, db))
	if !reflect.DeepEqual(got["tidy-recent"], wantRecent) {
		t.Fatalf("recent session events = %v, want %v", got["tidy-recent"], wantRecent)
	}
	if _, ok := got["tidy-old"]; ok {
		t.Fatalf("old session events = %v, want pruned", got["tidy-old"])
	}
}

func TestTidyAfterKillServerChangesNoRowOrEvent(t *testing.T) {
	srv := startTidyServer(t)
	st, db := openFrameStore(t)
	ctx := t.Context()
	m := daemon.NewMirror(st, tidyClock, nil)

	m.Tidy(ctx, srv.server)
	rowsBefore := listSessions(t, db)
	eventsBefore := listEvents(t, db)
	if len(rowsBefore) != 1 || len(eventsBefore) == 0 {
		t.Fatalf("setup rows = %d, events = %d; want one row and some events", len(rowsBefore), len(eventsBefore))
	}

	if _, err := srv.client.Run(ctx, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v", err)
	}
	m.Tidy(ctx, srv.server)

	if got := listSessions(t, db); !reflect.DeepEqual(got, rowsBefore) {
		t.Fatalf("rows after kill-server = %+v, want unchanged %+v", got, rowsBefore)
	}
	if got := listEvents(t, db); !reflect.DeepEqual(got, eventsBefore) {
		t.Fatalf("events after kill-server = %+v, want unchanged %+v", got, eventsBefore)
	}
}

func TestTidyServeWithConfiguredServerTidiesAtStart(t *testing.T) {
	srv := startTidyServer(t)
	st, db := openFrameStore(t)
	serve(t, daemon.Config{
		Server:       srv.server,
		Mirror:       daemon.NewMirror(st, tidyClock, nil),
		TidyInterval: time.Hour,
	})

	id := waitForPaneID(t, srv.client, srv.pane)
	if row := mustSession(t, db, id); row.Pane != srv.pane || row.Server != tidyLocalServer(srv) {
		t.Fatalf("row = server %q pane %q, want server %q pane %q",
			row.Server, row.Pane, tidyLocalServer(srv), srv.pane)
	}
}

func TestTidyServeTidiesAgainAfterInterval(t *testing.T) {
	srv := startTidyServer(t)
	st, db := openFrameStore(t)
	serve(t, daemon.Config{
		Server:       srv.server,
		Mirror:       daemon.NewMirror(st, tidyClock, nil),
		TidyInterval: 20 * time.Millisecond,
	})

	waitForPaneID(t, srv.client, srv.pane)
	later := newPane(t, srv)
	laterID := waitForPaneID(t, srv.client, later)
	if row := mustSession(t, db, laterID); row.Pane != later {
		t.Fatalf("row pane = %q, want %q", row.Pane, later)
	}
}

func TestTidyServeWithoutConfiguredServerTidiesFrameServer(t *testing.T) {
	srv := startTidyServer(t)
	st, db := openFrameStore(t)
	socket := serve(t, daemon.Config{
		Mirror:       daemon.NewMirror(st, tidyClock, nil),
		TidyInterval: 20 * time.Millisecond,
	})

	sendFrame(t, socket, frameFor(userPromptSubmitHook, srv.pane, srv.socket))
	waitForPaneID(t, srv.client, srv.pane)

	later := newPane(t, srv)
	laterID := waitForPaneID(t, srv.client, later)
	if row := mustSession(t, db, laterID); row.Pane != later {
		t.Fatalf("row pane = %q, want %q", row.Pane, later)
	}
}
