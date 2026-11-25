package tmuxctl_test

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
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

// waitForExec polls pane until the pane's own process has exec'd sleep, the command every test pane runs
// (testCommand): its /proc comm is sleep. It has no deadline of its own. The pane's foreground command, which tmux
// reports, is no such test: while the pane's shell starts, tmux can report sleep and then the shell again before the
// exec, so a wait that saw sleep once could see the shell next. An exec does not go back, and after it the pane's
// foreground command is sleep and stays so.
func waitForExec(t *testing.T, client *tmuxctl.Client, pane string) {
	t.Helper()
	ticker := time.NewTicker(commandPollEach)
	defer ticker.Stop()
	for !paneExeced(t, client, pane, "sleep") {
		<-ticker.C
	}
}

// paneExeced reports whether the process tmux started for pane has exec'd want: its /proc comm is want. It fails the
// test if the process has exited, since it can exec nothing more.
func paneExeced(t *testing.T, client *tmuxctl.Client, pane, want string) bool {
	t.Helper()
	out, err := client.Run(t.Context(), "display-message", "-p", "-t", pane, "#{pane_pid}")
	if err != nil {
		t.Fatalf("pane %s pane_pid: %v", pane, err)
	}
	comm, err := os.ReadFile(filepath.Join("/proc", strings.TrimSpace(out), "comm"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("pane %s never exec'd %s: its process exited first", pane, want)
	}
	return err == nil && strings.TrimSpace(string(comm)) == want
}

// currentCommand returns the foreground command tmux reports for pane.
func currentCommand(t *testing.T, client *tmuxctl.Client, pane string) string {
	t.Helper()
	out, err := client.Run(t.Context(), "display-message", "-p", "-t", pane, "#{pane_current_command}")
	if err != nil {
		t.Fatalf("pane %s pane_current_command: %v", pane, err)
	}
	return strings.TrimSpace(out)
}

// awaitCurrentCommand polls pane until tmux reports want as its foreground command. It has no deadline of its own.
func awaitCurrentCommand(t *testing.T, client *tmuxctl.Client, pane, want string) {
	t.Helper()
	ticker := time.NewTicker(commandPollEach)
	defer ticker.Stop()
	for currentCommand(t, client, pane) != want {
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
		waitForExec(t, client, pane)
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
		waitForExec(t, client, pane)
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

// TestPaneCommandCanReadTheCommandBeforeTheShell forces the order a busy machine can give a starting pane: tmux reports
// the pane's command as sleep, then as the shell, then as sleep again. The pane's process first runs a shell that
// calls itself sleep, then a plain shell, then execs sleep, each step held on a fifo the test writes. Seeing sleep
// once is no proof the pane runs it; its process's exec is, and only the last step makes it.
func TestPaneCommandCanReadTheCommandBeforeTheShell(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gates := [2]string{filepath.Join(dir, "gate1"), filepath.Join(dir, "gate2")}
	for _, gate := range gates {
		if err := syscall.Mkfifo(gate, 0o600); err != nil {
			t.Fatalf("make fifo %s: %v", gate, err)
		}
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("find bash: %v", err)
	}
	// Step one is a bash whose argv[0] is sleep; step two a bash; step three sleep itself.
	script := fmt.Sprintf(
		`exec -a sleep %[1]s -c 'read -r go < %[2]s; exec %[1]s -c "read -r go < %[3]s; exec sleep 300"'`,
		bash, gates[0], gates[1])
	client := newServer(t)
	mustRun(t, client, "-f", noConfig, "new-session", "-d", "-s", "s1", "-c", dir, bash+" -c "+strconv.Quote(script))
	pane := paneIDs(t, client, "s1")[0]

	awaitCurrentCommand(t, client, pane, "sleep")
	if paneExeced(t, client, pane, "sleep") {
		t.Fatal("pane counts as running sleep while its process is a shell named sleep")
	}
	releaseGate(t, gates[0])
	awaitCurrentCommand(t, client, pane, "bash")
	if paneExeced(t, client, pane, "sleep") {
		t.Fatal("pane counts as running sleep while its process is bash")
	}
	releaseGate(t, gates[1])
	waitForExec(t, client, pane)
	if got := currentCommand(t, client, pane); got != "sleep" {
		t.Fatalf("pane command after the exec = %q, want sleep", got)
	}
}

// releaseGate writes one line to the fifo at path, which releases the pane step that reads it.
func releaseGate(t *testing.T, path string) {
	t.Helper()
	gate, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open gate %s: %v", path, err)
	}
	if _, err = gate.WriteString("go\n"); err != nil {
		t.Fatalf("release gate %s: %v", path, err)
	}
	if err = gate.Close(); err != nil {
		t.Fatalf("close gate %s: %v", path, err)
	}
}

// TestWritePaneOptionsKeepsSemicolons writes, in one invocation, values that end in ";", a lone ";", one that ends in
// "\;", and one with ";" inside, then unsets one of them. tmux reads a final ";" of an argument as a command separator,
// so each value must come back as written, and the unset must reach its option.
func TestWritePaneOptionsKeepsSemicolons(t *testing.T) {
	client, pane := startPane(t)
	setPaneOption(t, client, pane, protocol.OptionTool, "to unset")
	values := map[string]string{
		protocol.OptionSummary:  "ends;",
		protocol.OptionLabel:    ";",
		protocol.OptionQuestion: `ends\;`,
		protocol.OptionContext:  "in;side",
	}
	names := []string{protocol.OptionSummary, protocol.OptionLabel, protocol.OptionQuestion, protocol.OptionContext}
	writes := make([]tmuxctl.OptionWrite, 0, 1+len(names))
	writes = append(writes, tmuxctl.Unset(protocol.OptionTool))
	for _, name := range names {
		writes = append(writes, tmuxctl.Set(name, values[name]))
	}
	if err := client.WritePaneOptions(t.Context(), pane, writes); err != nil {
		t.Fatalf("WritePaneOptions: %v", err)
	}
	for name, want := range values {
		got, ok, err := client.PaneOption(t.Context(), pane, name)
		if err != nil || !ok || got != want {
			t.Errorf("%s = (%q, %v, %v), want (%q, true, nil)", name, got, ok, err, want)
		}
	}
	if _, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionTool); err != nil || ok {
		t.Errorf("%s set after its unset (%v)", protocol.OptionTool, err)
	}
}

// TestWritePaneOptionsExpandsFormats writes one option with SetFormat and one with Set, both given the same format: the
// SetFormat write stores what tmux expands it to, the pane's foreground command, and the Set write stores it as text.
// The test waits for the pane's process to exec sleep before it writes, then reads the command in the same invocation as
// the writes, so the command is the one the pane runs, not the shell it starts as.
func TestWritePaneOptionsExpandsFormats(t *testing.T) {
	client, pane := startPane(t)
	waitForExec(t, client, pane)
	const format = "#{pane_current_command}"
	writes := []tmuxctl.OptionWrite{
		tmuxctl.SetFormat(protocol.OptionCommand, format),
		tmuxctl.Set(protocol.OptionSummary, format),
	}
	cmds := append(tmuxctl.WriteCommands(pane, writes), []string{"display-message", "-p", "-t", pane, format})
	out, err := client.Batch(t.Context(), cmds...)
	if err != nil {
		t.Fatalf("write the options and read the pane's command: %v", err)
	}
	command := strings.TrimSuffix(out, "\n")
	if command == "" {
		t.Fatal("the pane's command reads empty")
	}
	if command != "sleep" {
		t.Fatalf("the pane's command reads %q, want sleep", command)
	}
	got, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionCommand)
	if err != nil || !ok || got != command {
		t.Errorf("%s = (%q, %v, %v), want (%q, true, nil)", protocol.OptionCommand, got, ok, err, command)
	}
	got, ok, err = client.PaneOption(t.Context(), pane, protocol.OptionSummary)
	if err != nil || !ok || got != format {
		t.Errorf("%s = (%q, %v, %v), want (%q, true, nil)", protocol.OptionSummary, got, ok, err, format)
	}
}

// TestPaneValuesReadsInOneLine reads a set option, an option set to a value with ";" and a format, and an unset
// option, which reads as "".
func TestPaneValuesReadsInOneLine(t *testing.T) {
	client, pane := startPane(t)
	setPaneOption(t, client, pane, protocol.OptionState, "working")
	setPaneOption(t, client, pane, protocol.OptionSummary, "a; #{b};")
	got, err := client.PaneValues(t.Context(), pane,
		protocol.OptionState, protocol.OptionSummary, protocol.OptionLabel, "session_name")
	if err != nil {
		t.Fatalf("PaneValues: %v", err)
	}
	want := []string{"working", "a; #{b};", "", "s1"}
	if !slices.Equal(got, want) {
		t.Fatalf("PaneValues = %q, want %q", got, want)
	}
}

// TestBatchRunsCommandsInOrder runs two reads as one invocation and returns both outputs in order, and runs nothing
// for no commands.
func TestBatchRunsCommandsInOrder(t *testing.T) {
	client, pane := startPane(t)
	out, err := client.Batch(t.Context(),
		[]string{"display-message", "-p", "-t", pane, "#{session_name}"},
		[]string{"display-message", "-p", "-t", pane, "#{pane_id}"},
	)
	if err != nil || out != "s1\n"+pane+"\n" {
		t.Fatalf("Batch = (%q, %v), want (%q, nil)", out, err, "s1\n"+pane+"\n")
	}
	if out, err = client.Batch(t.Context()); err != nil || out != "" {
		t.Fatalf("Batch() = (%q, %v), want nothing", out, err)
	}
}
