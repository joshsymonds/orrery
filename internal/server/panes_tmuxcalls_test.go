package server_test

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/exectest"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// Synthetic questions of the two panes of the tmux call fixture that ask one.
const (
	tmuxCallsWorkingQuestion = "synthetic working question?"
	tmuxCallsDoneQuestion    = "synthetic done question?"
)

// tmuxCallsFixture holds the IDs of the three panes of the tmux call fixture and a handler that lists them. The working
// pane has a transcript with no interruption, the done pane has a transcript and a turn end in the store, and the plain
// pane has no orrery options.
type tmuxCallsFixture struct {
	working string
	done    string
	plain   string
	handler http.Handler
}

// newTmuxCallsFixture starts the three panes of tmuxCallsFixture on a fresh tmux server and returns them with a handler
// that reads turn ends from a seeded store. Nothing changes after a stream opens, so a stream sends only its first delta.
func newTmuxCallsFixture(t *testing.T) tmuxCallsFixture {
	t.Helper()
	tmux := newTmuxServer(t)
	working := startFixturePane(t, tmux, "tmux-working", map[string]string{
		protocol.OptionState:      string(protocol.StateWorking),
		protocol.OptionTranscript: writeNewestTranscript(t, newestReplyLine(newestStamp0900)),
		protocol.OptionID:         turnOpenSession,
		protocol.OptionQuestion:   tmuxCallsWorkingQuestion,
	})
	done := startFixturePane(t, tmux, "tmux-done", map[string]string{
		protocol.OptionState:      string(protocol.StateDone),
		protocol.OptionTranscript: writeNewestTranscript(t, newestReplyLine(newestStamp0905)),
		protocol.OptionID:         turnDoneSession,
		protocol.OptionQuestion:   tmuxCallsDoneQuestion,
	})
	plain := startFixturePane(t, tmux, "tmux-plain", nil)

	path := filepath.Join(t.TempDir(), "orrery.db")
	seedTurnEnds(t, path)
	handler, err := server.New(server.Config{
		Logins:   []string{allowedLogin},
		Tmux:     tmux,
		Push:     newPushStore(t),
		Sessions: store.NewReader(path),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tmuxCallsFixture{working: working, done: done, plain: plain, handler: handler}
}

// startFixturePane starts a session named session on a pane running longCommand, sets each of options on that pane,
// and returns the pane ID.
func startFixturePane(t *testing.T, tmux *tmuxctl.Client, session string, options map[string]string) string {
	t.Helper()
	startSession(t, tmux, session, longCommand)
	pane := paneID(t, tmux, session)
	for name, value := range options {
		if err := tmux.SetPaneOption(t.Context(), pane, name, value); err != nil {
			t.Fatalf("SetPaneOption(%s, %s): %v", pane, name, err)
		}
	}
	return pane
}

// installTmuxShim puts a tmux script first on PATH. Each call the script receives appends one line to the count file
// it returns, then runs the tmux that PATH named before, so the count is the number of tmux commands run after this.
func installTmuxShim(t *testing.T) string {
	t.Helper()
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatalf("LookPath(tmux): %v", err)
	}
	dir := t.TempDir()
	count := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho call >> " + shellQuote(count) + "\nexec " + shellQuote(tmuxPath) + " \"$@\"\n"
	exectest.WriteFile(t, filepath.Join(dir, "tmux"), []byte(script), 0o700)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return count
}

// shellQuote returns s in single quotes for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// tmuxCalls returns the number of tmux commands the shim installed with count has run.
func tmuxCalls(t *testing.T, count string) int {
	t.Helper()
	data, err := os.ReadFile(count)
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("read tmux call count: %v", err)
	}
	return strings.Count(string(data), "\n")
}

// TestPanesTmuxCallsList is R1 for Panes.List: a listing that ends no turn runs one tmux command at three panes.
func TestPanesTmuxCallsList(t *testing.T) {
	fixture := newTmuxCallsFixture(t)
	count := installTmuxShim(t)

	if panes := listPanes(t, fixture.handler); len(panes) != 3 {
		t.Fatalf("Panes.List has %d panes, want 3", len(panes))
	}
	if got := tmuxCalls(t, count); got != 1 {
		t.Fatalf("Panes.List ran %d tmux commands, want 1", got)
	}
}

// TestPanesTmuxCallsWatchStep is R1 for WatchConversation: the first step on a working pane runs one tmux command.
func TestPanesTmuxCallsWatchStep(t *testing.T) {
	fixture := newTmuxCallsFixture(t)
	count := installTmuxShim(t)

	req := &orreryv1.WatchConversationRequest{Pane: fixture.working}
	stream := openConversation(t, panesClient(t, fixture.handler), req)
	msg := stream.next(t)
	if got := msg.GetState().GetPane().GetId(); got != fixture.working {
		t.Fatalf("first delta is for pane %q, want %q", got, fixture.working)
	}
	if got := tmuxCalls(t, count); got != 1 {
		t.Fatalf("WatchConversation's first step ran %d tmux commands, want 1", got)
	}
}

// TestPanesTmuxCallsFields is R2: each pane of the listing answers its id, state, question hash, newest reply and
// last turn end.
func TestPanesTmuxCallsFields(t *testing.T) {
	t.Parallel()
	fixture := newTmuxCallsFixture(t)

	panes := listPanes(t, fixture.handler)
	if len(panes) != 3 {
		t.Fatalf("Panes.List has %d panes, want 3", len(panes))
	}
	byID := make(map[string]*orreryv1.Pane, len(panes))
	for _, pane := range panes {
		byID[pane.GetId()] = pane
	}
	cases := []struct {
		name     string
		id       string
		state    string
		question string
		newest   int64
		turnEnd  int64
	}{
		{
			name:     "working pane",
			id:       fixture.working,
			state:    string(protocol.StateWorking),
			question: tmuxCallsWorkingQuestion,
			newest:   newestUnix0900,
			turnEnd:  0,
		},
		{
			name:     "done pane",
			id:       fixture.done,
			state:    string(protocol.StateDone),
			question: tmuxCallsDoneQuestion,
			newest:   newestUnix0905,
			turnEnd:  turnEndUnix,
		},
		{
			name:     "plain pane",
			id:       fixture.plain,
			state:    "",
			question: "",
			newest:   0,
			turnEnd:  0,
		},
	}
	for _, tc := range cases {
		pane, ok := byID[tc.id]
		if !ok {
			t.Fatalf("%s %s is not in Panes.List", tc.name, tc.id)
		}
		if pane.GetState() != tc.state {
			t.Errorf("%s state = %q, want %q", tc.name, pane.GetState(), tc.state)
		}
		if pane.GetQuestionHash() != protocol.QuestionHash(tc.question) {
			t.Errorf("%s question_hash = %q, want the hash of %q", tc.name, pane.GetQuestionHash(), tc.question)
		}
		if pane.GetNewestReply() != tc.newest {
			t.Errorf("%s newest_reply = %d, want %d", tc.name, pane.GetNewestReply(), tc.newest)
		}
		if pane.GetLastTurnEnd() != tc.turnEnd {
			t.Errorf("%s last_turn_end = %d, want %d", tc.name, pane.GetLastTurnEnd(), tc.turnEnd)
		}
	}
}
