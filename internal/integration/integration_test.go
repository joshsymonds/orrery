// Package integration_test builds the orrery and orreryd binaries and proves each orrery subcommand
// by running the built orrery against private tmux servers and a listening test socket.
package integration_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
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

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/push"
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
	return p.exitOutcome(t)
}

// exitOutcome returns the exit outcome once orreryd exits, failing the test if it is still running after waitLimit.
func (p *orreryProcess) exitOutcome(t *testing.T) outcome {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(waitLimit):
		t.Fatal("orreryd did not exit within the wait limit")
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

// newServer returns a client for a unique private tmux server, killed at cleanup.
func newServer(t *testing.T) (*tmuxctl.Client, string) {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("random server name: %v", err)
	}
	name := fmt.Sprintf("orrery-integration-%d-%x", os.Getpid(), suffix)
	client, err := tmuxctl.New(tmuxctl.Server{Name: name})
	if err != nil {
		t.Fatalf("New(Name=%q): %v", name, err)
	}
	t.Cleanup(func() {
		if _, killErr := client.Run(context.Background(), "kill-server"); killErr != nil {
			t.Errorf("kill-server: %v", killErr)
		}
	})
	return client, name
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

	daemon := startOrreryd(t, scrubbedEnv("ORRERY_NTFY_URL_FILE="+urlFile, "XDG_STATE_HOME="+t.TempDir()),
		"-L", name, "--socket", socket,
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
	daemon := startOrreryd(t, scrubbedEnv("XDG_STATE_HOME="+dir), "-L", server, "--socket", socket,
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
			out := runCommand(t, "", "orreryd", "", scrubbedEnv("XDG_STATE_HOME="+t.TempDir()), tc.args...)
			wantExit(t, out, 2)
			if !strings.HasPrefix(out.stderr, "orreryd: ") {
				t.Fatalf("stderr %q, want orreryd: prefix", out.stderr)
			}
		})
	}
}

// pushEndpoint is a fake Web Push service: a TLS server that records each request it receives, and the user agent
// key and auth secret that a subscription to it carries.
type pushEndpoint struct {
	server   *httptest.Server
	key      *ecdh.PrivateKey
	auth     []byte
	requests chan pushRequest
}

// pushRequest is one POST a fake endpoint received.
type pushRequest struct {
	encoding string
	body     []byte
}

// newPushEndpoint starts a fake endpoint on 127.0.0.1 with fresh subscription keys. Its server closes at cleanup.
func newPushEndpoint(t *testing.T) *pushEndpoint {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate user agent key: %v", err)
	}
	auth := make([]byte, 16)
	if _, err = rand.Read(auth); err != nil {
		t.Fatalf("generate auth secret: %v", err)
	}
	ep := &pushEndpoint{key: key, auth: auth, requests: make(chan pushRequest, 8)}
	ep.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read push body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ep.requests <- pushRequest{encoding: req.Header.Get("Content-Encoding"), body: body}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(ep.server.Close)
	return ep
}

// subscription returns the subscription a client registers for the endpoint.
func (ep *pushEndpoint) subscription() push.Subscription {
	return push.Subscription{
		Endpoint: ep.server.URL + "/push/example",
		P256dh:   base64.RawURLEncoding.EncodeToString(ep.key.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(ep.auth),
	}
}

// registerPush stores the subscriptions for endpoints in the push store under stateHome, where orreryd reads them.
func registerPush(t *testing.T, stateHome string, endpoints ...*pushEndpoint) {
	t.Helper()
	store, err := push.NewStore(filepath.Join(stateHome, "orrery", "push.json"), []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("open push store: %v", err)
	}
	for _, ep := range endpoints {
		if err = store.Add(ep.subscription()); err != nil {
			t.Fatalf("register push endpoint: %v", err)
		}
	}
}

// startPushDaemon starts orreryd for server with the fake claude script, the push store under stateHome, and
// SSL_CERT_FILE trusting the certificate of ep's server. It waits for the listener and returns the daemon and socket.
func startPushDaemon(t *testing.T, server, dir, stateHome string, ep *pushEndpoint) (*orreryProcess, string) {
	t.Helper()
	fakeClaude := filepath.Join(dir, "fake-claude.sh")
	writeFile(t, fakeClaude, fakeClaudeScript, 0o700)
	roots := filepath.Join(dir, "push-roots.pem")
	writeFile(t, roots, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ep.server.Certificate().Raw})),
		0o600)
	socket := filepath.Join(dir, "orreryd.sock")
	daemon := startOrreryd(t, scrubbedEnv("XDG_STATE_HOME="+stateHome, "SSL_CERT_FILE="+roots), "-L", server,
		"--socket", socket, "--claude", fakeClaude, "--summary-model", exampleContextName+"="+exampleModel,
		"--push-host", "127.0.0.1")
	daemon.waitForListener(t, socket)
	return daemon, socket
}

// nextPush returns the next request ep received, failing the test after waitLimit.
func nextPush(t *testing.T, ep *pushEndpoint) pushRequest {
	t.Helper()
	select {
	case got := <-ep.requests:
		return got
	case <-time.After(waitLimit):
		t.Fatal("no push request within the wait limit")
	}
	return pushRequest{}
}

// assertPushed reads the next request ep received and fails unless it is one aes128gcm body that decrypts to a
// notification for pane in state.
func assertPushed(t *testing.T, ep *pushEndpoint, pane string, state protocol.State) {
	t.Helper()
	got := nextPush(t, ep)
	if got.encoding != "aes128gcm" {
		t.Fatalf("Content-Encoding %q, want aes128gcm", got.encoding)
	}
	var note map[string]string
	if err := json.Unmarshal(decryptPush(t, got.body, ep.key, ep.auth), &note); err != nil {
		t.Fatalf("decode notification: %v", err)
	}
	if note["pane"] != pane || note["state"] != string(state) {
		t.Fatalf("notification pane %q state %q, want pane %q state %q", note["pane"], note["state"], pane, state)
	}
}

// decryptPush is the user agent side of RFC 8291 for an aes128gcm body with one record. It returns the plaintext
// without its padding delimiter.
func decryptPush(t *testing.T, body []byte, uaPrivate *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	const (
		saltLen   = 16
		keyIDLen  = 65
		headerLen = saltLen + 4 + 1 + keyIDLen
		recordLen = 4096
		delimiter = 0x02
	)
	if len(body) <= headerLen {
		t.Fatalf("push body is %d bytes, shorter than its header", len(body))
	}
	salt := body[:saltLen]
	if rs := binary.BigEndian.Uint32(body[saltLen : saltLen+4]); rs != recordLen {
		t.Fatalf("record size %d, want %d", rs, recordLen)
	}
	if idlen := int(body[saltLen+4]); idlen != keyIDLen {
		t.Fatalf("key id length %d, want %d", idlen, keyIDLen)
	}
	keyID := body[saltLen+5 : headerLen]
	asPublic, err := ecdh.P256().NewPublicKey(keyID)
	if err != nil {
		t.Fatalf("key id is not a P-256 point: %v", err)
	}
	secret, err := uaPrivate.ECDH(asPublic)
	if err != nil {
		t.Fatalf("ecdh: %v", err)
	}
	ikm, err := hkdf.Key(sha256.New, secret, authSecret,
		"WebPush: info\x00"+string(uaPrivate.PublicKey().Bytes())+string(keyID), 32)
	if err != nil {
		t.Fatalf("derive ikm: %v", err)
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		t.Fatalf("derive cek: %v", err)
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		t.Fatalf("derive nonce: %v", err)
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatalf("aes: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	padded, err := gcm.Open(nil, nonce, body[headerLen:], nil)
	if err != nil {
		t.Fatalf("open record: %v", err)
	}
	if len(padded) == 0 || padded[len(padded)-1] != delimiter {
		t.Fatalf("record lacks its last-record delimiter")
	}
	return padded[:len(padded)-1]
}

func TestOrrerydPushesEncryptedNotifications(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	dir := t.TempDir()
	stateHome := filepath.Join(dir, "state")
	endpoint := newPushEndpoint(t)
	registerPush(t, stateHome, endpoint)
	daemon, socket := startPushDaemon(t, name, dir, stateHome, endpoint)
	transcript := filepath.Join(dir, "transcript.jsonl")
	writeFile(t, transcript, exampleTranscript, 0o600)

	sendHook(t, socket, pane, dir, map[string]any{
		"hook_event_name": "PermissionRequest",
		"tool_name":       "example-tool",
		"tool_input":      map[string]any{"command": "example"},
	})
	assertPushed(t, endpoint, pane, protocol.StateWaiting)

	sendHook(t, socket, pane, dir, map[string]any{
		"hook_event_name":        "Stop",
		"transcript_path":        transcript,
		"last_assistant_message": exampleLastMessage,
	})
	assertPushed(t, endpoint, pane, protocol.StateDone)

	// orreryd awaits every notification before it exits, so a request still queued after the stop is an extra one.
	wantExit(t, daemon.stop(t), 0)
	if extra := len(endpoint.requests); extra != 0 {
		t.Fatalf("%d push requests beyond one per transition", extra)
	}
}

func TestOrrerydPushFailureHidesEndpointURL(t *testing.T) {
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	dir := t.TempDir()
	stateHome := filepath.Join(dir, "state")
	live := newPushEndpoint(t)
	closed := newPushEndpoint(t)
	registerPush(t, stateHome, live, closed)
	closed.server.Close()
	daemon, socket := startPushDaemon(t, name, dir, stateHome, live)

	sendHook(t, socket, pane, dir, map[string]any{
		"hook_event_name": "PermissionRequest",
		"tool_name":       "example-tool",
		"tool_input":      map[string]any{"command": "example"},
	})
	assertPushed(t, live, pane, protocol.StateWaiting)

	out := daemon.stop(t)
	wantExit(t, out, 0)
	if !strings.Contains(out.stderr, "post push notification") {
		t.Fatalf("stderr %q lacks the logged push failure", out.stderr)
	}
	if endpoint := closed.subscription().Endpoint; strings.Contains(out.stderr, endpoint) {
		t.Fatalf("stderr %q contains the endpoint URL %s", out.stderr, endpoint)
	}
}

func TestOrrerydWithoutPushStoreExitsOne(t *testing.T) {
	// Without XDG_STATE_HOME or HOME the push store has no path, so orreryd exits before it listens.
	var env []string
	for _, entry := range scrubbedEnv() {
		if !strings.HasPrefix(entry, "XDG_STATE_HOME=") && !strings.HasPrefix(entry, "HOME=") {
			env = append(env, entry)
		}
	}
	socket := filepath.Join(t.TempDir(), "orreryd.sock")
	daemon := startOrreryd(t, env, "--socket", socket)
	out := daemon.exitOutcome(t)
	wantExit(t, out, 1)
	if !strings.Contains(out.stderr, "push store") {
		t.Fatalf("stderr %q, want a push store error", out.stderr)
	}
}
