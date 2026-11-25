package panehost_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/autoseen"
	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/node"
	"github.com/joshsymonds/orrery/internal/panehost"
	"github.com/joshsymonds/orrery/internal/paneregistry"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// fakeClaudeSource is the scripted claude that the summary tests run. It is copied into a fresh directory for each
// test.
const fakeClaudeSource = "../summary/testdata/fake-claude.sh"

// summaryReply is the reply the fake claude prints. A done pane takes its summary and label from it.
const (
	summaryReply = "summary: host summarizes its own pane\nlabel: host summarizes pane\n"
	wantSummary  = "host summarizes its own pane"
	wantLabel    = "host summarizes pane"
)

// sessionID is the id of the test session, whose transcript is under the test's home.
const sessionID = "synthetic-session"

// installClaude copies the fake claude into a fresh directory as claude, and writes reply beside it as the reply it
// prints. It returns the path of the copy.
func installClaude(t *testing.T, reply string) string {
	t.Helper()
	src, err := os.ReadFile(fakeClaudeSource)
	if err != nil {
		t.Fatalf("read fake claude: %v", err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	exectest.WriteFile(t, bin, src, 0o755)
	exectest.WriteFile(t, filepath.Join(dir, "reply"), []byte(reply), 0o600)
	return bin
}

// summaryHome sets HOME to a fresh directory, under which the summarizer finds the user memory a run excludes, and
// clears the claude config directory so HOME decides it. It returns the directory.
func summaryHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

// writeTranscript writes the transcript of the test session, holding one prompt, where the host reads it: under home,
// by the session's working directory cwd.
func writeTranscript(t *testing.T, home, cwd string) {
	t.Helper()
	path := paneregistry.TranscriptPath(home, cwd, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make transcript directory: %v", err)
	}
	prompt := `{"type":"user","message":{"role":"user","content":"synthetic prompt"}}` + "\n"
	if err := os.WriteFile(path, []byte(prompt), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

// tmuxEnv is the environment of the test session's claude process: it runs in pane on server.
func tmuxEnv(server tmuxctl.Server, pane string) map[string]string {
	return map[string]string{"TMUX": server.Socket + ",1,0", "TMUX_PANE": pane}
}

// writeSession writes the registry entry of the test session in cwd with status, so the pane's state follows it.
func writeSession(t *testing.T, dir, cwd, status string) {
	t.Helper()
	writeEntry(t, dir, paneregistry.Entry{
		PID:        synthetic,
		SessionID:  sessionID,
		Cwd:        cwd,
		Entrypoint: paneregistry.EntrypointCLI,
		Status:     status,
	})
}

// waitEvent reads events until one satisfies match. Events it passes over are dropped.
func waitEvent(events <-chan node.PaneEvent, match func(node.PaneEvent) bool) {
	for event := range events {
		if match(event) {
			return
		}
	}
}

// transitionTo matches an event whose transition moves a pane to state.
func transitionTo(state protocol.State) func(node.PaneEvent) bool {
	return func(event node.PaneEvent) bool {
		return event.Transition != nil && event.Transition.To == state
	}
}

// summarized matches an event whose snapshot carries the summary and label the fake claude prints.
func summarized(event node.PaneEvent) bool {
	return event.Pane != nil && event.Pane.Fields.Summary == wantSummary && event.Pane.Fields.Label == wantLabel
}

// summaryHost is a host that runs the fake claude over one test pane, with the events it publishes.
type summaryHost struct {
	dir    string // the session registry directory
	cwd    string // the working directory of the test session
	events <-chan node.PaneEvent
}

// startSummaryHost runs a host over pane on server, and watches its events. Its claude is the fake one, which prints
// summaryReply, and its viewer is viewer, or autoseen.Viewing when viewer is nil. The test session's transcript is
// written under the host's home.
func startSummaryHost(t *testing.T, server tmuxctl.Server, pane string, viewer autoseen.Viewer) summaryHost {
	t.Helper()
	home := summaryHome(t)
	cwd := t.TempDir()
	writeTranscript(t, home, cwd)
	dir := t.TempDir()
	host := runHost(t, panehost.Config{
		Name:          "synthetic-host",
		Dir:           dir,
		Home:          home,
		Proc:          fakeProc{env: tmuxEnv(server, pane)},
		DefaultServer: server,
		Ticks:         make(chan time.Time),
		Now:           fixedNow,
		Claude:        installClaude(t, summaryReply),
		Viewer:        viewer,
	})
	events := watchEvents(t, host)
	if first := <-events; !first.Full {
		t.Fatalf("first event = %+v, want a full listing", first)
	}
	return summaryHost{dir: dir, cwd: cwd, events: events}
}

// finish moves the test session to done: busy, which makes the pane working, then idle, which makes it done.
func (h summaryHost) finish(t *testing.T) {
	t.Helper()
	writeSession(t, h.dir, h.cwd, paneregistry.StatusBusy)
	waitEvent(h.events, transitionTo(protocol.StateWorking))
	writeSession(t, h.dir, h.cwd, paneregistry.StatusIdle)
	waitEvent(h.events, transitionTo(protocol.StateDone))
}

// TestPaneSummariesOnHost checks that a done pane on the host's own tmux gets its summary and label from the host's
// claude run, and that the event stream carries them: the host writes the options to the pane, then publishes the
// pane's snapshot, so the controller stores the new summary and label under the host.
func TestPaneSummariesOnHost(t *testing.T) {
	server, pane := startServer(t)
	host := startSummaryHost(t, server, pane, nil)
	host.finish(t)
	waitEvent(host.events, summarized)

	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	for option, want := range map[string]string{protocol.OptionSummary: wantSummary, protocol.OptionLabel: wantLabel} {
		got, ok, err := client.PaneOption(t.Context(), pane, option)
		if err != nil {
			t.Fatalf("read %s: %v", option, err)
		}
		if !ok || got != want {
			t.Errorf("%s on the host's pane = %q (set %v), want %q", option, got, ok, want)
		}
	}
}

// TestPaneSummariesAutoSeen checks that a done pane the host's tmux client views is marked seen on the host's tmux. The
// viewer is handed the host's tmux client, and views the pane only when that client is on the pane's server, so the
// seen mark can come only from a check of the host's own tmux.
func TestPaneSummariesAutoSeen(t *testing.T) {
	server, pane := startServer(t)
	viewer := func(ctx context.Context, client *tmuxctl.Client, _ string, _ time.Time) (bool, error) {
		socket, err := client.Run(ctx, "display", "-p", "#{socket_path}")
		if err != nil {
			return false, err
		}
		return strings.TrimSpace(socket) == server.Socket, nil
	}
	host := startSummaryHost(t, server, pane, viewer)
	host.finish(t)
	// The summary is written once the done transition has finished, auto-seen check included, since the write takes
	// the host's lock; so the summary's snapshot is published after the seen mark.
	waitEvent(host.events, summarized)

	client, err := tmuxctl.New(server)
	if err != nil {
		t.Fatalf("New(%+v): %v", server, err)
	}
	got, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionSeen)
	if err != nil {
		t.Fatalf("read %s: %v", protocol.OptionSeen, err)
	}
	if want := "1"; !ok || got != want {
		t.Errorf("%s on the host's pane = %q (set %v), want %q: the host's client views the pane",
			protocol.OptionSeen, got, ok, want)
	}
}
