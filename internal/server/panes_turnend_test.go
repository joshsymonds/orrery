package server_test

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Synthetic @orrery_id values of the turn-end fixtures. turnDoneSession went done at turnEndUnix and later went
// working again; turnOpenSession only went working.
const (
	turnDoneSession = "orrery-turn-done"
	turnOpenSession = "orrery-turn-open"
	turnEndUnix     = int64(1794301200)
)

// seedTurnEnds writes a store at path with the state events of the turn-end fixtures and closes it.
func seedTurnEnds(t *testing.T, path string) {
	t.Helper()
	ctx := t.Context()
	s, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	steps := []struct {
		id    string
		pane  string
		state string
		at    int64
	}{
		{turnDoneSession, "%1", "done", turnEndUnix},
		{turnOpenSession, "%2", "working", turnEndUnix + 5},
		{turnDoneSession, "%1", "working", turnEndUnix + 60},
	}
	for _, step := range steps {
		pane := store.Pane{ID: step.id, Pane: step.pane, Fields: store.Fields{State: step.state}}
		if err = s.SyncPane(ctx, "-L synthetic", pane, time.Unix(step.at, 0)); err != nil {
			t.Fatalf("SyncPane %s: %v", step.id, err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}
}

// startTurnPane starts a session named session on a pane running longCommand, sets its @orrery_id to orreryID
// when orreryID is not empty, and returns the pane ID.
func startTurnPane(t *testing.T, tmux *tmuxctl.Client, session, orreryID string) string {
	t.Helper()
	startSession(t, tmux, session, longCommand)
	pane := paneID(t, tmux, session)
	if orreryID != "" {
		if err := tmux.SetPaneOption(t.Context(), pane, protocol.OptionID, orreryID); err != nil {
			t.Fatalf("SetPaneOption(%s, %s): %v", pane, protocol.OptionID, err)
		}
	}
	return pane
}

// newTurnEndHandler builds the guarded handler that admits allowedLogin, routes to tmux, and reads turn ends
// from reader, which is nil for no store.
func newTurnEndHandler(t *testing.T, tmux *tmuxctl.Client, reader *store.Reader) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:   []string{allowedLogin},
		Tmux:     tmux,
		Push:     newPushStore(t),
		Sessions: reader,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

func TestServePanesLastTurnEnd(t *testing.T) {
	t.Parallel()

	tmux := newTmuxServer(t)
	donePane := startTurnPane(t, tmux, "turn-done", turnDoneSession)
	openPane := startTurnPane(t, tmux, "turn-open", turnOpenSession)
	plainPane := startTurnPane(t, tmux, "turn-plain", "")
	path := filepath.Join(t.TempDir(), "orrery.db")
	seedTurnEnds(t, path)

	cases := []struct {
		name     string
		reader   *store.Reader
		doneWant int64
	}{
		{
			name:     "done session answers its done time after it went working again",
			reader:   store.NewReader(path),
			doneWant: turnEndUnix,
		},
		{
			name:     "no reader answers 0",
			reader:   nil,
			doneWant: 0,
		},
		{
			name:     "missing store file answers 0",
			reader:   store.NewReader(filepath.Join(t.TempDir(), "absent.db")),
			doneWant: 0,
		},
	}
	for _, tc := range cases {
		listed := listPanes(t, newTurnEndHandler(t, tmux, tc.reader))
		if len(listed) != 3 {
			t.Fatalf("%s: got %d panes, want 3", tc.name, len(listed))
		}
		ends := make(map[string]int64, len(listed))
		for _, pane := range listed {
			ends[pane.GetId()] = pane.GetLastTurnEnd()
		}
		if got := ends[donePane]; got != tc.doneWant {
			t.Errorf("%s: done pane last_turn_end = %d, want %d", tc.name, got, tc.doneWant)
		}
		if got := ends[openPane]; got != 0 {
			t.Errorf("%s: pane without a done event last_turn_end = %d, want 0", tc.name, got)
		}
		if got := ends[plainPane]; got != 0 {
			t.Errorf("%s: pane without @orrery_id last_turn_end = %d, want 0", tc.name, got)
		}
	}
}
