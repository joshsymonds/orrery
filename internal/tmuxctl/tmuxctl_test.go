package tmuxctl_test

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
	"github.com/joshsymonds/orrery/internal/tmuxtest"
)

// Every test server starts with -f /dev/null so the user's tmux config never applies.
const (
	noConfig        = "/dev/null"
	testCommand     = "sleep 300"
	commandPollEach = 10 * time.Millisecond
	exampleTool     = `{"id":"toolu_example","name":"Bash","input":"go test ./...","start":1700000400}`
)

// newServer returns a client for a unique private server name and kills that server at cleanup.
func newServer(t *testing.T) *tmuxctl.Client {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-test-%d-%x", os.Getpid(), suffix)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	stopServerAtCleanup(t, tmuxctl.Server{Name: name})
	return client
}

func stopServerAtCleanup(t *testing.T, server tmuxctl.Server) {
	t.Helper()
	t.Cleanup(func() {
		if err := tmuxtest.Stop(t, server); err != nil {
			t.Errorf("kill-server: %v", err)
		}
	})
}

func mustRun(t *testing.T, client *tmuxctl.Client, args ...string) string {
	t.Helper()
	out, err := client.Run(t.Context(), args...)
	if err != nil {
		t.Fatalf("Run(%v): %v", args, err)
	}
	return out
}

// startSession creates detached session name in dir running a long-lived command, starting the server if needed.
func startSession(t *testing.T, client *tmuxctl.Client, name, dir string) {
	t.Helper()
	mustRun(t, client, "-f", noConfig, "new-session", "-d", "-s", name, "-c", dir, testCommand)
}

// waitForCommand polls pane until its foreground command is want. It has no deadline of its own.
// A pane reports its command only after its shell has exec'd it, so listing earlier shows the shell instead.
func waitForCommand(t *testing.T, client *tmuxctl.Client, pane, want string) {
	t.Helper()
	ctx := t.Context()
	ticker := time.NewTicker(commandPollEach)
	defer ticker.Stop()
	for {
		out, err := client.Run(ctx, "display-message", "-p", "-t", pane, "#{pane_current_command}")
		if err != nil {
			t.Fatalf("pane %s pane_current_command: %v", pane, err)
		}
		got := strings.TrimSpace(out)
		if got == want {
			return
		}
		<-ticker.C
	}
}

func paneIDs(t *testing.T, client *tmuxctl.Client, session string) []string {
	t.Helper()
	return strings.Fields(mustRun(t, client, "list-panes", "-t", session, "-F", "#{pane_id}"))
}

// sessionActivity returns session's #{session_activity} as tmux reports it, in unix seconds.
func sessionActivity(t *testing.T, client *tmuxctl.Client, session string) int64 {
	t.Helper()
	out := mustRun(t, client, "display-message", "-p", "-t", session, "#{session_activity}")
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		t.Fatalf("session %s session_activity %q: %v", session, out, err)
	}
	return n
}

// startPane returns a client whose server has one session with one pane, and that pane's ID.
func startPane(t *testing.T) (*tmuxctl.Client, string) {
	t.Helper()
	client := newServer(t)
	startSession(t, client, "s1", t.TempDir())
	return client, paneIDs(t, client, "s1")[0]
}

func setPaneOption(t *testing.T, client *tmuxctl.Client, pane, name, value string) {
	t.Helper()
	if err := client.SetPaneOption(t.Context(), pane, name, value); err != nil {
		t.Fatalf("SetPaneOption(%s, %s, %q): %v", pane, name, value, err)
	}
}

func TestNewRejectsNameAndSocket(t *testing.T) {
	_, err := tmuxctl.New(tmuxctl.Server{
		Name:   "orrery-test-both",
		Socket: filepath.Join(t.TempDir(), "both.sock"),
	})
	if err == nil {
		t.Fatal("New with Name and Socket: want error, got nil")
	}
}

func TestRunReturnsStdoutAndStderr(t *testing.T) {
	client := newServer(t)
	startSession(t, client, "s1", t.TempDir())

	if got := mustRun(t, client, "display-message", "-p", "-t", "s1", "#{session_name}"); got != "s1\n" {
		t.Fatalf("stdout = %q, want %q", got, "s1\n")
	}

	_, err := client.Run(t.Context(), "no-such-command")
	if err == nil {
		t.Fatal("Run(no-such-command): want error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("error %q does not carry tmux stderr", err)
	}
}

func TestSocketServer(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "s.sock")
	client, err := tmuxctl.New(tmuxctl.Server{Socket: socket})
	if err != nil {
		t.Fatalf("New(Socket=%q): %v", socket, err)
	}
	stopServerAtCleanup(t, tmuxctl.Server{Socket: socket})

	startSession(t, client, "s1", t.TempDir())
	if got := mustRun(t, client, "display-message", "-p", "-t", "s1", "#{session_name}"); got != "s1\n" {
		t.Fatalf("stdout = %q, want %q", got, "s1\n")
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("socket %s not created: %v", socket, err)
	}
}

func TestPaneOptionRoundTrip(t *testing.T) {
	client, pane := startPane(t)
	setPaneOption(t, client, pane, protocol.OptionLabel, "example label")

	got, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionLabel)
	if err != nil {
		t.Fatalf("PaneOption: %v", err)
	}
	if !ok || got != "example label" {
		t.Fatalf("PaneOption = (%q, %v), want (%q, true)", got, ok, "example label")
	}
}

func TestPaneOptionUnsetReportsNotOK(t *testing.T) {
	client, pane := startPane(t)

	got, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionSummary)
	if err != nil {
		t.Fatalf("PaneOption: %v", err)
	}
	if ok || got != "" {
		t.Fatalf("PaneOption on unset option = (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestPaneOptionSetToEmptyReportsOK(t *testing.T) {
	client, pane := startPane(t)
	setPaneOption(t, client, pane, protocol.OptionQuestion, "")

	got, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionQuestion)
	if err != nil {
		t.Fatalf("PaneOption: %v", err)
	}
	if !ok || got != "" {
		t.Fatalf("PaneOption on empty option = (%q, %v), want (\"\", true)", got, ok)
	}
}

func TestUnsetPaneOptionRemoves(t *testing.T) {
	client, pane := startPane(t)
	setPaneOption(t, client, pane, protocol.OptionLabel, "example label")

	if err := client.UnsetPaneOption(t.Context(), pane, protocol.OptionLabel); err != nil {
		t.Fatalf("UnsetPaneOption: %v", err)
	}
	_, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionLabel)
	if err != nil {
		t.Fatalf("PaneOption after unset: %v", err)
	}
	if ok {
		t.Fatal("PaneOption after unset: ok = true, want false")
	}
}

func TestListPanesReturnsEveryPane(t *testing.T) {
	client := newServer(t)
	dir := filepath.Join(t.TempDir(), "example dir")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	startSession(t, client, "s1", dir)
	mustRun(t, client, "split-window", "-t", "s1", "-c", dir, testCommand)
	startSession(t, client, "s2", dir)

	s1 := paneIDs(t, client, "s1")
	s2 := paneIDs(t, client, "s2")
	if len(s1) != 2 || len(s2) != 1 {
		t.Fatalf("pane IDs s1=%v s2=%v, want 2 and 1", s1, s2)
	}

	setPaneOption(t, client, s1[0], protocol.OptionState, string(protocol.StateWaiting))
	setPaneOption(t, client, s1[0], protocol.OptionChanged, "1700000000")
	setPaneOption(t, client, s1[0], protocol.OptionSeen, "1700000100")
	setPaneOption(t, client, s1[0], protocol.OptionSummary, "example summary\twith tab")
	setPaneOption(t, client, s1[0], protocol.OptionLabel, "example label")
	setPaneOption(t, client, s1[0], protocol.OptionQuestion, "example question?")
	setPaneOption(t, client, s1[0], protocol.OptionContext, "example-context")
	setPaneOption(t, client, s1[0], protocol.OptionClaudeSession, "example-claude-session")
	setPaneOption(t, client, s1[0], protocol.OptionTool, exampleTool)

	for _, pane := range append(s1, s2...) {
		waitForCommand(t, client, pane, "sleep")
	}
	s1Activity := sessionActivity(t, client, "s1")
	s2Activity := sessionActivity(t, client, "s2")

	got, err := client.ListPanes(t.Context())
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	want := []protocol.Pane{
		{
			ID: s1[0], Session: "s1", WindowIndex: 0, PaneIndex: 0, Cwd: resolved, Command: "sleep",
			State: protocol.StateWaiting, Summary: "example summary\twith tab", Label: "example label",
			Changed: 1700000000, Seen: 1700000100, Question: "example question?",
			Context: "example-context", ClaudeSession: "example-claude-session", Activity: s1Activity,
			Tool: exampleTool,
		},
		{ID: s1[1], Session: "s1", WindowIndex: 0, PaneIndex: 1, Cwd: resolved, Command: "sleep", Activity: s1Activity},
		{ID: s2[0], Session: "s2", WindowIndex: 0, PaneIndex: 0, Cwd: resolved, Command: "sleep", Activity: s2Activity},
	}

	if len(got) != len(want) {
		t.Fatalf("ListPanes returned %d panes, want %d: %+v", len(got), len(want), got)
	}
	byID := make(map[string]protocol.Pane, len(got))
	for _, pane := range got {
		byID[pane.ID] = pane
	}
	for _, w := range want {
		g, ok := byID[w.ID]
		if !ok {
			t.Errorf("pane %s missing from ListPanes", w.ID)
			continue
		}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("pane %s:\n got %+v\nwant %+v", w.ID, g, w)
		}
	}
}

func TestListPanesRejectsInvalidState(t *testing.T) {
	client, pane := startPane(t)
	if _, err := client.ListPanes(t.Context()); err != nil {
		t.Fatalf("ListPanes on clean pane: %v", err)
	}

	setPaneOption(t, client, pane, protocol.OptionState, "bogus")
	if _, err := client.ListPanes(t.Context()); err == nil {
		t.Fatal("ListPanes with invalid state: want error, got nil")
	}
}

func TestListPanesRejectsNonNumericTimestamps(t *testing.T) {
	for _, name := range []string{protocol.OptionChanged, protocol.OptionSeen} {
		t.Run(name, func(t *testing.T) {
			client, pane := startPane(t)
			if _, err := client.ListPanes(t.Context()); err != nil {
				t.Fatalf("ListPanes on clean pane: %v", err)
			}

			setPaneOption(t, client, pane, name, "soon")
			if _, err := client.ListPanes(t.Context()); err == nil {
				t.Fatalf("ListPanes with non-numeric %s: want error, got nil", name)
			}
		})
	}
}

func TestListCarriesOrreryOptions(t *testing.T) {
	t.Parallel()
	client := newServer(t)
	dir := t.TempDir()
	startSession(t, client, "s1", dir)
	mustRun(t, client, "split-window", "-t", "s1", "-c", dir, testCommand)

	panes := paneIDs(t, client, "s1")
	if len(panes) != 2 {
		t.Fatalf("pane IDs %v, want 2", panes)
	}
	setPaneOption(t, client, panes[0], protocol.OptionID, "example-orrery-id")
	setPaneOption(t, client, panes[0], protocol.OptionTranscript, "/example/transcript.jsonl")
	setPaneOption(t, client, panes[0], protocol.OptionTurnStart, "1700000200")
	for _, pane := range panes {
		waitForCommand(t, client, pane, "sleep")
	}

	listed, err := client.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	listedPanes, err := client.ListPanes(t.Context())
	if err != nil {
		t.Fatalf("ListPanes: %v", err)
	}
	if len(listed) != 2 || len(listedPanes) != 2 {
		t.Fatalf("List returned %d panes and ListPanes %d, want 2 each", len(listed), len(listedPanes))
	}

	plain := make(map[string]protocol.Pane, len(listedPanes))
	for _, pane := range listedPanes {
		plain[pane.ID] = pane
	}
	byID := make(map[string]tmuxctl.Listed, len(listed))
	for _, entry := range listed {
		byID[entry.ID] = entry
		p, ok := plain[entry.ID]
		if !ok {
			t.Errorf("pane %s missing from ListPanes", entry.ID)
			continue
		}
		if !reflect.DeepEqual(entry.Pane, p) {
			t.Errorf("pane %s Pane from List:\n got %+v\nwant %+v", entry.ID, entry.Pane, p)
		}
	}

	tagged := byID[panes[0]]
	if tagged.OrreryID != "example-orrery-id" || tagged.Transcript != "/example/transcript.jsonl" ||
		tagged.TurnStart != 1700000200 {
		t.Errorf("pane %s options = (%q, %q, %d), want (%q, %q, %d)", panes[0],
			tagged.OrreryID, tagged.Transcript, tagged.TurnStart,
			"example-orrery-id", "/example/transcript.jsonl", 1700000200)
	}
	untagged := byID[panes[1]]
	if untagged.OrreryID != "" || untagged.Transcript != "" || untagged.TurnStart != 0 {
		t.Errorf("pane %s options = (%q, %q, %d), want (\"\", \"\", 0)", panes[1],
			untagged.OrreryID, untagged.Transcript, untagged.TurnStart)
	}
}

func TestListRejectsNonNumericTurnStart(t *testing.T) {
	t.Parallel()
	client, pane := startPane(t)
	setPaneOption(t, client, pane, protocol.OptionTurnStart, "soon")

	_, err := client.List(t.Context())
	if err == nil {
		t.Fatal("List with non-numeric @orrery_turn_start: want error, got nil")
	}
	if !strings.Contains(err.Error(), pane) {
		t.Fatalf("List error %q does not name pane %s", err, pane)
	}
}
