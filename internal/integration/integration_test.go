// Package integration_test builds the orrery and orreryd binaries and proves each orrery subcommand
// by running the built orrery against private tmux servers and a listening test socket.
package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/archive"
	"github.com/joshsymonds/orrery/internal/help"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/shell"
	"github.com/joshsymonds/orrery/internal/tmuxconf"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

const (
	binDirEnv       = "ORRERY_TEST_BIN_DIR"
	noConfig        = "/dev/null"
	exampleQuestion = "example question? yes or no"
	exampleHookJSON = `{"event":"example"}`
	exampleContext  = "http://example.invalid/ctx/example-context"
	sentinelKey     = "z"
	waitLimit       = 10 * time.Second
	pollEvery       = 10 * time.Millisecond
)

func TestMain(m *testing.M) {
	os.Exit(runWithBinaries(m))
}

// runWithBinaries builds orrery and orreryd into a fresh directory, points the tests at it, and runs them.
func runWithBinaries(m *testing.M) int {
	dir, err := os.MkdirTemp("", "orrery-bin-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "make binary directory: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	for _, name := range []string{"orrery", "orreryd"} {
		out, buildErr := exec.Command("go", "build", "-o", filepath.Join(dir, name), "../../cmd/"+name).CombinedOutput()
		if buildErr != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", name, buildErr, out)
			return 1
		}
	}
	if err = os.Setenv(binDirEnv, dir); err != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", binDirEnv, err)
		return 1
	}
	return m.Run()
}

// outcome is what one orrery run printed and how it exited.
type outcome struct {
	stdout string
	stderr string
	code   int
}

// runOrrery runs the built orrery with args, stdin, and env added to the test's environment.
func runOrrery(t *testing.T, stdin string, env []string, args ...string) outcome {
	t.Helper()
	return runCommand(t, "", "orrery", stdin, append(os.Environ(), env...), args...)
}

// runCommand runs the built binary name with args in dir ("" keeps the test's directory) and env, which is the
// whole environment, and returns what it printed and how it exited.
func runCommand(t *testing.T, dir, name, stdin string, env []string, args ...string) outcome {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), filepath.Join(os.Getenv(binDirEnv), name), args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("run %s %v: %v", name, args, err)
	}
	code := 0
	if exitErr != nil {
		code = exitErr.ExitCode()
	}
	return outcome{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// scrubbedEnv returns the test's environment without the variables orreryd and the hook read from a developer's
// shell, plus extra. A test that starts a daemon or a hook passes every value it relies on in extra.
func scrubbedEnv(extra ...string) []string {
	var env []string
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "ORRERY_") || strings.HasPrefix(entry, "ANTHROPIC_BASE_URL=") ||
			strings.HasPrefix(entry, "TMUX=") || strings.HasPrefix(entry, "TMUX_PANE=") ||
			strings.HasPrefix(entry, "XDG_RUNTIME_DIR=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, extra...)
}

// orreryProcess is one orreryd started by a test. done closes once the process has exited, and waitErr is set
// before done closes.
type orreryProcess struct {
	cmd     *exec.Cmd
	stderr  bytes.Buffer
	done    chan struct{}
	waitErr error
}

// startOrreryd starts the built orreryd with args and env and kills it at cleanup if it is still running.
func startOrreryd(t *testing.T, env []string, args ...string) *orreryProcess {
	t.Helper()
	p := &orreryProcess{
		cmd:  exec.Command(filepath.Join(os.Getenv(binDirEnv), "orreryd"), args...),
		done: make(chan struct{}),
	}
	p.cmd.Env = env
	p.cmd.Stderr = &p.stderr
	if err := p.cmd.Start(); err != nil {
		t.Fatalf("start orreryd %v: %v", args, err)
	}
	go func() {
		p.waitErr = p.cmd.Wait()
		close(p.done)
	}()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	})
	return p
}

// waitForListener polls socket until a client connects, failing the test if orreryd exits first or the
// socket never accepts within waitLimit.
func (p *orreryProcess) waitForListener(t *testing.T, socket string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		conn, err := net.Dial("unix", socket)
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				t.Fatalf("close probe connection: %v", closeErr)
			}
			return
		}
		select {
		case <-p.done:
			t.Fatalf("orreryd exited before listening on %s; stderr %q", socket, p.stderr.String())
		case <-ctx.Done():
			t.Fatalf("orreryd never listened on %s: %v", socket, err)
		case <-ticker.C:
		}
	}
}

// stop sends SIGTERM and returns the exit outcome, failing the test if orreryd is still running after waitLimit.
func (p *orreryProcess) stop(t *testing.T) outcome {
	t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal orreryd: %v", err)
	}
	select {
	case <-p.done:
	case <-time.After(waitLimit):
		t.Fatal("orreryd did not exit within the wait limit after SIGTERM")
	}
	code := 0
	var exitErr *exec.ExitError
	if errors.As(p.waitErr, &exitErr) {
		code = exitErr.ExitCode()
	} else if p.waitErr != nil {
		t.Fatalf("wait orreryd: %v", p.waitErr)
	}
	return outcome{stderr: p.stderr.String(), code: code}
}

// wantExit fails the test unless out exited with code, showing both streams.
func wantExit(t *testing.T, out outcome, code int) {
	t.Helper()
	if out.code != code {
		t.Fatalf("exit %d, want %d; stdout %q stderr %q", out.code, code, out.stdout, out.stderr)
	}
}

// newServerName returns a name no other test's tmux server has.
func newServerName(t *testing.T) string {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	return fmt.Sprintf("orrery-integration-%d-%x", os.Getpid(), suffix)
}

// newServer returns a client for a unique private tmux server, killed at cleanup.
func newServer(t *testing.T) (*tmuxctl.Client, string) {
	t.Helper()
	name := newServerName(t)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		groups := paneGroups(t, client)
		if _, killErr := client.Run(context.Background(), "kill-server"); killErr != nil {
			t.Errorf("kill-server: %v", killErr)
		}
		waitGroupsGone(t, groups)
	})
	return client, name
}

// paneExitLimit bounds how long a pane's process group may outlive kill-server before it is killed.
const paneExitLimit = 2 * time.Second

// paneGroups returns the process group of each pane on client's server. A pane's command leads its own group, so
// the group ID is the pane's pid.
func paneGroups(t *testing.T, client *tmuxctl.Client) []int {
	t.Helper()
	out, err := client.Run(context.Background(), "list-panes", "-a", "-F", "#{pane_pid}")
	if err != nil {
		t.Errorf("list-panes: %v", err)
		return nil
	}
	var groups []int
	for _, field := range strings.Fields(out) {
		pgid, parseErr := strconv.Atoi(field)
		if parseErr != nil {
			t.Errorf("pane_pid %q: %v", field, parseErr)
			continue
		}
		// A pane with no process reports 0, and kill(-0) would signal the test's own group.
		if pgid > 1 {
			groups = append(groups, pgid)
		}
	}
	return groups
}

// waitGroupsGone waits after kill-server until no process is left in each of groups. A group still alive after
// paneExitLimit is SIGKILLed, so that no pane process can write into a TempDir after its test returns.
func waitGroupsGone(t *testing.T, groups []int) {
	t.Helper()
	for _, pgid := range groups {
		if waitGroupGone(pgid, paneExitLimit) {
			continue
		}
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("kill process group %d: %v", pgid, err)
		}
		if !waitGroupGone(pgid, paneExitLimit) {
			t.Errorf("process group %d outlives kill-server", pgid)
		}
	}
}

// waitGroupGone reports whether process group pgid has no process left within limit.
func waitGroupGone(pgid int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for {
		if errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// startPane starts detached session s1 running command and returns the ID of its only pane.
func startPane(t *testing.T, client *tmuxctl.Client, command string) string {
	t.Helper()
	if _, err := client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "s1", "-c", t.TempDir(),
		command); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", "s1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	return strings.TrimSpace(out)
}

// splitPane adds a detached pane running command to the window of pane and returns its ID.
func splitPane(t *testing.T, client *tmuxctl.Client, pane, command string) string {
	t.Helper()
	out, err := client.Run(t.Context(), "split-window", "-d", "-P", "-F", "#{pane_id}", "-t", pane, command)
	if err != nil {
		t.Fatalf("split-window: %v", err)
	}
	return strings.TrimSpace(out)
}

// screen returns the captured text of pane.
func screen(t *testing.T, client *tmuxctl.Client, pane string) string {
	t.Helper()
	out, err := client.Run(t.Context(), "capture-pane", "-p", "-J", "-t", pane)
	if err != nil {
		t.Fatalf("capture-pane %s: %v", pane, err)
	}
	return out
}

// waitForScreen polls pane until its screen shows want, failing the test after waitLimit.
func waitForScreen(t *testing.T, client *tmuxctl.Client, pane, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for !strings.Contains(screen(t, client, pane), want) {
		select {
		case <-ctx.Done():
			t.Fatalf("pane %s never showed %q; screen:\n%s", pane, want, screen(t, client, pane))
		case <-ticker.C:
		}
	}
}

// assertNothingTyped sends the sentinel key and waits for it to echo. Keys reach the pane in order, so any
// refused key sent before the sentinel would already be on screen; the check then finds no typed "1".
func assertNothingTyped(t *testing.T, client *tmuxctl.Client, name, pane string) {
	t.Helper()
	wantExit(t, runOrrery(t, "", nil, "-L", name, "send", "--pane", pane, "--keys", sentinelKey), 0)
	waitForScreen(t, client, pane, sentinelKey)
	if got := screen(t, client, pane); strings.Contains(got, "1") {
		t.Fatalf("pane %s shows a typed 1:\n%s", pane, got)
	}
}

func TestHookForwardsFrameSilently(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "orreryd.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	frames := make(chan []byte, 1)
	failures := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			failures <- acceptErr
			return
		}
		defer conn.Close()
		frame, readErr := io.ReadAll(conn)
		if readErr != nil {
			failures <- readErr
			return
		}
		frames <- frame
	}()

	out := runOrrery(t, exampleHookJSON, []string{"TMUX_PANE=%7"}, "--socket", socket, "hook")
	wantExit(t, out, 0)
	if out.stdout != "" || out.stderr != "" {
		t.Fatalf("hook printed stdout %q stderr %q, want nothing", out.stdout, out.stderr)
	}

	select {
	case frame := <-frames:
		var got protocol.HookFrame
		if err = json.Unmarshal(frame, &got); err != nil {
			t.Fatalf("decode frame %q: %v", frame, err)
		}
		if got.TmuxPane != "%7" || string(got.Hook) != exampleHookJSON {
			t.Fatalf("frame pane %q hook %s, want pane %%7 hook %s", got.TmuxPane, got.Hook, exampleHookJSON)
		}
	case err = <-failures:
		t.Fatalf("daemon socket: %v", err)
	case <-time.After(waitLimit):
		t.Fatal("no frame reached the daemon socket")
	}
}

func TestHookAbsentSocketIsSilentWithJunkArgs(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent.sock")
	out := runOrrery(t, exampleHookJSON, nil, "--socket", absent, "hook", "--junk", "-x", "extra")
	wantExit(t, out, 0)
	if out.stdout != "" || out.stderr != "" {
		t.Fatalf("hook printed stdout %q stderr %q, want nothing", out.stdout, out.stderr)
	}
}

func TestHookExitsSilentlyWhenGlobalFlagsFail(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "absent.sock")
	cases := []struct {
		name string
		args []string
	}{
		{name: "unknown flag before hook", args: []string{"--bogus", "hook"}},
		{name: "-L and -S together before hook", args: []string{
			"-L", "orrery-integration-unused", "-S", "/dev/null", "hook",
		}},
		{name: "flag value consumes hook", args: []string{"-L", "hook"}},
		{name: "socket twice and unknown flag before hook", args: []string{
			"--socket", socket, "--socket", socket, "--bogus", "hook", "--junk", "extra",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := runOrrery(t, exampleHookJSON, nil, tc.args...)
			wantExit(t, out, 0)
			if out.stdout != "" || out.stderr != "" {
				t.Fatalf("hook printed stdout %q stderr %q, want nothing", out.stdout, out.stderr)
			}
		})
	}
}

func TestListJSONPrintsPanes(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")

	out := runOrrery(t, "", nil, "-L", name, "list", "--json")
	wantExit(t, out, 0)
	if out.stderr != "" {
		t.Fatalf("list wrote stderr %q", out.stderr)
	}
	var panes []protocol.Pane
	if err := json.Unmarshal([]byte(out.stdout), &panes); err != nil {
		t.Fatalf("decode list output %q: %v", out.stdout, err)
	}
	if len(panes) != 1 || panes[0].ID != pane {
		t.Fatalf("panes %+v, want exactly pane %s", panes, pane)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	if panes[0].Host != host {
		t.Fatalf("pane host %q, want %q", panes[0].Host, host)
	}
	if panes[0].Session != "s1" {
		t.Fatalf("pane session %q, want s1", panes[0].Session)
	}
}

func TestSeenSetsOption(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")

	before := time.Now().Unix()
	wantExit(t, runOrrery(t, "", nil, "-L", name, "seen", "--pane", pane), 0)
	after := time.Now().Unix()

	got, ok, err := client.PaneOption(t.Context(), pane, protocol.OptionSeen)
	if err != nil {
		t.Fatalf("read %s: %v", protocol.OptionSeen, err)
	}
	if !ok {
		t.Fatalf("%s is unset after seen", protocol.OptionSeen)
	}
	seen, err := strconv.ParseInt(got, 10, 64)
	if err != nil {
		t.Fatalf("%s = %q, not a unix time: %v", protocol.OptionSeen, got, err)
	}
	if seen < before || seen > after {
		t.Fatalf("%s = %d, want between %d and %d", protocol.OptionSeen, seen, before, after)
	}
}

func TestSendRefusesSendFlagsAfterKeys(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "cat")

	refused := [][]string{
		{"--expect-question", "0000"},
		{"-expect-question=0000"},
		{"--text=example"},
		{"-text"},
		{"--keys"},
		{"-pane=" + pane},
	}
	for _, extra := range refused {
		args := append([]string{"-L", name, "send", "--pane", pane, "--keys", "1"}, extra...)
		out := runOrrery(t, "", nil, args...)
		wantExit(t, out, 2)
		if !strings.HasPrefix(out.stderr, "orrery: ") {
			t.Fatalf("args %v: stderr %q, want orrery: prefix", args, out.stderr)
		}
	}
	assertNothingTyped(t, client, name, pane)
}

func TestSendGuardedKeysRequireMatchingQuestion(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "printf '"+exampleQuestion+"\\n'; exec cat")
	if err := client.SetPaneOption(t.Context(), pane, protocol.OptionQuestion, exampleQuestion); err != nil {
		t.Fatalf("set question: %v", err)
	}
	waitForScreen(t, client, pane, exampleQuestion)

	wrong := protocol.QuestionHash("example other question?")
	out := runOrrery(t, "", nil, "-L", name, "send", "--pane", pane, "--expect-question", wrong, "--keys", "1")
	wantExit(t, out, 3)
	if out.stdout != "" {
		t.Fatalf("refused send printed stdout %q", out.stdout)
	}
	assertNothingTyped(t, client, name, pane)

	right := protocol.QuestionHash(exampleQuestion)
	wantExit(t, runOrrery(t, "", nil, "-L", name, "send", "--pane", pane, "--expect-question", right,
		"--keys", "1"), 0)
	waitForScreen(t, client, pane, "1")
}

func TestDebugSummarizeWritesSummaryAndLabel(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")

	dir := t.TempDir()
	transcript := filepath.Join(dir, "transcript.jsonl")
	transcriptLines := `{"type":"user","message":{"role":"user","content":"example prompt"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"example reply"}]}}` + "\n"
	if err := os.WriteFile(transcript, []byte(transcriptLines), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	fakeClaude := filepath.Join(dir, "fake-claude.sh")
	script := "#!/bin/sh\ncat >/dev/null\nprintf 'summary: example session in progress\\nlabel: example task name\\n'\n"
	if err := os.WriteFile(fakeClaude, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	out := runOrrery(t, "", []string{"ANTHROPIC_BASE_URL=" + exampleContext},
		"-L", name, "debug", "summarize", "--pane", pane, "--transcript", transcript, "--claude", fakeClaude)
	wantExit(t, out, 0)
	if out.stdout != "" || out.stderr != "" {
		t.Fatalf("debug summarize printed stdout %q stderr %q, want nothing", out.stdout, out.stderr)
	}

	for option, want := range map[string]string{
		protocol.OptionSummary: "example session in progress",
		protocol.OptionLabel:   "example task name",
	} {
		got, ok, err := client.PaneOption(t.Context(), pane, option)
		if err != nil {
			t.Fatalf("read %s: %v", option, err)
		}
		if !ok || got != want {
			t.Fatalf("%s = %q (set %t), want %q", option, got, ok, want)
		}
	}
}

func TestHookExitSilenceIsLimitedToHookCommands(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "bad global flag before list", args: []string{"--bogus", "list", "--json"}},
		{name: "unknown subcommand naming hook", args: []string{"bogus", "hook"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := runOrrery(t, "", nil, tc.args...)
			wantExit(t, out, 2)
			if !strings.HasPrefix(out.stderr, "orrery: ") {
				t.Fatalf("stderr %q, want orrery: prefix", out.stderr)
			}
		})
	}
}

func TestUsageAndFailureExitCodes(t *testing.T) {
	// No server is started here: every case exits before any tmux call.
	const name = "orrery-integration-unused"
	cases := []struct {
		name string
		args []string
		code int
	}{
		{name: "no subcommand", args: []string{}, code: 2},
		{name: "unknown subcommand", args: []string{"bogus"}, code: 2},
		{name: "both -L and -S", args: []string{"-L", name, "-S", "/dev/null", "list", "--json"}, code: 2},
		{name: "debug without subcommand", args: []string{"debug"}, code: 2},
		{name: "debug summarize without flags", args: []string{"debug", "summarize"}, code: 2},
		{name: "list without --json is a runtime error", args: []string{"-L", name, "list"}, code: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := runOrrery(t, "", nil, tc.args...)
			wantExit(t, out, tc.code)
			if !strings.HasPrefix(out.stderr, "orrery: ") {
				t.Fatalf("stderr %q, want orrery: prefix", out.stderr)
			}
		})
	}
}

// Synthetic values for the end-to-end test. The patchbay context and the topic are invented.
const (
	exampleContextName = "example-context"
	examplePatchbayURL = "https://patchbay.example.com/ctx/" + exampleContextName
	exampleModel       = "example-model"
	exampleTopic       = "demo-topic"
	exampleProjectName = "example-project"
	exampleTitle       = exampleProjectName + " · s1:0"
	exampleSummary     = "example summary"
	exampleLastMessage = "example last reply"
	exampleLabel       = "example label"
	exampleFollowUp    = "example follow-up reply"
	exampleExchanges   = 8 // exchanges in the transcript whose session gets its label
	labelPrompt        = "label: <3 to 4 words"
	exampleTranscript  = `{"type":"user","message":{"role":"user","content":"example prompt"}}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"example reply"}]}}` + "\n"
	// fakeClaudeScript stands in for the claude binary. Each run records its argv, the summarizer environment and
	// its stdin under calls/run.*, marks itself done, and replies with the summary line.
	fakeClaudeScript = `#!/bin/sh
D=$(dirname "$0")
mkdir -p "$D/calls"
R=$(mktemp -d "$D/calls/run.XXXXXX")
printf '%s\n' "$@" > "$R/args"
env | grep -E '^(ANTHROPIC_BASE_URL|ORRERY_SUMMARIZER)=' > "$R/env"
cat > "$R/stdin"
printf 'summary: example summary\n'
: > "$R/done"
`
	// slowFakeClaudeScript is fakeClaudeScript after a pause, replying with a label line as well as the summary.
	slowFakeClaudeScript = `#!/bin/sh
D=$(dirname "$0")
mkdir -p "$D/calls"
R=$(mktemp -d "$D/calls/run.XXXXXX")
printf '%s\n' "$@" > "$R/args"
env | grep -E '^(ANTHROPIC_BASE_URL|ORRERY_SUMMARIZER)=' > "$R/env"
cat > "$R/stdin"
sleep 1
printf 'summary: example summary\nlabel: example label\n'
: > "$R/done"
`
)

// notice is one ntfy request the fake server received.
type notice struct {
	priority string
	title    string
	body     string
}

// fakeNtfy starts an httptest ntfy server, returns its topic URL, and delivers each notification on the channel.
func fakeNtfy(t *testing.T) (string, chan notice) {
	t.Helper()
	notices := make(chan notice, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read ntfy body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		notices <- notice{priority: req.Header.Get("Priority"), title: req.Header.Get("Title"), body: string(body)}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/" + exampleTopic, notices
}

// nextNotice returns the next notification the fake ntfy server received, failing the test after waitLimit.
func nextNotice(t *testing.T, notices chan notice) notice {
	t.Helper()
	select {
	case got := <-notices:
		return got
	case <-time.After(waitLimit):
		t.Fatal("no ntfy notification within the wait limit")
	}
	return notice{}
}

// readOption returns the pane-scoped option name on pane, or "" when it is unset.
func readOption(t *testing.T, client *tmuxctl.Client, pane, name string) string {
	t.Helper()
	got, _, err := client.PaneOption(t.Context(), pane, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return got
}

// waitForOption polls pane until option reads want, failing the test after waitLimit.
func waitForOption(t *testing.T, client *tmuxctl.Client, pane, name, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		got := readOption(t, client, pane, name)
		if got == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("%s = %q, want %q", name, got, want)
		case <-ticker.C:
		}
	}
}

// finishedRuns returns the fake claude run directories under dir that have marked themselves done.
func finishedRuns(t *testing.T, dir string) []string {
	t.Helper()
	runs, err := filepath.Glob(filepath.Join(dir, "calls", "run.*"))
	if err != nil {
		t.Fatalf("glob runs: %v", err)
	}
	var done []string
	for _, run := range runs {
		if _, statErr := os.Stat(filepath.Join(run, "done")); statErr == nil {
			done = append(done, run)
		}
	}
	return done
}

// waitForRunWithStdin polls until a finished fake claude run received stdin containing text, and returns that run's
// directory.
func waitForRunWithStdin(t *testing.T, dir, text string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		for _, run := range finishedRuns(t, dir) {
			stdin, err := os.ReadFile(filepath.Join(run, "stdin"))
			if err != nil {
				t.Fatalf("read run stdin: %v", err)
			}
			if strings.Contains(string(stdin), text) {
				return run
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("no finished claude run received %q", text)
		case <-ticker.C:
		}
	}
}

// waitForFinishedRuns polls until n fake claude runs under dir have marked themselves done, failing the test after
// waitLimit.
func waitForFinishedRuns(t *testing.T, dir string, n int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		if len(finishedRuns(t, dir)) >= n {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("fewer than %d claude runs finished", n)
		case <-ticker.C:
		}
	}
}

// readLines returns the lines of the file at path.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// hasPair reports whether args holds flag immediately followed by value.
func hasPair(args []string, flag, value string) bool {
	for i := range len(args) - 1 {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

// assertSummaryRun fails unless the fake claude run got --model exampleModel, the session's base URL, and the
// summarizer sentinel.
func assertSummaryRun(t *testing.T, run string) {
	t.Helper()
	args := readLines(t, filepath.Join(run, "args"))
	if !hasPair(args, "--model", exampleModel) {
		t.Fatalf("claude argv %q, want --model %s", args, exampleModel)
	}
	env := readLines(t, filepath.Join(run, "env"))
	for _, want := range []string{
		"ANTHROPIC_BASE_URL=" + examplePatchbayURL,
		protocol.EnvSummarizer + "=" + protocol.SummarizerValue,
	} {
		if !slices.Contains(env, want) {
			t.Fatalf("claude environment %q lacks %s", env, want)
		}
	}
}

// sendHook runs orrery hook in project for the session in pane, with hook as its JSON stdin and extra added to
// the environment.
func sendHook(t *testing.T, socket, pane, project string, hook map[string]any, extra ...string) {
	t.Helper()
	payload, err := json.Marshal(hook)
	if err != nil {
		t.Fatalf("encode hook: %v", err)
	}
	env := scrubbedEnv(append([]string{"TMUX_PANE=" + pane, "ANTHROPIC_BASE_URL=" + examplePatchbayURL}, extra...)...)
	wantExit(t, runCommand(t, project, "orrery", string(payload), env, "--socket", socket, "hook"), 0)
}

func TestOrrerydDrivesHooksEndToEnd(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")

	dir := t.TempDir()
	fakeClaude := filepath.Join(dir, "fake-claude.sh")
	if err := os.WriteFile(fakeClaude, []byte(fakeClaudeScript), 0o700); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	ntfyURL, notices := fakeNtfy(t)
	urlFile := filepath.Join(dir, "ntfy-url")
	if err := os.WriteFile(urlFile, []byte(ntfyURL+"\n"), 0o600); err != nil {
		t.Fatalf("write ntfy URL file: %v", err)
	}
	project := filepath.Join(dir, exampleProjectName)
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatalf("make project: %v", err)
	}
	transcript := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte(exampleTranscript), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	socket := filepath.Join(dir, "orreryd.sock")

	daemon := startOrreryd(t, scrubbedEnv("ORRERY_NTFY_URL_FILE="+urlFile), "-L", name, "--socket", socket,
		"--claude", fakeClaude, "--summary-model", exampleContextName+"="+exampleModel)
	daemon.waitForListener(t, socket)

	sendHook(t, socket, pane, project, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"transcript_path": transcript,
	})
	waitForOption(t, client, pane, protocol.OptionState, string(protocol.StateWorking))
	waitForOption(t, client, pane, protocol.OptionSummary, exampleSummary)
	runs := finishedRuns(t, dir)
	if len(runs) != 1 {
		t.Fatalf("finished claude runs %v, want exactly one", runs)
	}
	assertSummaryRun(t, runs[0])

	sendHook(t, socket, pane, project, map[string]any{
		"hook_event_name": "PermissionRequest",
		"tool_name":       "example-tool",
		"tool_input":      map[string]any{"command": "example"},
	})
	waiting := nextNotice(t, notices)
	question := readOption(t, client, pane, protocol.OptionQuestion)
	if question == "" || waiting != (notice{priority: "5", title: exampleTitle, body: question}) {
		t.Fatalf("waiting notice %+v, want priority 5, title %q, body %q", waiting, exampleTitle, question)
	}

	sendHook(t, socket, pane, project, map[string]any{
		"hook_event_name":        "Stop",
		"transcript_path":        transcript,
		"last_assistant_message": exampleLastMessage,
	})
	done := nextNotice(t, notices)
	if done != (notice{priority: "4", title: exampleTitle, body: exampleSummary}) {
		t.Fatalf("done notice %+v, want priority 4, title %q, body %q", done, exampleTitle, exampleSummary)
	}
	waitForRunWithStdin(t, dir, exampleLastMessage)

	// The summarizer's own hooks change nothing: its PermissionRequest must not reach the daemon. It goes to the
	// pane that is done, so a leaked one would notify from that pane. A sentinel PermissionRequest goes to a second
	// pane, which then notifies exactly once. The pane's waiting state marks the sentinel applied. Connections are
	// accepted in order, so the summarizer's connection was accepted before it and Serve awaits it at shutdown.
	sentinelPane := splitPane(t, client, pane, "sleep 300")
	sendHook(t, socket, pane, project, map[string]any{
		"hook_event_name": "PermissionRequest",
		"tool_name":       "example-summarizer-tool",
		"tool_input":      map[string]any{"command": "example"},
	}, protocol.EnvSummarizer+"="+protocol.SummarizerValue)
	sendHook(t, socket, sentinelPane, project, map[string]any{
		"hook_event_name": "PermissionRequest",
		"tool_name":       "example-sentinel-tool",
		"tool_input":      map[string]any{"command": "example"},
	})
	waitForOption(t, client, sentinelPane, protocol.OptionState, string(protocol.StateWaiting))

	// Every notice is delivered before its handler responds, and orreryd awaits every notification before it exits.
	// After a clean shutdown the fake holds every notice the run produced, whatever the order of their bodies.
	wantExit(t, daemon.stop(t), 0)
	var got []notice
	for drained := false; !drained; {
		select {
		case n := <-notices:
			got = append(got, n)
		default:
			drained = true
		}
	}
	if len(got) != 1 || got[0].priority != "5" || got[0].title != exampleTitle ||
		!strings.Contains(got[0].body, "example-sentinel-tool") {
		t.Fatalf("notices after the summarizer and sentinel hooks %+v, want exactly the sentinel question", got)
	}
}

// writeFile writes content to path with perm, failing the test on error.
func writeFile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// startSummaryDaemon starts orreryd on a socket in a fresh directory, with script as the summarizer's claude binary,
// and waits for its listener. It returns the daemon, its socket, and the directory that holds the fake claude's runs.
func startSummaryDaemon(t *testing.T, server, script string) (*orreryProcess, string, string) {
	t.Helper()
	dir := t.TempDir()
	fakeClaude := filepath.Join(dir, "fake-claude.sh")
	writeFile(t, fakeClaude, script, 0o700)
	socket := filepath.Join(dir, "orreryd.sock")
	daemon := startOrreryd(t, scrubbedEnv(), "-L", server, "--socket", socket,
		"--claude", fakeClaude, "--summary-model", exampleContextName+"="+exampleModel)
	daemon.waitForListener(t, socket)
	return daemon, socket, dir
}

func TestOrrerydForgetsPaneOnSessionEnd(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	daemon, socket, dir := startSummaryDaemon(t, name, slowFakeClaudeScript)
	transcript := filepath.Join(dir, "transcript.jsonl")
	writeFile(t, transcript, exampleTranscript, 0o600)

	sendHook(t, socket, pane, dir, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"transcript_path": transcript,
	})
	waitForOption(t, client, pane, protocol.OptionState, string(protocol.StateWorking))
	sendHook(t, socket, pane, dir, map[string]any{
		"hook_event_name":        "Stop",
		"transcript_path":        transcript,
		"last_assistant_message": exampleLastMessage,
	})
	waitForOption(t, client, pane, protocol.OptionState, string(protocol.StateDone))
	sendHook(t, socket, pane, dir, map[string]any{"hook_event_name": "SessionEnd"})
	waitForOption(t, client, pane, protocol.OptionState, "")

	// The run in flight at SessionEnd finishes, then a late summary write has this window to land before the daemon
	// stops. A stop cancels any run still in flight, so it must come after the window.
	const lateWriteWindow = 500 * time.Millisecond
	waitForFinishedRuns(t, dir, 1)
	<-time.After(lateWriteWindow)
	wantExit(t, daemon.stop(t), 0)
	for _, option := range []string{protocol.OptionSummary, protocol.OptionLabel} {
		if got := readOption(t, client, pane, option); got != "" {
			t.Fatalf("%s = %q after SessionEnd, want unset", option, got)
		}
	}
}

func TestOrrerydAsksLabelAfterSessionEnd(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	daemon, socket, dir := startSummaryDaemon(t, name, slowFakeClaudeScript)
	long := filepath.Join(dir, "long.jsonl")
	writeFile(t, long, strings.Repeat(exampleTranscript, exampleExchanges), 0o600)
	short := filepath.Join(dir, "short.jsonl")
	writeFile(t, short, exampleTranscript, 0o600)

	// The first session is labeled at exampleExchanges exchanges.
	sendHook(t, socket, pane, dir, map[string]any{
		"hook_event_name":        "Stop",
		"transcript_path":        long,
		"last_assistant_message": exampleLastMessage,
	})
	waitForOption(t, client, pane, protocol.OptionLabel, exampleLabel)
	sendHook(t, socket, pane, dir, map[string]any{"hook_event_name": "SessionEnd"})
	waitForOption(t, client, pane, protocol.OptionLabel, "")

	// The next session has one exchange, far below the first session's label count, yet its first run asks for a label.
	sendHook(t, socket, pane, dir, map[string]any{
		"hook_event_name":        "Stop",
		"transcript_path":        short,
		"last_assistant_message": exampleFollowUp,
	})
	run := waitForRunWithStdin(t, dir, exampleFollowUp)
	stdin, err := os.ReadFile(filepath.Join(run, "stdin"))
	if err != nil {
		t.Fatalf("read run stdin: %v", err)
	}
	if !strings.Contains(string(stdin), labelPrompt) {
		t.Fatalf("next session's run stdin lacks %q; stdin %q", labelPrompt, stdin)
	}
	wantExit(t, daemon.stop(t), 0)
}

// waitUntil polls ok until it reports true, failing the test after waitLimit.
func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), waitLimit)
	defer cancel()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for !ok() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-ticker.C:
		}
	}
}

// viewer is a tmux client attached to session s1 of a private server. The client runs under script in outerPane
// on a second private server, so keys sent to outerPane reach the client.
type viewer struct {
	inner     *tmuxctl.Client
	outer     *tmuxctl.Client
	outerPane string
}

// attachViewer attaches a client to session s1 of the server named innerName, and waits until that server lists it.
func attachViewer(t *testing.T, inner *tmuxctl.Client, innerName string) viewer {
	t.Helper()
	outer, _ := newServer(t)
	attach := fmt.Sprintf("script -qfc 'env -u TMUX tmux -L %s attach -t s1' /dev/null", innerName)
	if _, err := outer.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "o1",
		"-c", t.TempDir(), attach); err != nil {
		t.Fatalf("outer new-session: %v", err)
	}
	pane, err := outer.Run(t.Context(), "list-panes", "-t", "o1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("outer list-panes: %v", err)
	}
	waitUntil(t, "client attached to s1", func() bool {
		out, listErr := inner.Run(t.Context(), "list-clients", "-F", "#{client_name}")
		if listErr != nil {
			t.Fatalf("list-clients: %v", listErr)
		}
		return strings.TrimSpace(out) != ""
	})
	return viewer{inner: inner, outer: outer, outerPane: strings.TrimSpace(pane)}
}

// clientActivity returns the latest client_activity among the clients of the server behind client, or 0 when none.
func clientActivity(t *testing.T, client *tmuxctl.Client) int64 {
	t.Helper()
	out, err := client.Run(t.Context(), "list-clients", "-F", "#{client_activity}")
	if err != nil {
		t.Fatalf("list-clients: %v", err)
	}
	var latest int64
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n, parseErr := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
		if parseErr != nil {
			t.Fatalf("client_activity %q: %v", line, parseErr)
		}
		latest = max(latest, n)
	}
	return latest
}

// typeKey types one key through the outer pane and waits until the server records the client's input. Activity has
// one-second resolution, so the clock must first pass the client's last activity for the new one to differ.
func (v viewer) typeKey(t *testing.T) {
	t.Helper()
	baseline := clientActivity(t, v.inner)
	waitUntil(t, "wall clock to pass the client's last activity", func() bool {
		return time.Now().Unix() > baseline
	})
	if _, err := v.outer.Run(t.Context(), "send-keys", "-t", v.outerPane, "-l", "x"); err != nil {
		t.Fatalf("send-keys to outer pane: %v", err)
	}
	waitUntil(t, "client activity to advance after typing", func() bool {
		return clientActivity(t, v.inner) > baseline
	})
}

// TestOrrerydMarksViewedPaneSeenOnDone drives the built orreryd while a client shows window 0 of s1 and has just
// typed. paneA in window 0 is marked seen when it finishes. paneB in window 1, which the client does not show, stays
// unseen when it finishes.
func TestOrrerydMarksViewedPaneSeenOnDone(t *testing.T) {
	inner, name := newServer(t)
	paneA := startPane(t, inner, "sleep 300")
	if _, err := inner.Run(t.Context(), "new-window", "-d", "-t", "s1:1", "sleep 300"); err != nil {
		t.Fatalf("new-window: %v", err)
	}
	out, err := inner.Run(t.Context(), "list-panes", "-t", "s1:1", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes window 1: %v", err)
	}
	paneB := strings.TrimSpace(out)

	view := attachViewer(t, inner, name)
	_, socket, dir := startSummaryDaemon(t, name, fakeClaudeScript)
	transcript := filepath.Join(dir, "transcript.jsonl")
	writeFile(t, transcript, exampleTranscript, 0o600)
	view.typeKey(t)

	prompt := map[string]any{"hook_event_name": "UserPromptSubmit", "transcript_path": transcript}
	stop := map[string]any{
		"hook_event_name":        "Stop",
		"transcript_path":        transcript,
		"last_assistant_message": exampleLastMessage,
	}

	// paneB finishes first. The observer runs under the daemon's lock, and the frames for paneA wait for that lock,
	// so paneA's seen mark cannot land before paneB's observer has returned.
	sendHook(t, socket, paneB, dir, prompt)
	sendHook(t, socket, paneB, dir, stop)
	waitForOption(t, inner, paneB, protocol.OptionState, string(protocol.StateDone))

	sendHook(t, socket, paneA, dir, prompt)
	waitForOption(t, inner, paneA, protocol.OptionState, string(protocol.StateWorking))
	sendHook(t, socket, paneA, dir, stop)
	waitForOption(t, inner, paneA, protocol.OptionState, string(protocol.StateDone))
	waitUntil(t, "seen on the pane the client shows", func() bool {
		return readOption(t, inner, paneA, protocol.OptionSeen) != ""
	})
	if got := readOption(t, inner, paneB, protocol.OptionSeen); got != "" {
		t.Fatalf("%s on the pane in a window the client does not show = %q, want unset", protocol.OptionSeen, got)
	}
}

func TestOrrerydUsageExitCodes(t *testing.T) {
	// Every case exits before any tmux call or socket use, so the socket below is never created.
	socket := filepath.Join(t.TempDir(), "orreryd.sock")
	cases := []struct {
		name string
		args []string
	}{
		{name: "no socket", args: []string{"-L", "orrery-integration-unused"}},
		{name: "summary model without =", args: []string{"--socket", socket, "--summary-model", exampleContextName}},
		{name: "summary model with empty context", args: []string{
			"--socket", socket, "--summary-model", "=" + exampleModel,
		}},
		{name: "summary model with empty model", args: []string{
			"--socket", socket, "--summary-model", exampleContextName + "=",
		}},
		{name: "summary model repeated", args: []string{
			"--socket", socket,
			"--summary-model", exampleContextName + "=" + exampleModel,
			"--summary-model", exampleContextName + "=other-model",
		}},
		{name: "-L and -S", args: []string{
			"--socket", socket, "-L", "orrery-integration-unused", "-S", "/dev/null",
		}},
		{name: "unexpected argument", args: []string{"--socket", socket, "extra"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := runCommand(t, "", "orreryd", "", scrubbedEnv(), tc.args...)
			wantExit(t, out, 2)
			if !strings.HasPrefix(out.stderr, "orreryd: ") {
				t.Fatalf("stderr %q, want orreryd: prefix", out.stderr)
			}
		})
	}
}

// clientRow is one client of a tmux server as list-clients reports it.
type clientRow struct {
	tty     string
	session string
	width   int
	height  int
}

// clientRows lists each client of the server behind client with the session it shows and its size.
func clientRows(t *testing.T, client *tmuxctl.Client) []clientRow {
	t.Helper()
	out, err := client.Run(t.Context(), "list-clients", "-F",
		"#{client_tty} #{client_session} #{client_width} #{client_height}")
	if err != nil {
		t.Fatalf("list-clients: %v", err)
	}
	var rows []clientRow
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			t.Fatalf("list-clients line %q: want 4 fields", line)
		}
		width, widthErr := strconv.Atoi(fields[2])
		if widthErr != nil {
			t.Fatalf("list-clients width in %q: %v", line, widthErr)
		}
		height, heightErr := strconv.Atoi(fields[3])
		if heightErr != nil {
			t.Fatalf("list-clients height in %q: %v", line, heightErr)
		}
		rows = append(rows, clientRow{tty: fields[0], session: fields[1], width: width, height: height})
	}
	return rows
}

// attachClient starts a client of cols by rows on session of the server named name, through script, and returns
// its tty once the server lists it. The client stays attached until cleanup.
func attachClient(t *testing.T, client *tmuxctl.Client, name, session string, cols, rows int) string {
	t.Helper()
	known := knownClients(t, client)
	startScript(t, fmt.Sprintf("stty cols %d rows %d; env -u TMUX tmux -L %s attach -t %s", cols, rows, name, session))
	return waitForNewClient(t, client, session, known)
}

// knownClients returns the ttys of the clients the server behind client lists now.
func knownClients(t *testing.T, client *tmuxctl.Client) map[string]bool {
	t.Helper()
	known := map[string]bool{}
	for _, row := range clientRows(t, client) {
		known[row.tty] = true
	}
	return known
}

// waitForNewClient waits until the server lists a client that is not in known and shows session, and returns its tty.
func waitForNewClient(t *testing.T, client *tmuxctl.Client, session string, known map[string]bool) string {
	t.Helper()
	var tty string
	waitUntil(t, "client attached to "+session, func() bool {
		for _, row := range clientRows(t, client) {
			if !known[row.tty] && row.session == session {
				tty = row.tty
				return true
			}
		}
		return false
	})
	return tty
}

// startScript runs command on a pseudo-terminal through script, with the test's environment minus the tmux
// variables. The process stays until cleanup, which closes its input and waits for it.
func startScript(t *testing.T, command string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "script", "-qfc", command, "/dev/null")
	cmd.Env = scrubbedEnv()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatalf("start client: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
}

// popupError returns an error unless the invoking and other clients still show session, and a client shows the
// popup session name at a size smaller than the invoking client. A popup drawn on the other client is sized from
// that larger client, so it fails the size check.
func popupError(t *testing.T, client *tmuxctl.Client, name, session, invokingTTY, otherTTY string) error {
	t.Helper()
	var invoking, other, popup *clientRow
	rows := clientRows(t, client)
	for i := range rows {
		switch {
		case rows[i].tty == invokingTTY:
			invoking = &rows[i]
		case rows[i].tty == otherTTY:
			other = &rows[i]
		case rows[i].session == name:
			popup = &rows[i]
		}
	}
	if invoking == nil || other == nil {
		return fmt.Errorf("invoking %s or other %s client not listed in %v", invokingTTY, otherTTY, rows)
	}
	if invoking.session != session {
		return fmt.Errorf("invoking client %s moved to session %s", invokingTTY, invoking.session)
	}
	if other.session != session {
		return fmt.Errorf("other client %s moved to session %s", otherTTY, other.session)
	}
	if popup == nil {
		return fmt.Errorf("no client attached to %s in %v", name, rows)
	}
	if popup.width >= invoking.width || popup.height >= invoking.height {
		return fmt.Errorf("popup client is %dx%d, not smaller than invoking client %dx%d",
			popup.width, popup.height, invoking.width, invoking.height)
	}
	return nil
}

func TestShellOpensPopupOnInvokingClient(t *testing.T) {
	// Synthetic directory names. Each holds a character that tmux would expand in a -c argument or a command
	// string if the argument were not escaped.
	dirNames := []string{"example dir", "example#(id)", "example#{session_name}"}
	for _, dirName := range dirNames {
		t.Run(dirName, func(t *testing.T) {
			client, name := newServer(t)
			startPane(t, client, "sleep 300")
			dir := filepath.Join(t.TempDir(), dirName)
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatalf("make directory: %v", err)
			}
			invoking := attachClient(t, client, name, "s1", 80, 24)
			other := attachClient(t, client, name, "s1", 200, 60)

			wantExit(t, runOrrery(t, "", nil, "-L", name, "shell", "--dir", dir, "--client", invoking), 0)

			sessionName := shell.Name(dir)
			if _, err := client.Run(t.Context(), "has-session", "-t", "="+sessionName); err != nil {
				t.Fatalf("shell session %s: %v", sessionName, err)
			}
			got, err := client.Run(t.Context(), "show-options", "-v", "-t", "="+sessionName+":", protocol.OptionDir)
			if err != nil {
				t.Fatalf("read %s: %v", protocol.OptionDir, err)
			}
			if strings.TrimSpace(got) != dir {
				t.Fatalf("%s = %q, want %q", protocol.OptionDir, strings.TrimSpace(got), dir)
			}
			waitUntil(t, "popup on the invoking client", func() bool {
				return popupError(t, client, sessionName, "s1", invoking, other) == nil
			})
		})
	}
}

func TestShellRefusesBadCommandLines(t *testing.T) {
	// Every case exits before any tmux call that needs a server, so the server name below is never started.
	const name = "orrery-integration-unused"
	cases := []struct {
		name string
		args []string
	}{
		{name: "missing --dir", args: []string{"shell", "--client", "/dev/pts/example"}},
		{name: "missing --client", args: []string{"shell", "--dir", "/example/dir"}},
		{name: "stray argument", args: []string{
			"shell", "--dir", "/example/dir", "--client", "/dev/pts/example", "extra",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := runOrrery(t, "", nil, append([]string{"-L", name}, tc.args...)...)
			wantExit(t, out, 2)
			if !strings.Contains(out.stderr, "usage: orrery") ||
				!strings.Contains(out.stderr, "shell --dir DIR --client TTY") {
				t.Fatalf("stderr %q, want usage text listing shell --dir DIR --client TTY", out.stderr)
			}
		})
	}
}

func TestShellRelativeDirExitsOneWithoutSession(t *testing.T) {
	client, name := newServer(t)
	startPane(t, client, "sleep 300")

	out := runOrrery(t, "", nil, "-L", name, "shell", "--dir", "example/relative", "--client", "/dev/pts/example")
	wantExit(t, out, 1)
	if !strings.HasPrefix(out.stderr, "orrery: ") {
		t.Fatalf("stderr %q, want orrery: prefix", out.stderr)
	}
	sessions, err := client.Run(t.Context(), "list-sessions", "-F", "#{session_name}")
	if err != nil {
		t.Fatalf("list-sessions: %v", err)
	}
	for session := range strings.SplitSeq(sessions, "\n") {
		if strings.HasPrefix(session, protocol.ShellSessionPrefix) {
			t.Fatalf("session %q created for a relative directory", session)
		}
	}
}

func TestTmuxConfPrintsPluginForBinaryPath(t *testing.T) {
	// os.Executable resolves symlinks, so the expected path is the resolved path of the built binary.
	exe, err := filepath.EvalSymlinks(filepath.Join(os.Getenv(binDirEnv), "orrery"))
	if err != nil {
		t.Fatalf("resolve orrery binary: %v", err)
	}
	var want bytes.Buffer
	if err = tmuxconf.Print(&want, exe); err != nil {
		t.Fatalf("tmuxconf.Print(%s): %v", exe, err)
	}

	out := runOrrery(t, "", nil, "tmux-conf")
	wantExit(t, out, 0)
	if out.stdout != want.String() {
		t.Fatalf("stdout %q, want tmuxconf.Print output %q", out.stdout, want.String())
	}
	if !strings.Contains(out.stdout, exe+" tree --client") {
		t.Fatalf("stdout %q does not run %s tree --client", out.stdout, exe)
	}
}

func TestTmuxConfPluginSourcesOnPrivateServer(t *testing.T) {
	// tmux binds prefix Space to next-layout by default, so only the plugin's binding names the binary.
	exe, err := filepath.EvalSymlinks(filepath.Join(os.Getenv(binDirEnv), "orrery"))
	if err != nil {
		t.Fatalf("resolve orrery binary: %v", err)
	}
	client, _ := newServer(t)
	startPane(t, client, "sleep 300")

	out := runOrrery(t, "", nil, "tmux-conf")
	wantExit(t, out, 0)
	plugin := filepath.Join(t.TempDir(), "orrery.tmux")
	writeFile(t, plugin, out.stdout, 0o600)
	if _, err = client.Run(t.Context(), "source-file", plugin); err != nil {
		t.Fatalf("source-file %s: %v", plugin, err)
	}

	keys, err := client.Run(t.Context(), "list-keys", "-T", "prefix")
	if err != nil {
		t.Fatalf("list-keys: %v", err)
	}
	for line := range strings.SplitSeq(keys, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "bind-key" && slices.Contains(fields, "prefix") &&
			slices.Contains(fields, "Space") && strings.Contains(line, exe+" tree --client") {
			return
		}
	}
	t.Fatalf("no Space binding running %s tree --client in the prefix table; list-keys %q", exe, keys)
}

func TestTmuxConfRefusesStrayArgument(t *testing.T) {
	// The exit happens before any tmux call, so no server is started.
	out := runOrrery(t, "", nil, "tmux-conf", "extra")
	wantExit(t, out, 2)
	if !strings.Contains(out.stderr, "usage: orrery") || !strings.Contains(out.stderr, "\n  tmux-conf ") {
		t.Fatalf("stderr %q, want usage text listing tmux-conf", out.stderr)
	}
}

// helpGolden returns one of internal/help's goldens, which are rendered with the C-a prefix.
func helpGolden(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "help", "testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return string(body)
}

// prefixServer starts a private server whose global prefix is C-a and returns its name.
func prefixServer(t *testing.T) string {
	t.Helper()
	client, name := newServer(t)
	if _, err := client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", "help"); err != nil {
		t.Fatalf("new-session: %v", err)
	}
	if _, err := client.Run(t.Context(), "set-option", "-g", "prefix", "C-a"); err != nil {
		t.Fatalf("set-option prefix: %v", err)
	}
	return name
}

func TestHelpPrintsCardWithServerPrefix(t *testing.T) {
	name := prefixServer(t)

	out := runOrrery(t, "", nil, "-L", name, "help")
	wantExit(t, out, 0)
	if want := helpGolden(t, "card.golden"); out.stdout != want {
		t.Fatalf("stdout does not match card.golden:\n%s", out.stdout)
	}
	if out.stderr != "" {
		t.Fatalf("stderr %q, want empty", out.stderr)
	}
}

func TestHelpHTMLPrintsPageWithServerPrefix(t *testing.T) {
	name := prefixServer(t)

	out := runOrrery(t, "", nil, "-L", name, "help", "--html")
	wantExit(t, out, 0)
	if want := helpGolden(t, "page.golden"); out.stdout != want {
		t.Fatalf("stdout does not match page.golden:\n%s", out.stdout)
	}
	if out.stderr != "" {
		t.Fatalf("stderr %q, want empty", out.stderr)
	}
}

func TestHelpWithoutServerPrintsCardWithoutPrefix(t *testing.T) {
	// help reads the server and never starts one, so an unstarted server gives a card with no prefix.
	var want bytes.Buffer
	if err := help.Card(&want, ""); err != nil {
		t.Fatalf("help.Card: %v", err)
	}
	name := newServerName(t)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		// The kill stops a server that a regression started. With no server it errors, which is the passing case.
		_, _ = client.Run(context.Background(), "kill-server")
	})

	out := runOrrery(t, "", nil, "-L", name, "help")
	wantExit(t, out, 0)
	if out.stdout != want.String() {
		t.Fatalf("stdout %q, want help.Card with an empty prefix %q", out.stdout, want.String())
	}
	if out.stderr != "" {
		t.Fatalf("stderr %q, want empty", out.stderr)
	}
	if sessions, listErr := client.Run(t.Context(), "list-sessions"); listErr == nil {
		t.Fatalf("help started server %s; list-sessions %q", name, sessions)
	}
}

func TestHelpRefusesExtraArgument(t *testing.T) {
	// The exit happens before any tmux call, so no server is started.
	out := runOrrery(t, "", nil, "help", "extra")
	wantExit(t, out, 2)
	if !strings.Contains(out.stderr, "usage: orrery") || !strings.Contains(out.stderr, "\n  help ") {
		t.Fatalf("stderr %q, want usage text listing help", out.stderr)
	}
}

// fakeTool is a scripted fake from internal/open/testdata installed on a test's PATH. Its hold file, in dir,
// blocks each run while it exists.
type fakeTool struct {
	bin string
	dir string
}

// installFake installs testdata/<src>.sh as name in a fresh directory, printing reply on each run.
func installFake(t *testing.T, src, name, reply string) fakeTool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "open", "testdata", src+".sh"))
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, name)
	if err = os.WriteFile(bin, body, 0o755); err != nil {
		t.Fatalf("install %s: %v", name, err)
	}
	writeFile(t, filepath.Join(dir, "reply"), reply, 0o600)
	return fakeTool{bin: bin, dir: dir}
}

// runs counts the runs the fake has started.
func (f fakeTool) runs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.dir, "calls"))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatalf("read calls of %s: %v", f.bin, err)
	}
	return len(entries)
}

// waitForStarted waits until the fake has started n runs and each has opened its stdin file, the last file a run
// writes before it blocks. A run that is still writing when a test ends would race the removal of the fake's
// directory.
func (f fakeTool) waitForStarted(t *testing.T, n int) {
	t.Helper()
	waitUntil(t, fmt.Sprintf("%d runs of %s to start", n, f.bin), func() bool {
		entries, err := os.ReadDir(filepath.Join(f.dir, "calls"))
		if err != nil {
			return false
		}
		started := 0
		for _, entry := range entries {
			if _, statErr := os.Stat(filepath.Join(f.dir, "calls", entry.Name(), "stdin")); statErr == nil {
				started++
			}
		}
		return started == n
	})
}

// soleRun returns the directory of the one run the fake has started, and fails unless it started exactly one.
func (f fakeTool) soleRun(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.dir, "calls"))
	if err != nil {
		t.Fatalf("read calls of %s: %v", f.bin, err)
	}
	if len(entries) != 1 {
		t.Fatalf("%s ran %d times, want 1", f.bin, len(entries))
	}
	return filepath.Join(f.dir, "calls", entries[0].Name())
}

// setHold makes each run of the fake block while held is true.
func (f fakeTool) setHold(t *testing.T, held bool) {
	t.Helper()
	path := filepath.Join(f.dir, "hold")
	if held {
		writeFile(t, path, "", 0o600)
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release %s: %v", f.bin, err)
	}
}

// newSession starts detached session name in dir running sleep, and returns the ID of its first pane.
func newSession(t *testing.T, client *tmuxctl.Client, name, dir string) string {
	t.Helper()
	if _, err := client.Run(t.Context(), "-f", noConfig, "new-session", "-d", "-s", name, "-c", dir,
		"sleep 300"); err != nil {
		t.Fatalf("new-session %s: %v", name, err)
	}
	out, err := client.Run(t.Context(), "list-panes", "-t", name, "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes %s: %v", name, err)
	}
	return strings.TrimSpace(out)
}

// sendKeys sends keys to pane, typing them literally when literal is set.
func sendKeys(t *testing.T, client *tmuxctl.Client, pane string, literal bool, keys ...string) {
	t.Helper()
	args := []string{"send-keys", "-t", pane}
	if literal {
		args = append(args, "-l")
	}
	if _, err := client.Run(t.Context(), append(args, keys...)...); err != nil {
		t.Fatalf("send-keys %v to %s: %v", keys, pane, err)
	}
}

// clientSession returns the session the client at tty shows.
func clientSession(t *testing.T, client *tmuxctl.Client, tty string) string {
	t.Helper()
	for _, row := range clientRows(t, client) {
		if row.tty == tty {
			return row.session
		}
	}
	t.Fatalf("client %s not listed", tty)
	return ""
}

// waitForCursor polls pane until the row under the cursor shows want, failing the test after waitLimit.
func waitForCursor(t *testing.T, client *tmuxctl.Client, pane, want string) {
	t.Helper()
	waitUntil(t, "cursor row of "+pane+" to show "+want, func() bool {
		for line := range strings.SplitSeq(screen(t, client, pane), "\n") {
			if strings.HasPrefix(line, "> ") {
				return strings.Contains(line, want)
			}
		}
		return false
	})
}

// treeWorld is a private server for one tree test. Session s1 shows the tree on the invoking client. Session s2
// holds the waiting target pane, in targetDir. Session s3 is the session of the other client. The fake claude
// and fake zoxide lead PATH, and HOME is a temp directory.
type treeWorld struct {
	client    *tmuxctl.Client
	invoking  string
	other     string
	target    string
	targetDir string
	tree      string
	claude    fakeTool
}

// setupTree builds a treeWorld and starts orrery tree in a pane of s1 for the invoking client.
func setupTree(t *testing.T) treeWorld {
	t.Helper()
	claude := installFake(t, "fake-claude", "claude", "pane d1\ntask: no\n")
	zoxide := installFake(t, "fake-zoxide", "zoxide", "  9.0 "+filepath.Join(t.TempDir(), "example-project")+"\n")
	t.Setenv("PATH", claude.dir+string(os.PathListSeparator)+zoxide.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())

	client, name := newServer(t)
	newSession(t, client, "s1", t.TempDir())
	invoking := attachClient(t, client, name, "s1", 80, 24)

	targetDir := t.TempDir()
	target := newSession(t, client, "s2", targetDir)
	mustSetState(t, client, target)
	newSession(t, client, "s3", t.TempDir())
	other := attachClient(t, client, name, "s3", 100, 30)

	orrery := filepath.Join(os.Getenv(binDirEnv), "orrery")
	tree := splitPane(t, client, "s1", fmt.Sprintf("%s -L %s tree --client %s", orrery, name, invoking))
	waitForCursor(t, client, tree, "waiting")
	return treeWorld{
		client: client, invoking: invoking, other: other, target: target, targetDir: targetDir, tree: tree,
		claude: claude,
	}
}

// mustSetState marks pane waiting, so that the tree ranks it first.
func mustSetState(t *testing.T, client *tmuxctl.Client, pane string) {
	t.Helper()
	if _, err := client.Run(t.Context(), "set-option", "-p", "-t", pane, protocol.OptionState, "waiting"); err != nil {
		t.Fatalf("set %s on %s: %v", protocol.OptionState, pane, err)
	}
}

func TestTreeEnterSwitchesOnlyInvokingClient(t *testing.T) {
	w := setupTree(t)
	sendKeys(t, w.client, w.tree, false, "Enter")

	waitUntil(t, "invoking client on the target session", func() bool {
		return clientSession(t, w.client, w.invoking) == "s2"
	})
	if got := clientSession(t, w.client, w.other); got != "s3" {
		t.Fatalf("other client shows session %s, want s3", got)
	}
}

func TestTreeNOpensPaneInGroupDirectory(t *testing.T) {
	w := setupTree(t)
	before := sessionNames(t, w.client)
	w.claude.setHold(t, true)

	sendKeys(t, w.client, w.tree, true, "N")
	waitForScreen(t, w.client, w.tree, "text:")
	sendKeys(t, w.client, w.tree, true, "example text")
	sendKeys(t, w.client, w.tree, false, "Enter")
	waitUntil(t, "the intent run to start", func() bool { return w.claude.runs(t) == 1 })
	w.claude.setHold(t, false)
	waitForScreen(t, w.client, w.tree, "> pane "+w.targetDir)
	w.claude.setHold(t, true)

	sendKeys(t, w.client, w.tree, false, "Enter")
	var opened string
	waitUntil(t, "invoking client on a new session", func() bool {
		opened = clientSession(t, w.client, w.invoking)
		return !slices.Contains(before, opened)
	})
	if got := clientSession(t, w.client, w.other); got != "s3" {
		t.Fatalf("other client shows session %s, want s3", got)
	}
	if got := clientSession(t, w.client, w.invoking); got != opened {
		t.Fatalf("invoking client moved again to %s", got)
	}
	want := "=" + opened + ":"
	waitUntil(t, "claude pane in "+w.targetDir, func() bool {
		return paneField(t, w.client, want, "#{pane_current_path}") == w.targetDir
	})
	if got := paneField(t, w.client, want, "#{pane_start_command}"); got != w.claude.bin {
		t.Fatalf("pane runs %q, want the fake claude at %s", got, w.claude.bin)
	}
	if got := paneField(t, w.client, "=s2:", "#{pane_id}"); got != w.target {
		t.Fatalf("target session s2 shows pane %s, want %s", got, w.target)
	}
}

func TestTreeEscInPickerChangesNothing(t *testing.T) {
	w := setupTree(t)
	before := sessionNames(t, w.client)
	w.claude.setHold(t, true)

	sendKeys(t, w.client, w.tree, true, "N")
	waitForScreen(t, w.client, w.tree, "text:")
	sendKeys(t, w.client, w.tree, true, "example text")
	sendKeys(t, w.client, w.tree, false, "Enter")
	waitUntil(t, "the intent run to start", func() bool { return w.claude.runs(t) == 1 })
	w.claude.setHold(t, false)
	waitForScreen(t, w.client, w.tree, "> pane "+w.targetDir)

	sendKeys(t, w.client, w.tree, false, "Escape")
	waitUntil(t, "the tree to exit", func() bool { return !paneListed(t, w.client, w.tree) })
	if got := clientSession(t, w.client, w.invoking); got != "s1" {
		t.Fatalf("invoking client shows session %s, want s1", got)
	}
	if got := clientSession(t, w.client, w.other); got != "s3" {
		t.Fatalf("other client shows session %s, want s3", got)
	}
	if got := sessionNames(t, w.client); !slices.Equal(got, before) {
		t.Fatalf("sessions %v after Esc, want %v", got, before)
	}
	if got := w.claude.runs(t); got != 1 {
		t.Fatalf("fake claude ran %d times, want only the intent run", got)
	}
}

func TestTreeRefusesMissingClient(t *testing.T) {
	out := runOrrery(t, "", nil, "-L", "orrery-integration-unused", "tree")
	wantExit(t, out, 2)
	if !strings.Contains(out.stderr, "usage: orrery") || !strings.Contains(out.stderr, "tree --client TTY") {
		t.Fatalf("stderr %q, want usage text listing tree --client TTY", out.stderr)
	}
}

func TestTreeXArchivesClaudePaneBeforeClose(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	w := setupTree(t)
	transcript := filepath.Join(t.TempDir(), "example-transcript.jsonl")
	writeFile(t, transcript, "{}\n", 0o600)
	if _, err := w.client.Run(t.Context(), "set-option", "-p", "-t", w.target, protocol.OptionClaudeSession,
		"example-claude-session"); err != nil {
		t.Fatalf("set %s on %s: %v", protocol.OptionClaudeSession, w.target, err)
	}
	if _, err := w.client.Run(t.Context(), "set-option", "-p", "-t", w.target, protocol.OptionTranscript,
		transcript); err != nil {
		t.Fatalf("set %s on %s: %v", protocol.OptionTranscript, w.target, err)
	}

	sendKeys(t, w.client, w.tree, true, "X")
	waitForScreen(t, w.client, w.tree, "y/N")
	sendKeys(t, w.client, w.tree, true, "y")
	waitUntil(t, "target pane to close", func() bool { return !paneListed(t, w.client, w.target) })

	records, err := archive.ForDir(filepath.Join(stateHome, "orrery", "archive.jsonl"), w.targetDir)
	if err != nil {
		t.Fatalf("archive.ForDir: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("archive holds %d records for %s, want 1", len(records), w.targetDir)
	}
	if got := records[0]; got.ClaudeSession != "example-claude-session" || got.Transcript != transcript ||
		got.Session != "s2" {
		t.Fatalf("archived record %+v, want claude session example-claude-session, transcript %s, session s2",
			got, transcript)
	}
}

// sessionNames returns the sorted names of the sessions on the server behind client.
func sessionNames(t *testing.T, client *tmuxctl.Client) []string {
	t.Helper()
	out, err := client.Run(t.Context(), "list-sessions", "-F", "#{session_name}")
	if err != nil {
		t.Fatalf("list-sessions: %v", err)
	}
	names := strings.Fields(out)
	slices.Sort(names)
	return names
}

// paneField returns one format value of the active pane of target.
func paneField(t *testing.T, client *tmuxctl.Client, target, format string) string {
	t.Helper()
	out, err := client.Run(t.Context(), "display-message", "-p", "-t", target, format)
	if err != nil {
		t.Fatalf("display-message %s on %s: %v", format, target, err)
	}
	return strings.TrimSpace(out)
}

// paneListed reports whether pane is still on the server.
func paneListed(t *testing.T, client *tmuxctl.Client, pane string) bool {
	t.Helper()
	out, err := client.Run(t.Context(), "list-panes", "-a", "-F", "#{pane_id}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	return slices.Contains(strings.Fields(out), pane)
}

// orreryPath returns the built orrery binary.
func orreryPath() string {
	return filepath.Join(os.Getenv(binDirEnv), "orrery")
}

// installOpenFakes installs a fake claude and a fake zoxide whose reply is zoxideReply, leads PATH with them, and
// points HOME at a temp directory. It returns the fake claude.
func installOpenFakes(t *testing.T, zoxideReply string) fakeTool {
	t.Helper()
	claude := installFake(t, "fake-claude", "claude", "pane d1\ntask: no\n")
	zoxide := installFake(t, "fake-zoxide", "zoxide", zoxideReply)
	t.Setenv("PATH", claude.dir+string(os.PathListSeparator)+zoxide.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return claude
}

// openWorld is a private server for the open tests. Session s1 is the invoking client's session and holds the pane
// that runs orrery. Session s2 is the existing session NAME. Session s3 is the other client's session.
type openWorld struct {
	client   *tmuxctl.Client
	name     string
	invoking string
	other    string
	claude   fakeTool
}

// setupOpen installs the open fakes with zoxideReply, then attaches the invoking client to s1 and the other client to s3.
func setupOpen(t *testing.T, zoxideReply string) openWorld {
	t.Helper()
	claude := installOpenFakes(t, zoxideReply)
	client, name := newServer(t)
	newSession(t, client, "s1", t.TempDir())
	invoking := attachClient(t, client, name, "s1", 80, 24)
	newSession(t, client, "s2", t.TempDir())
	newSession(t, client, "s3", t.TempDir())
	other := attachClient(t, client, name, "s3", 100, 30)
	return openWorld{client: client, name: name, invoking: invoking, other: other, claude: claude}
}

// runInPane starts orrery with args in a new pane of s1, so the command runs with TMUX set. The pane then prints
// "exit STATUS" and waits. It returns the pane's ID.
func (w openWorld) runInPane(t *testing.T, args string) string {
	t.Helper()
	command := fmt.Sprintf("%s -L %s %s; echo exit $?; sleep 300", orreryPath(), w.name, args)
	return splitPane(t, w.client, "s1", command)
}

// exampleProjectDir returns a directory named example-project under a temp directory.
func exampleProjectDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "example-project")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("make directory: %v", err)
	}
	return dir
}

func TestOpenExistingSessionSwitchesOnlyInvokingClient(t *testing.T) {
	w := setupOpen(t, "")
	pane := w.runInPane(t, "open s2")
	waitForScreen(t, w.client, pane, "exit 0")
	if got := clientSession(t, w.client, w.invoking); got != "s2" {
		t.Fatalf("invoking client shows session %s, want s2", got)
	}
	if got := clientSession(t, w.client, w.other); got != "s3" {
		t.Fatalf("other client shows session %s, want s3", got)
	}
}

func TestOpenZoxideFastMatchCreatesSessionAndSwitchesInvokingClient(t *testing.T) {
	dir := exampleProjectDir(t)
	w := setupOpen(t, "  9.0 "+dir+"\n")
	pane := w.runInPane(t, "open example-project")
	waitForScreen(t, w.client, pane, "> continue latest in ")
	sendKeys(t, w.client, pane, false, "Enter")
	waitForScreen(t, w.client, pane, "exit 0")
	if got := clientSession(t, w.client, w.invoking); got != "example-project" {
		t.Fatalf("invoking client shows session %s, want example-project", got)
	}
	if got := clientSession(t, w.client, w.other); got != "s3" {
		t.Fatalf("other client shows session %s, want s3", got)
	}
	got, err := w.client.Run(t.Context(), "show-options", "-v", "-t", "=example-project:", protocol.OptionDir)
	if err != nil {
		t.Fatalf("read %s: %v", protocol.OptionDir, err)
	}
	if strings.TrimSpace(got) != dir {
		t.Fatalf("%s = %q, want %q", protocol.OptionDir, strings.TrimSpace(got), dir)
	}
	w.claude.waitForStarted(t, 1)
	if args := readLines(t, filepath.Join(w.claude.soleRun(t), "args")); !slices.Equal(args, []string{"--continue"}) {
		t.Fatalf("claude argv %q, want --continue", args)
	}
}

func TestOpenZoxideFastMatchOffersArchivedResume(t *testing.T) {
	dir := exampleProjectDir(t)
	w := setupOpen(t, "  9.0 "+dir+"\n")
	archivePath, err := archive.Path(os.Getenv)
	if err != nil {
		t.Fatalf("archive.Path: %v", err)
	}
	transcript := filepath.Join(t.TempDir(), "example-transcript.jsonl")
	writeFile(t, transcript, "{}\n", 0o600)
	gone := filepath.Join(t.TempDir(), "example-gone-transcript.jsonl")
	for _, record := range []archive.Record{
		{Dir: dir, Session: "s2", ClaudeSession: "example-claude-session-kept", Transcript: transcript,
			Summary: "example summary", Closed: 100},
		{Dir: dir, Session: "s2", ClaudeSession: "example-claude-session-gone", Transcript: gone,
			Summary: "example gone summary", Closed: 200},
	} {
		if err = archive.Append(archivePath, record); err != nil {
			t.Fatalf("archive.Append: %v", err)
		}
	}

	pane := w.runInPane(t, "open example-project")
	waitForScreen(t, w.client, pane, "> resume example-claude-session-kept: example summary")
	shown := screen(t, w.client, pane)
	for _, unwanted := range []string{"example-claude-session-gone", "example gone summary", "continue latest in"} {
		if strings.Contains(shown, unwanted) {
			t.Fatalf("picker shows %q, want no row for it:\n%s", unwanted, shown)
		}
	}
	if !strings.Contains(shown, "start fresh in ") {
		t.Fatalf("picker lacks the start fresh row:\n%s", shown)
	}
	if got := w.claude.runs(t); got != 0 {
		t.Fatalf("fake claude ran %d times before Enter, want 0", got)
	}

	sendKeys(t, w.client, pane, false, "Enter")
	waitForScreen(t, w.client, pane, "exit 0")
	if got := clientSession(t, w.client, w.invoking); got != "example-project" {
		t.Fatalf("invoking client shows session %s, want example-project", got)
	}
	if got := clientSession(t, w.client, w.other); got != "s3" {
		t.Fatalf("other client shows session %s, want s3", got)
	}
	w.claude.waitForStarted(t, 1)
	if args := readLines(t, filepath.Join(w.claude.soleRun(t), "args")); !slices.Equal(args,
		[]string{"--resume", "example-claude-session-kept"}) {
		t.Fatalf("claude argv %q, want --resume example-claude-session-kept", args)
	}
}

func TestOpenOutsideTmuxAttachesNamedSession(t *testing.T) {
	installOpenFakes(t, "")
	client, name := newServer(t)
	newSession(t, client, "s2", t.TempDir())
	known := knownClients(t, client)
	startScript(t, fmt.Sprintf("stty cols 80 rows 24; %s -L %s open s2", orreryPath(), name))
	waitForNewClient(t, client, "s2", known)
}

func TestOpenNewSessionOutsideTmuxCreatesDirectoryAndAttaches(t *testing.T) {
	claude := installOpenFakes(t, "")
	root := t.TempDir()
	client, name := newServer(t)
	newSession(t, client, "s1", t.TempDir())
	known := knownClients(t, client)
	startScript(t, fmt.Sprintf("stty cols 80 rows 24; %s -L %s open new example-new --root %s",
		orreryPath(), name, root))
	waitForNewClient(t, client, "example-new", known)

	dir := filepath.Join(root, "example-new")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("directory %s: %v", dir, err)
	}
	got, err := client.Run(t.Context(), "show-options", "-v", "-t", "=example-new:", protocol.OptionDir)
	if err != nil {
		t.Fatalf("read %s: %v", protocol.OptionDir, err)
	}
	if strings.TrimSpace(got) != dir {
		t.Fatalf("%s = %q, want %q", protocol.OptionDir, strings.TrimSpace(got), dir)
	}
	claude.waitForStarted(t, 1)
}

func TestOpenNewSessionDefaultsToPersonalUnderHome(t *testing.T) {
	claude := installOpenFakes(t, "")
	home := os.Getenv("HOME")
	personal := filepath.Join(home, "Personal")
	if err := os.Mkdir(personal, 0o700); err != nil {
		t.Fatalf("make directory: %v", err)
	}
	client, name := newServer(t)
	newSession(t, client, "s1", t.TempDir())
	known := knownClients(t, client)
	startScript(t, fmt.Sprintf("stty cols 80 rows 24; %s -L %s open new example-new", orreryPath(), name))
	waitForNewClient(t, client, "example-new", known)

	dir := filepath.Join(personal, "example-new")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("directory %s: %v", dir, err)
	}
	if _, err := os.Stat(filepath.Join(home, "example-new")); !os.IsNotExist(err) {
		t.Fatalf("directory %s exists or cannot be checked: %v", filepath.Join(home, "example-new"), err)
	}
	got, err := client.Run(t.Context(), "show-options", "-v", "-t", "=example-new:", protocol.OptionDir)
	if err != nil {
		t.Fatalf("read %s: %v", protocol.OptionDir, err)
	}
	if strings.TrimSpace(got) != dir {
		t.Fatalf("%s = %q, want %q", protocol.OptionDir, strings.TrimSpace(got), dir)
	}
	claude.waitForStarted(t, 1)
}

func TestOpenPickerEnterRunsFirstActionAndSwitchesInvokingClient(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{name: "words", args: "open two words"},
		{name: "intent", args: "open --intent 'two words'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := exampleProjectDir(t)
			w := setupOpen(t, "  9.0 "+dir+"\n")
			pane := w.runInPane(t, tc.args)
			waitForScreen(t, w.client, pane, "> pane ")
			sendKeys(t, w.client, pane, false, "Enter")
			waitForScreen(t, w.client, pane, "exit 0")
			if got := clientSession(t, w.client, w.invoking); got != "example-project" {
				t.Fatalf("invoking client shows session %s, want example-project", got)
			}
			if got := clientSession(t, w.client, w.other); got != "s3" {
				t.Fatalf("other client shows session %s, want s3", got)
			}
			w.claude.waitForStarted(t, 2)
		})
	}
}

func TestOpenPickerEscCreatesNothing(t *testing.T) {
	dir := exampleProjectDir(t)
	w := setupOpen(t, "  9.0 "+dir+"\n")
	before := sessionNames(t, w.client)
	pane := w.runInPane(t, "open two words")
	waitForScreen(t, w.client, pane, "> pane ")
	sendKeys(t, w.client, pane, false, "Escape")
	waitForScreen(t, w.client, pane, "exit 0")
	if got := clientSession(t, w.client, w.invoking); got != "s1" {
		t.Fatalf("invoking client shows session %s, want s1", got)
	}
	if got := clientSession(t, w.client, w.other); got != "s3" {
		t.Fatalf("other client shows session %s, want s3", got)
	}
	if got := sessionNames(t, w.client); !slices.Equal(got, before) {
		t.Fatalf("sessions %v after Esc, want %v", got, before)
	}
	if got := w.claude.runs(t); got != 1 {
		t.Fatalf("fake claude ran %d times, want only the intent run", got)
	}
}

func TestOpenRemoteDotRefusesWithoutCreating(t *testing.T) {
	installOpenFakes(t, "")
	client, name := newServer(t)
	newSession(t, client, "s1", t.TempDir())
	before := sessionNames(t, client)
	out := runOrrery(t, "", nil, "-L", name, "open", "--remote", ".")
	wantExit(t, out, 1)
	if !strings.Contains(out.stderr, "open .: the current directory is on the remote host") {
		t.Fatalf("stderr %q, want the open . remote refusal", out.stderr)
	}
	if got := sessionNames(t, client); !slices.Equal(got, before) {
		t.Fatalf("sessions %v after refusal, want %v", got, before)
	}
}

func TestOpenRefusesIntentWithArgs(t *testing.T) {
	out := runOrrery(t, "", nil, "-L", "orrery-integration-unused", "open", "--intent", "two words", "NAME")
	wantExit(t, out, 2)
	if !strings.Contains(out.stderr, "--intent takes no ARGS") || !strings.Contains(out.stderr, "usage: orrery") {
		t.Fatalf("stderr %q, want the --intent refusal with usage text", out.stderr)
	}
}

// newRecentSession starts session name in a temporary directory once the wall clock has passed the activity of every
// session the server lists, so that name is the most recently active session. Session activity has one-second
// resolution.
func newRecentSession(t *testing.T, client *tmuxctl.Client, name string) {
	t.Helper()
	out, err := client.Run(t.Context(), "list-sessions", "-F", "#{session_activity}")
	if err != nil {
		t.Fatalf("list-sessions: %v", err)
	}
	var latest int64
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		n, parseErr := strconv.ParseInt(line, 10, 64)
		if parseErr != nil {
			t.Fatalf("session_activity %q: %v", line, parseErr)
		}
		latest = max(latest, n)
	}
	waitUntil(t, "wall clock to pass the sessions' last activity", func() bool {
		return time.Now().Unix() > latest
	})
	newSession(t, client, name, t.TempDir())
}

// setupRecentOpen is setupOpen with session s2 created last, so that s2 is the most recent session. The invoking
// client shows s1, and the other client shows s3.
func setupRecentOpen(t *testing.T) openWorld {
	t.Helper()
	claude := installOpenFakes(t, "")
	client, name := newServer(t)
	newSession(t, client, "s1", t.TempDir())
	invoking := attachClient(t, client, name, "s1", 80, 24)
	newSession(t, client, "s3", t.TempDir())
	other := attachClient(t, client, name, "s3", 200, 60)
	newRecentSession(t, client, "s2")
	return openWorld{client: client, name: name, invoking: invoking, other: other, claude: claude}
}

// treePIDs returns the IDs of the processes whose command line ends in tree --client TTY, which are the tree
// processes orrery starts for tty.
func treePIDs(tty string) ([]int, error) {
	paths, err := filepath.Glob("/proc/[0-9]*/cmdline")
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	var pids []int
	for _, path := range paths {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
		n := len(args)
		if n < 3 || args[n-3] != "tree" || args[n-2] != "--client" || args[n-1] != tty {
			continue
		}
		pid, convErr := strconv.Atoi(filepath.Base(filepath.Dir(path)))
		if convErr != nil {
			return nil, fmt.Errorf("process ID of %s: %w", path, convErr)
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// killTreesAtCleanup kills the tree processes for tty when the test ends, so that no tree outlives its test.
func killTreesAtCleanup(t *testing.T, tty string) {
	t.Helper()
	t.Cleanup(func() {
		pids, err := treePIDs(tty)
		if err != nil {
			t.Errorf("find tree processes for %s: %v", tty, err)
		}
		for _, pid := range pids {
			if killErr := syscall.Kill(pid, syscall.SIGKILL); killErr != nil {
				t.Errorf("kill tree process %d: %v", pid, killErr)
			}
		}
	})
}

// terminalSize returns the columns and rows of the terminal process pid reads from. For a tree popup that terminal is
// the popup.
func terminalSize(t *testing.T, pid int) (int, int, error) {
	t.Helper()
	pty, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/0", pid))
	if err != nil {
		return 0, 0, fmt.Errorf("terminal of process %d: %w", pid, err)
	}
	out, err := exec.CommandContext(t.Context(), "stty", "-F", pty, "size").Output()
	if err != nil {
		return 0, 0, fmt.Errorf("stty size of %s: %w", pty, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("stty size of %s printed %q", pty, out)
	}
	rows, rowsErr := strconv.Atoi(fields[0])
	cols, colsErr := strconv.Atoi(fields[1])
	if rowsErr != nil || colsErr != nil {
		return 0, 0, fmt.Errorf("stty size of %s printed %q", pty, out)
	}
	return cols, rows, nil
}

// treePopupError returns an error unless a tree for tty runs on a terminal smaller than the client at tty. A popup
// drawn on another client takes that client's size, so a popup on the wrong client fails the size check.
func treePopupError(t *testing.T, client *tmuxctl.Client, tty string) error {
	t.Helper()
	var listed *clientRow
	rows := clientRows(t, client)
	for i := range rows {
		if rows[i].tty == tty {
			listed = &rows[i]
		}
	}
	if listed == nil {
		return fmt.Errorf("client %s not listed in %v", tty, rows)
	}
	pids, err := treePIDs(tty)
	if err != nil {
		return err
	}
	if len(pids) == 0 {
		return fmt.Errorf("no tree for client %s", tty)
	}
	cols, lines, err := terminalSize(t, pids[0])
	if err != nil {
		return err
	}
	if cols >= listed.width || lines >= listed.height {
		return fmt.Errorf("tree terminal is %dx%d, not smaller than client %dx%d",
			cols, lines, listed.width, listed.height)
	}
	return nil
}

// waitForTreePopup waits until treePopupError for tty is nil.
func waitForTreePopup(t *testing.T, client *tmuxctl.Client, tty string) {
	t.Helper()
	waitUntil(t, "tree popup on client "+tty, func() bool {
		return treePopupError(t, client, tty) == nil
	})
}

func TestOpenNoArgumentShowsTreeOnInvokingClient(t *testing.T) {
	w := setupRecentOpen(t)
	killTreesAtCleanup(t, w.invoking)
	killTreesAtCleanup(t, w.other)
	pane := w.runInPane(t, "open")
	waitForScreen(t, w.client, pane, "exit 0")
	if got := clientSession(t, w.client, w.invoking); got != "s2" {
		t.Fatalf("invoking client shows session %s, want s2", got)
	}
	if got := clientSession(t, w.client, w.other); got != "s3" {
		t.Fatalf("other client shows session %s, want s3", got)
	}
	waitForTreePopup(t, w.client, w.invoking)
	pids, err := treePIDs(w.other)
	if err != nil {
		t.Fatalf("find tree processes for other client: %v", err)
	}
	if len(pids) != 0 {
		t.Fatalf("tree processes %v started for other client %s", pids, w.other)
	}
}

func TestOpenNoArgumentOutsideTmuxAttachesRecentAndShowsTree(t *testing.T) {
	installOpenFakes(t, "")
	client, name := newServer(t)
	newSession(t, client, "s1", t.TempDir())
	newRecentSession(t, client, "s2")
	known := knownClients(t, client)
	startScript(t, fmt.Sprintf("stty cols 80 rows 24; %s -L %s open", orreryPath(), name))
	tty := waitForNewClient(t, client, "s2", known)
	killTreesAtCleanup(t, tty)
	waitForTreePopup(t, client, tty)
}

func TestOpenNamedSessionStartsNoTree(t *testing.T) {
	w := setupOpen(t, "")
	killTreesAtCleanup(t, w.invoking)
	killTreesAtCleanup(t, w.other)
	pane := w.runInPane(t, "open s2")
	waitForScreen(t, w.client, pane, "exit 0")
	for _, tty := range []string{w.invoking, w.other} {
		pids, err := treePIDs(tty)
		if err != nil {
			t.Fatalf("find tree processes for %s: %v", tty, err)
		}
		if len(pids) != 0 {
			t.Fatalf("tree processes %v started for %s by open s2", pids, tty)
		}
	}
}
